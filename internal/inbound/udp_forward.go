package inbound

import (
	"context"
	"net"

	"github.com/nodeox/NodePro/internal/common"
	"go.uber.org/zap"
)

// UDPForwardHandler 增强版：支持双流控、配额和会话管理
type UDPForwardHandler struct {
	listenAddr     string
	targetAddr     string
	conn           *net.UDPConn
	logger         *zap.Logger
	sm             *UDPSessionManager
	limiterManager *common.LimiterManager
	quotaManager   *common.QuotaManager

	// 回调给 Agent 的会话管理器
	onNewSession func(id string, addr net.Addr, conn *net.UDPConn)
}

func NewUDPForwardHandler(listen, target string, lm *common.LimiterManager, qm *common.QuotaManager, logger *zap.Logger, onNew func(string, net.Addr, *net.UDPConn)) *UDPForwardHandler {
	return &UDPForwardHandler{
		listenAddr:     listen,
		targetAddr:     target,
		logger:         logger,
		sm:             NewUDPSessionManager(0),
		limiterManager: lm,
		quotaManager:   qm,
		onNewSession:   onNew,
	}
}

func (h *UDPForwardHandler) Start(ctx context.Context, router common.Router) error {
	addr, err := net.ResolveUDPAddr("udp", h.listenAddr)
	if err != nil {
		return err
	}
	h.conn, err = net.ListenUDP("udp", addr)
	if err != nil {
		return err
	}

	h.logger.Info("UDP Forward listening (Full Duplex)", zap.String("addr", h.listenAddr))

	go h.sm.CleanupLoop(ctx)

	buf := common.GetBuf()
	defer common.PutBuf(buf)

	for {
		n, raddr, err := h.conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				h.logger.Error("UDP read error", zap.Error(err))
				continue
			}
		}

		srcKey := raddr.String()
		s, isNew, err := h.sm.GetOrCreate(srcKey, h.targetAddr, raddr, h.conn, false, router)
		if err != nil {
			h.logger.Warn("Failed to route UDP", zap.String("src", srcKey), zap.Error(err))
			continue
		}

		if isNew && h.onNewSession != nil {
			h.onNewSession(s.sessionID, raddr, h.conn)
		}

		// 匿名/默认用户的限流器
		limiter := h.limiterManager.GetOrCreate("anonymous")
		go h.sm.ForwardPacket(ctx, s, append([]byte{}, buf[:n]...), h.targetAddr, "anonymous", limiter, h.quotaManager)
	}
}

func (h *UDPForwardHandler) Stop() error {
	if h.conn != nil {
		return h.conn.Close()
	}
	return nil
}

func (h *UDPForwardHandler) Addr() net.Addr { return h.conn.LocalAddr() }
