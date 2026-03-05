package agent

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	pb "github.com/nodeox/NodePro/api/proto"
	"github.com/nodeox/NodePro/internal/common"
	"github.com/nodeox/NodePro/internal/control"
	"github.com/nodeox/NodePro/internal/inbound"
	"github.com/nodeox/NodePro/internal/observability"
	"github.com/nodeox/NodePro/internal/outbound"
	"github.com/nodeox/NodePro/internal/protocol/npchain"
	"github.com/nodeox/NodePro/internal/routing"
	"github.com/nodeox/NodePro/internal/transport"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/quic-go/quic-go"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// Agent 核心引擎
type Agent struct {
	cm     *common.ConfigManager
	logger *zap.Logger

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	state atomic.Int32 

	activeInbounds map[string]common.InboundHandler
	inboundMu      sync.Mutex

	router    common.Router
	control   *control.ControlClient
	dns       *routing.SmartResolver

	quicPool    *transport.QUICPool
	quicServer  *transport.QUICServer
	certManager *transport.CertManager
	adminServer *http.Server

	limiter        *common.BandwidthLimiter
	limiterManager *common.LimiterManager
	udpSessions    *UDPSessionManager
	dgRouter       *DatagramRouter
	quotaManager   *common.QuotaManager
}

func New(cfg *common.Config, logger *zap.Logger) (*Agent, error) {
	return &Agent{
		cm:             common.NewConfigManager(cfg),
		logger:         logger,
		activeInbounds: make(map[string]common.InboundHandler),
		udpSessions:    NewUDPSessionManager(),
		dgRouter:       NewDatagramRouter(),
		quotaManager:   common.NewQuotaManager(),
		limiterManager: common.NewLimiterManager(cfg.Limits.PerUserBandwidthMBps),
	}, nil
}

func (a *Agent) Start(ctx context.Context) error {
	if !a.state.CompareAndSwap(0, 1) { return fmt.Errorf("agent already running") }
	cfg := a.cm.Get()
	a.ctx, a.cancel = context.WithCancel(ctx)

	// 初始化服务...
	observability.InitAuditLogger("logs/audit.log")
	a.certManager, _ = transport.NewCertManager(cfg.Controller.CertFile, cfg.Controller.KeyFile, cfg.Controller.CAFile, cfg.Controller.Insecure)
	
	a.router = routing.NewRouter()
	a.dns = routing.NewSmartResolver(a.router, a.logger)
	a.router.SetResolver(a.dns)
	
	// 应用 DNS 配置
	if cfg.Routing.FakeIP.Enabled {
		cidr := cfg.Routing.FakeIP.Range
		if cidr == "" { cidr = "198.18.0.0/16" }
		a.dns.EnableFakeIP(cidr)
		a.logger.Info("FakeIP enabled", zap.String("range", cidr))
	}

	for _, u := range cfg.Routing.DNSUpstreams {
		a.dns.AddUpstreamByString("local", u)
	}
	// TODO: 以后可以在 Config 中支持 remote 组配置
	a.dns.AddUpstreamByString("remote", "8.8.8.8:53")
	a.dns.AddUpstreamByString("remote", "https://dns.google/dns-query")

	a.applyConfigToRouter(cfg)
	a.syncInbounds(cfg)
	a.startHTTPServices(cfg)

	// 订阅配置变更并自动应用
	a.cm.Subscribe(func(newCfg *common.Config) {
		a.logger.Info("Applying new configuration")
		a.applyConfigToRouter(newCfg)
		a.syncInbounds(newCfg)
	})

	// 初始化并启动控制端连接
	if cfg.Controller.Enabled {
		tlsClientCfg := a.certManager.GetTLSConfigClient("localhost") // FIXME: Use correct ServerName
		a.control = control.NewControlClient(&cfg.Controller, cfg.Node.ID, cfg.Node.Type, tlsClientCfg, a.logger)
		a.control.SetHandlers(
			func(newCfg *common.Config) { a.cm.Update(newCfg) },
			a.handleRemoteCommand,
			a.handlePolicyUpdate,
		)
		if err := a.control.Start(); err != nil {
			a.logger.Error("Failed to start control client", zap.Error(err))
		}
	}

	// 启动 QUIC Server 处理入站数据
	if cfg.Node.Type == "relay" || cfg.Node.Type == "egress" || cfg.Node.Type == "ingress" {
		listenAddr := "0.0.0.0:443"
		if len(cfg.Inbounds) > 0 { listenAddr = cfg.Inbounds[0].Listen }
		var tlsServerCfg *tls.Config
		if a.certManager != nil { tlsServerCfg = a.certManager.GetTLSConfigServer() }

		a.quicServer, _ = transport.NewQUICServerWithTLS(listenAddr, tlsServerCfg, a.logger)
		a.wg.Add(1)
		go func() {
			defer a.wg.Done()
			// 获取第一个入站配置中的混淆设置（由于 QUIC Server 是全局的，此处取通用配置）
			var obfs common.ObfsConfig
			for _, in := range cfg.Inbounds {
				if in.Protocol == "npchain" {
					obfs = in.Obfuscation
					break
				}
			}
			relayInbound := inbound.NewNPChainInboundHandler(obfs, a.logger)
			a.quicServer.Start(a.ctx, func(conn net.Conn) {
				relayInbound.HandleRelay(conn, a.router)
			}, func(conn *quic.Conn, data []byte) {
				// 核心：处理 UDP 回传数据
				sessionID, payload, err := npchain.UnpackDatagram(data)
				if err != nil { return }
				
				if session := a.udpSessions.Get(sessionID); session != nil {
					// 将数据写回原始客户端地址
					session.Conn.WriteTo(payload, session.ClientAddr)
				}
			})
		}()
	}

	return nil
}

func (a *Agent) applyConfigToRouter(cfg *common.Config) {
	// 1. 添加出站处理器
	for _, outCfg := range cfg.Outbounds {
		var handler common.OutboundHandler
		switch outCfg.Protocol {
		case "direct":
			handler = outbound.NewDirectHandler(outCfg.Name, outCfg.Group)
		case "npchain":
			if outCfg.Transport == "quic" {
				tlsCfg := a.certManager.GetTLSConfigClient("localhost") // FIXME: Use correct SNI
				dialer := transport.NewQUICDialerWithTLS(tlsCfg)
				handler = outbound.NewNPChainHandler(outCfg.Name, outCfg.Group, outCfg.Address, outCfg.Transport, dialer, outCfg.Obfuscation, outCfg.MultiPath, a.logger)
			}
		// 以后可以扩展 Trojan, SS 等
		}
		if handler != nil {
			a.router.AddOutbound(handler)
		}
	}

	// 2. 更新路由规则
	var rules []common.RoutingRule
	for _, r := range cfg.Routing.Rules {
		rules = append(rules, common.RoutingRule{
			Type:          r.Type,
			Pattern:       r.Pattern,
			Outbound:      r.Outbound,
			OutboundGroup: r.OutboundGroup,
			Strategy:      r.Strategy,
		})
	}
	a.router.UpdateRules(rules)
}

func (a *Agent) syncInbounds(cfg *common.Config) {
	a.inboundMu.Lock()
	defer a.inboundMu.Unlock()

	newInboundMap := make(map[string]common.InboundConfig)
	for _, in := range cfg.Inbounds {
		key := fmt.Sprintf("%s-%s", in.Protocol, in.Listen)
		newInboundMap[key] = in
	}

	// 停止已经移除的入站
	for key, handler := range a.activeInbounds {
		if _, ok := newInboundMap[key]; !ok {
			handler.Stop()
			delete(a.activeInbounds, key)
		}
	}

	// 启动新增的入站
	for key, inCfg := range newInboundMap {
		if _, ok := a.activeInbounds[key]; !ok {
			var handler common.InboundHandler
			switch inCfg.Protocol {
			case "socks5":
				handler = inbound.NewSocks5Handler(inCfg.Listen, inCfg.ProxyProtocol, inCfg.Auth, a.limiterManager, a.quotaManager, a.logger)
			case "http":
				handler = inbound.NewHTTPProxyHandler(inCfg.Listen, inCfg.ProxyProtocol, inCfg.Auth, a.limiterManager, a.quotaManager, a.logger)
			case "tcp-forward":
				target, _ := inCfg.Settings["target"].(string)
				handler = inbound.NewTCPForwardHandler(inCfg.Listen, inCfg.ProxyProtocol, target, a.logger)
			case "tcp-balance":
				var targets []string
				if tSlice, ok := inCfg.Settings["targets"].([]interface{}); ok {
					for _, t := range tSlice {
						if ts, ok := t.(string); ok {
							targets = append(targets, ts)
						}
					}
				}
				handler = inbound.NewTCPBalanceHandler(inCfg.Listen, inCfg.ProxyProtocol, targets, a.logger)
			case "udp-forward":
				target, _ := inCfg.Settings["target"].(string)
				handler = inbound.NewUDPForwardHandler(inCfg.Listen, target, a.limiterManager, a.quotaManager, a.logger, func(id string, addr net.Addr, conn *net.UDPConn) {
					a.udpSessions.Add(id, &UDPSession{Conn: conn, ClientAddr: addr})
				})
			case "udp-range":
				target, _ := inCfg.Settings["target"].(string)
				handler = inbound.NewUDPRangeHandler(inCfg.Listen, target, a.limiterManager, a.quotaManager, a.logger, func(id string, addr net.Addr, conn *net.UDPConn) {
					a.udpSessions.Add(id, &UDPSession{Conn: conn, ClientAddr: addr})
				})
			case "redirect":
				handler = inbound.NewRedirectHandler(inCfg.Listen, inCfg.ProxyProtocol, a.logger)
			case "tproxy":
				handler = inbound.NewTProxyHandler(inCfg.Listen, inCfg.ProxyProtocol, a.logger)
			}

			if handler != nil {
				a.activeInbounds[key] = handler
				go handler.Start(a.ctx, a.router)
			}
		}
	}
}

func (a *Agent) Stop() error {
	a.cancel()
	if a.control != nil {
		a.control.Stop()
	}
	if a.adminServer != nil {
		a.adminServer.Close()
	}
	a.wg.Wait()
	return nil
}

func (a *Agent) ApplyConfig(cfg *common.Config) error {
	return a.cm.Update(cfg)
}

func (a *Agent) startHTTPServices(cfg *common.Config) {
	adminAddr := cfg.Node.AdminAddr
	if adminAddr == "" {
		return // 不启动管理接口
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/status", a.handleAdminStatus)
	mux.HandleFunc("/config", a.handleAdminConfig)
	mux.HandleFunc("/connections", a.handleAdminConnections)
	mux.HandleFunc("/connections/close", a.handleAdminKick)
	mux.HandleFunc("/users/kick", a.handleAdminKickUser)
	mux.HandleFunc("/dns/flush", a.handleAdminDNSFlush)
	mux.HandleFunc("/users/quota/reset", a.handleAdminQuotaReset)
	mux.HandleFunc("/logs", a.handleAdminLogs)
	mux.Handle("/metrics", promhttp.Handler())

	a.adminServer = &http.Server{
		Addr:    adminAddr,
		Handler: mux,
	}

	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		a.logger.Info("Admin API listening", zap.String("addr", adminAddr))
		if err := a.adminServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			a.logger.Error("Admin API server failed", zap.Error(err))
		}
	}()
}

func (a *Agent) handleAdminStatus(w http.ResponseWriter, r *http.Request) {
	cfg := a.cm.Get()
	in, out := common.GetTotalStats()
	status := map[string]interface{}{
		"node_id":         cfg.Node.ID,
		"node_type":       cfg.Node.Type,
		"version":         "2.0",
		"active_sessions": common.GetActiveSessions(),
		"total_bytes_in":  in,
		"total_bytes_out": out,
		"state":           a.state.Load(),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func (a *Agent) handleAdminConfig(w http.ResponseWriter, r *http.Request) {
	cfg := a.cm.Get()
	w.Header().Set("Content-Type", "application/yaml")
	yaml.NewEncoder(w).Encode(cfg)
}

func (a *Agent) handleAdminConnections(w http.ResponseWriter, r *http.Request) {
	var sessions []common.SessionInfo
	common.ActiveSessionMap.Range(func(key, value interface{}) bool {
		s := value.(*common.SessionInfo)
		sessions = append(sessions, *s)
		return true
	})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sessions)
}

func (a *Agent) handleAdminKick(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if val, ok := common.ActiveSessionMap.Load(id); ok {
		s := val.(*common.SessionInfo)
		s.CancelFunc()
		fmt.Fprint(w, "Session closed")
	} else {
		http.Error(w, "Session not found", http.StatusNotFound)
	}
}

func (a *Agent) handleAdminKickUser(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")
	common.ActiveSessionMap.Range(func(key, value interface{}) bool {
		s := value.(*common.SessionInfo)
		if s.UserID == userID {
			s.CancelFunc()
		}
		return true
	})
	fmt.Fprint(w, "User connections closed")
}

func (a *Agent) handleAdminDNSFlush(w http.ResponseWriter, r *http.Request) {
	if a.dns != nil {
		a.dns.Flush()
		fmt.Fprint(w, "DNS cache flushed")
	} else {
		http.Error(w, "DNS resolver not initialized", http.StatusInternalServerError)
	}
}

func (a *Agent) handleAdminLogs(w http.ResponseWriter, r *http.Request) {
	cfg := a.cm.Get()
	logPath := cfg.Observability.Logging.Path
	if logPath == "" {
		http.Error(w, "Log path not configured", http.StatusNotFound)
		return
	}
	
	data, err := os.ReadFile(logPath)
	if err != nil {
		http.Error(w, "Failed to read logs: "+err.Error(), http.StatusInternalServerError)
		return
	}
	
	// 只返回最后 100 行以防太大
	lines := strings.Split(string(data), "\n")
	start := len(lines) - 100
	if start < 0 { start = 0 }
	
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprint(w, strings.Join(lines[start:], "\n"))
}

func (a *Agent) handleAdminQuotaReset(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")
	a.quotaManager.Reset(userID)
	fmt.Fprint(w, "Quota reset")
}

func (a *Agent) handleRemoteCommand(cmd string) {
	a.logger.Info("Received remote command", zap.String("cmd", cmd))
	switch cmd {
	case "restart":
		// TODO: 实现平滑重启
	case "shutdown":
		a.Stop()
	}
}

func (a *Agent) handlePolicyUpdate(policies []*pb.PolicyUpdate) {
	for _, p := range policies {
		a.logger.Info("Updating policy for user", zap.String("user_id", p.UserId))
		if p.Revoke {
			// TODO: 吊销逻辑，比如断开所有活跃连接
			continue
		}
		if p.BandwidthMbps > 0 {
			a.limiterManager.Update(p.UserId, int(p.BandwidthMbps))
		}
		if p.QuotaMb > 0 {
			a.quotaManager.SetLimit(p.UserId, p.QuotaMb*1024*1024)
		}
	}
}
func (a *Agent) handleAdminPorts(w http.ResponseWriter, r *http.Request) {}
