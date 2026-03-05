package inbound

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/nodeox/NodePro/internal/common"
	"go.uber.org/zap"
)

// UDPRangeHandler 处理批量 UDP 端口映射
type UDPRangeHandler struct {
	listenAddr     string // 格式 "0.0.0.0:10000-10100"
	targetBase     string // 格式 "1.1.1.1:20000" (基准目标)
	logger         *zap.Logger
	limiterManager *common.LimiterManager
	quotaManager   *common.QuotaManager
	
	conns      []*net.UDPConn
	sm         *UDPSessionManager
	onNewSession func(id string, addr net.Addr, conn *net.UDPConn)
}

// 注意：由于 common.LimiterManager 才是结构体名，修正一下
func NewUDPRangeHandler(listen, target string, lm *common.LimiterManager, qm *common.QuotaManager, logger *zap.Logger, onNew func(string, net.Addr, *net.UDPConn)) *UDPRangeHandler {
	return &UDPRangeHandler{
		listenAddr:     listen,
		targetBase:     target,
		logger:         logger,
		sm:             NewUDPSessionManager(0),
		limiterManager: lm,
		quotaManager:   qm,
		onNewSession:   onNew,
	}
}

func (h *UDPRangeHandler) Start(ctx context.Context, router common.Router) error {
	host, portRange, err := net.SplitHostPort(h.listenAddr)
	if err != nil {
		return err
	}
	
	ports := strings.Split(portRange, "-")
	if len(ports) != 2 {
		return fmt.Errorf("invalid port range: %s", portRange)
	}
	
	startPort, _ := strconv.Atoi(ports[0])
	endPort, _ := strconv.Atoi(ports[1])
	
	targetHost, targetBasePortStr, _ := net.SplitHostPort(h.targetBase)
	targetBasePort, _ := strconv.Atoi(targetBasePortStr)

	h.logger.Info("UDP Range Forwarding starting", 
		zap.Int("start", startPort), 
		zap.Int("end", endPort),
		zap.String("target_base", h.targetBase))

	go h.sm.CleanupLoop(ctx)

	for p := startPort; p <= endPort; p++ {
		addr := net.JoinHostPort(host, strconv.Itoa(p))
		uaddr, _ := net.ResolveUDPAddr("udp", addr)
		conn, err := net.ListenUDP("udp", uaddr)
		if err != nil {
			h.logger.Warn("failed to bind port in range", zap.Int("port", p), zap.Error(err))
			continue
		}
		h.conns = append(h.conns, conn)
		
		targetPort := targetBasePort + (p - startPort)
		targetAddr := net.JoinHostPort(targetHost, strconv.Itoa(targetPort))
		
		go h.servePort(ctx, conn, targetAddr, router)
	}

	return nil
}

func (h *UDPRangeHandler) servePort(ctx context.Context, conn *net.UDPConn, target string, router common.Router) {
	buf := common.GetBuf()
	defer common.PutBuf(buf)

	for {
		n, raddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}

		srcKey := raddr.String() + "-" + target
		s, isNew, err := h.sm.GetOrCreate(srcKey, target, raddr, conn, false, router)
		if err != nil {
			continue
		}

		if isNew && h.onNewSession != nil {
			h.onNewSession(s.sessionID, raddr, conn)
		}

		limiter := h.limiterManager.GetOrCreate("anonymous")
		go h.sm.ForwardPacket(ctx, s, append([]byte{}, buf[:n]...), target, "anonymous", limiter, h.quotaManager)
	}
}

func (h *UDPRangeHandler) Stop() error {
	for _, c := range h.conns {
		c.Close()
	}
	return nil
}

func (h *UDPRangeHandler) Addr() net.Addr { return nil }
