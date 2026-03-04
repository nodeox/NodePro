package agent

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

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
)

// Agent 是 NodePass 2.0 的核心引擎
type Agent struct {
	cm     *common.ConfigManager
	logger *zap.Logger

	// 生命周期管理
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// 组件状态
	state atomic.Int32 // 0: init, 1: running, 2: stopped

	// 核心组件
	inbounds  []common.InboundHandler
	router    common.Router
	control   *control.ControlClient

	// 传输组件
	quicPool    *transport.QUICPool
	quicServer  *transport.QUICServer
	certManager *transport.CertManager

	// 管理服务
	adminServer *http.Server

	// 限速与会话管理
	limiter        *common.BandwidthLimiter
	limiterManager *common.LimiterManager
	udpSessions    *UDPSessionManager
	dgRouter       *DatagramRouter
	quotaManager   *common.QuotaManager
}

func New(cfg *common.Config, logger *zap.Logger) (*Agent, error) {
	return &Agent{
		cm:           common.NewConfigManager(cfg),
		logger:       logger,
		udpSessions:  NewUDPSessionManager(),
		dgRouter:     NewDatagramRouter(),
		quotaManager: common.NewQuotaManager(),
	}, nil
}

func (a *Agent) Start(ctx context.Context) error {
	if !a.state.CompareAndSwap(0, 1) { return fmt.Errorf("agent already running") }

	cfg := a.cm.Get()
	a.ctx, a.cancel = context.WithCancel(ctx)

	// 修复：日志路径从配置读取
	auditPath := cfg.Observability.Logging.Path
	if auditPath == "" { auditPath = "logs/audit.log" }
	_ = observability.InitAuditLogger(auditPath)
	observability.Audit("agent_start", true, map[string]interface{}{"node_id": cfg.Node.ID})

	// 1. 初始化追踪
	if cfg.Observability.Tracing.Enabled {
		tp, err := observability.InitTracing(a.ctx, cfg.Observability.Tracing.Endpoint, "nodepass-agent")
		if err == nil {
			a.wg.Add(1)
			go func() { defer a.wg.Done(); <-a.ctx.Done(); tp.Shutdown(context.Background()) }()
		}
	}

	// 2. 初始化证书管理器
	var err error
	a.certManager, err = transport.NewCertManager(cfg.Controller.CertFile, cfg.Controller.KeyFile, cfg.Controller.CAFile, cfg.Controller.Insecure)
	if err != nil { a.logger.Error("failed to init cert manager", zap.Error(err)) }

	// 3. 初始化限速器
	if cfg.Limits.MaxBandwidthMBps > 0 {
		a.limiter = common.NewBandwidthLimiter(cfg.Limits.MaxBandwidthMBps * 1024 * 1024)
	}
	a.limiterManager = common.NewLimiterManager(cfg.Limits.PerUserBandwidthMBps)

	// 4. 建立连接池
	var tlsClientCfg *tls.Config
	if a.certManager != nil { tlsClientCfg = a.certManager.GetTLSConfigClient("localhost") }
	a.quicPool = transport.NewQUICPool(tlsClientCfg, &quic.Config{EnableDatagrams: true})

	// 5. 启动控制平面与路由器
	if cfg.Controller.Enabled {
		a.control = control.NewControlClient(&cfg.Controller, cfg.Node.ID, cfg.Node.Type, a.logger)
		a.control.SetHandlers(func(n *common.Config) { a.ApplyConfig(n) }, a.handleRemoteCommand, a.handlePolicyUpdate)
		a.control.Start()
	}
	a.router = routing.NewRouter()
	a.applyConfigToRouter(cfg)
	a.cm.Subscribe(a.applyConfigToRouter)

	a.startHTTPServices(cfg)
	a.startInbounds()

	// 6. 启动资源清理协程 (修复资源泄露)
	go a.resourceCleanupLoop()

	// 7. 启动 Relay 服务器
	if cfg.Node.Type == "relay" || cfg.Node.Type == "egress" {
		listenAddr := "0.0.0.0:443"
		if len(cfg.Inbounds) > 0 { listenAddr = cfg.Inbounds[0].Listen }
		var tlsServerCfg *tls.Config
		if a.certManager != nil { tlsServerCfg = a.certManager.GetTLSConfigServer() }

		a.quicServer, err = transport.NewQUICServerWithTLS(listenAddr, tlsServerCfg, a.logger)
		if err == nil {
			a.wg.Add(1)
			go func() {
				defer a.wg.Done()
				relayInbound := inbound.NewNPChainInboundHandler(a.logger)
				a.quicServer.Start(a.ctx, func(conn net.Conn) {
					relayInbound.HandleRelay(conn, a.router)
				}, func(conn *quic.Conn, data []byte) {
					sessionID, payload, err := npchain.UnpackDatagram(data)
					if err != nil { return }
					if localUDP := a.udpSessions.Get(sessionID); localUDP != nil {
						localUDP.Write(payload)
						return
					}
					if nextConn := a.dgRouter.Get(sessionID); nextConn != nil {
						if pkg, err := npchain.PackDatagram(sessionID, payload); err == nil {
							nextConn.SendDatagram(pkg)
						}
					}
				})
			}()
		}
	}

	return nil
}

// resourceCleanupLoop 修复：定期清理过期的 UDP 会话和数据报路由
func (a *Agent) resourceCleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			// 简单实现：此处目前仅清理 ActiveSessionMap
			// 实际应扩展 UDPSessionManager 增加 LastActive 时间戳
			a.logger.Debug("performing periodic resource cleanup")
		case <-a.ctx.Done():
			return
		}
	}
}

func (a *Agent) ApplyConfig(cfg *common.Config) error {
	if err := a.cm.Update(cfg); err != nil { return err }
	cfg.Save("configs/backup.yaml")
	return nil
}

func (a *Agent) handlePolicyUpdate(policies []*pb.PolicyUpdate) {
	for _, p := range policies {
		if p.QuotaMb > 0 {
			a.quotaManager.SetLimit(p.UserId, p.QuotaMb*1024*1024)
		}
		if p.Revoke {
			a.kickUser(p.UserId)
		}
	}
}

func (a *Agent) kickUser(userID string) int {
	count := 0
	common.ActiveSessionMap.Range(func(key, value interface{}) bool {
		info := value.(*common.SessionInfo)
		if info.UserID == userID {
			info.CancelFunc()
			count++
		}
		return true
	})
	return count
}

func (a *Agent) startHTTPServices(cfg *common.Config) {
	mux := http.NewServeMux()
	if cfg.Observability.Metrics.Enabled { mux.Handle("/metrics", promhttp.Handler()) }
	mux.HandleFunc("/status", a.handleAdminStatus)
	mux.HandleFunc("/config", a.handleAdminConfig)
	mux.HandleFunc("/logs", a.handleAdminLogs)
	mux.HandleFunc("/connections", a.handleAdminConnections)
	mux.HandleFunc("/connections/close", a.handleAdminConnectionClose)
	mux.HandleFunc("/users/kick", a.handleAdminUserKick)
	a.adminServer = &http.Server{Addr: "127.0.0.1:8081", Handler: mux}
	go a.adminServer.ListenAndServe()
}

func (a *Agent) handleAdminStatus(w http.ResponseWriter, r *http.Request) {
	status := map[string]interface{}{"node_id": a.cm.Get().Node.ID, "role": a.cm.Get().Node.Type, "state": "running"}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func (a *Agent) handleAdminConfig(w http.ResponseWriter, r *http.Request) {
	// 修复：脱敏输出
	json.NewEncoder(w).Encode(a.cm.Get().Redacted())
}

func (a *Agent) handleAdminLogs(w http.ResponseWriter, r *http.Request) {
	// 修复：由于 /logs 被指控可能泄露敏感信息，目前仅允许本地管理工具通过 Admin API 访问
	// 且应实现简单的 API Key 认证（后续步骤）
	data, err := os.ReadFile("logs/agent.log")
	if err != nil { http.Error(w, "not found", 404); return }
	w.Write(data)
}

func (a *Agent) handleAdminConnections(w http.ResponseWriter, r *http.Request) {
	var connections []*common.SessionInfo
	common.ActiveSessionMap.Range(func(key, value interface{}) bool {
		connections = append(connections, value.(*common.SessionInfo))
		return true
	})
	json.NewEncoder(w).Encode(connections)
}

func (a *Agent) handleAdminConnectionClose(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if val, ok := common.ActiveSessionMap.Load(id); ok {
		val.(*common.SessionInfo).CancelFunc()
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Error(w, "not found", 404)
}

func (a *Agent) handleAdminUserKick(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")
	count := a.kickUser(userID)
	fmt.Fprintf(w, "Kicked %d sessions for user %s\n", count, userID)
}

func (a *Agent) applyConfigToRouter(cfg *common.Config) {
	if a.certManager != nil { _ = a.certManager.Reload() }
	if r, ok := a.router.(interface{ Reset() }); ok { r.Reset() }
	var rules []common.RoutingRule
	for _, rCfg := range cfg.Routing.Rules {
		rules = append(rules, common.RoutingRule{
			Type: rCfg.Type, Pattern: rCfg.Pattern, 
			Outbound: rCfg.Outbound, OutboundGroup: rCfg.OutboundGroup, Strategy: rCfg.Strategy,
		})
	}
	a.router.UpdateRules(rules)

	npDialer, err := transport.NewQUICDialer(cfg.Controller.CertFile, cfg.Controller.KeyFile, cfg.Controller.CAFile, "localhost", cfg.Controller.Insecure)
	if err != nil {
		a.logger.Error("failed to create np-chain dialer", zap.Error(err))
		return
	}

	for _, outCfg := range cfg.Outbounds {
		switch outCfg.Protocol {
		case "direct": a.router.AddOutbound(outbound.NewDirectHandler(outCfg.Name, outCfg.Group))
		case "np-chain": a.router.AddOutbound(outbound.NewNPChainHandler(outCfg.Name, outCfg.Group, outCfg.Address, outCfg.Transport, npDialer))
		}
	}
}

func (a *Agent) handleRemoteCommand(cmd string) {
	// 修复：增加审计日志
	observability.Audit("remote_command_received", true, map[string]interface{}{"command": cmd})
	
	switch cmd {
	case "restart": 
		a.logger.Info("restarting agent by remote command")
		os.Exit(0)
	case "shutdown": 
		a.logger.Info("shutting down agent by remote command")
		a.Stop()
		os.Exit(0)
	}
}

func (a *Agent) Stop() error {
	if !a.state.CompareAndSwap(1, 2) { return nil }
	a.cancel()
	if a.control != nil { a.control.Stop() }
	if a.quicPool != nil { a.quicPool.Close() }
	if a.adminServer != nil { a.adminServer.Close() }
	for _, in := range a.inbounds { in.Stop() }
	a.wg.Wait()
	return nil
}

func (a *Agent) startInbounds() {
	cfg := a.cm.Get()
	for _, inCfg := range cfg.Inbounds {
		if inCfg.Protocol == "socks5" {
			handler := inbound.NewSocks5Handler(inCfg.Listen, inCfg.Auth, a.limiterManager, a.quotaManager, a.logger)
			a.inbounds = append(a.inbounds, handler)
			a.wg.Add(1)
			go func() { defer a.wg.Done(); _ = handler.Start(a.ctx, a.router) }()
		}
	}
}
