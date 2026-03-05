package inbound

import (
	"context"
	"net"
	"time"

	"github.com/google/uuid"
	"github.com/nodeox/NodePro/internal/common"
	"go.uber.org/zap"
)

// TCPForwardHandler 原生 TCP 端口转发处理器
type TCPForwardHandler struct {
	listenAddr    string
	useProxyProto bool
	targetAddr    string // 固定的目标地址，如 "1.1.1.1:80"
	listener      net.Listener
	logger        *zap.Logger
}

func NewTCPForwardHandler(listen string, useProxyProto bool, target string, logger *zap.Logger) *TCPForwardHandler {
	return &TCPForwardHandler{
		listenAddr:    listen,
		useProxyProto: useProxyProto,
		targetAddr:    target,
		logger:        logger,
	}
}

func (h *TCPForwardHandler) Start(ctx context.Context, router common.Router) error {
	var err error
	h.listener, err = common.Listen("tcp", h.listenAddr, h.useProxyProto)
	if err != nil { return err }

	h.logger.Info("TCP Forward listening", zap.String("addr", h.listenAddr), zap.String("target", h.targetAddr), zap.Bool("proxy_protocol", h.useProxyProto))

	for {
		conn, err := h.listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done(): return nil
			default: continue
			}
		}
		go h.handle(conn, router)
	}
}

func (h *TCPForwardHandler) handle(conn net.Conn, router common.Router) {
	defer conn.Close()

	// 1. 尝试嗅探域名 (SNI/Host)
	sniffedConn, result := common.SniffConn(conn)
	conn = sniffedConn // 使用包装后的连接

	target := h.targetAddr
	if result != nil && result.Domain != "" {
		_, port, _ := net.SplitHostPort(target)
		target = net.JoinHostPort(result.Domain, port)
		h.logger.Debug("Sniffed domain from TCP forward", zap.String("domain", result.Domain))
	}

	sessionID := uuid.New().String()
	meta := common.SessionMeta{
		ID:        sessionID,
		Source:    conn.RemoteAddr(),
		Target:    target,
		Network:   "tcp",
		HopChain:  []string{target},
		CreatedAt: time.Now(),
	}
	ctx := context.WithValue(context.Background(), "session_id", sessionID)
	outbound, err := router.Route(meta)
	if err != nil {
		h.logger.Error("no route for forward", zap.String("target", h.targetAddr))
		return
	}

	targetConn, err := outbound.Dial(ctx, meta)
	if err != nil {
		h.logger.Error("failed to dial target", zap.String("target", h.targetAddr), zap.Error(err))
		return
	}
	defer targetConn.Close()

	common.DualRelay(ctx, conn, targetConn, "anonymous", nil, nil)
}

func (h *TCPForwardHandler) Stop() error {
	if h.listener != nil { return h.listener.Close() }
	return nil
}

func (h *TCPForwardHandler) Addr() net.Addr { return h.listener.Addr() }
