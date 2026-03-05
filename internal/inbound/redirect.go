package inbound

import (
	"context"
	"net"
	"time"

	"github.com/google/uuid"
	"github.com/nodeox/NodePro/internal/common"
	"go.uber.org/zap"
)

// RedirectHandler 透明代理处理器 (Linux iptables REDIRECT 兼容)
type RedirectHandler struct {
	listenAddr    string
	useProxyProto bool
	listener      net.Listener
	logger        *zap.Logger
}

func NewRedirectHandler(listen string, useProxyProto bool, logger *zap.Logger) *RedirectHandler {
	return &RedirectHandler{listenAddr: listen, useProxyProto: useProxyProto, logger: logger}
}

func (h *RedirectHandler) Start(ctx context.Context, router common.Router) error {
	var err error
	h.listener, err = common.Listen("tcp", h.listenAddr, h.useProxyProto)
	if err != nil { return err }

	h.logger.Info("Redirect (Transparent) listening", zap.String("addr", h.listenAddr), zap.Bool("proxy_protocol", h.useProxyProto))

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

func (h *RedirectHandler) handle(conn net.Conn, router common.Router) {
	defer conn.Close()

	// 关键：提取原始目标地址
	target, err := getOriginalDst(conn)
	if err != nil {
		h.logger.Error("failed to get original destination", zap.Error(err))
		return
	}

	// 尝试嗅探域名 (SNI/Host)
	sniffedConn, result := common.SniffConn(conn)
	conn = sniffedConn

	if result != nil && result.Domain != "" {
		_, port, _ := net.SplitHostPort(target)
		target = net.JoinHostPort(result.Domain, port)
		h.logger.Debug("Sniffed domain from REDIRECT", zap.String("domain", result.Domain))
	}

	sessionID := uuid.New().String()
	meta := common.SessionMeta{
		ID:        sessionID,
		Source:    conn.RemoteAddr(),
		Target:    target,
		Network:   "tcp",
		CreatedAt: time.Now(),
	}

	outbound, err := router.Route(meta)
	if err != nil { return }

	ctx := context.WithValue(context.Background(), "session_id", sessionID)
	targetConn, err := outbound.Dial(ctx, meta)
	if err != nil { return }
	defer targetConn.Close()

	common.DualRelay(ctx, conn, targetConn, "transparent", nil, nil)
}

func (h *RedirectHandler) Stop() error {
	if h.listener != nil { return h.listener.Close() }
	return nil
}

func (h *RedirectHandler) Addr() net.Addr { return h.listener.Addr() }
