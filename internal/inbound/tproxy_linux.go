//go:build linux
package inbound

import (
	"context"
	"fmt"
	"net"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/nodeox/NodePro/internal/common"
	"go.uber.org/zap"
	"golang.org/x/sys/unix"
)

// TProxyHandler 处理 Linux TPROXY 透明代理
type TProxyHandler struct {
	listenAddr    string
	useProxyProto bool
	listener      net.Listener
	logger        *zap.Logger
}

func NewTProxyHandler(listen string, useProxyProto bool, logger *zap.Logger) *TProxyHandler {
	return &TProxyHandler{listenAddr: listen, useProxyProto: useProxyProto, logger: logger}
}

func (h *TProxyHandler) Start(ctx context.Context, router common.Router) error {
	// 使用 ListenConfig 设置 IP_TRANSPARENT 选项
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				// 允许绑定到非本地地址
				unix.SetsockoptInt(int(fd), unix.SOL_IP, unix.IP_TRANSPARENT, 1)
			})
		},
	}

	var err error
	h.listener, err = lc.Listen(ctx, "tcp", h.listenAddr)
	if err != nil {
		return fmt.Errorf("failed to listen tproxy: %w", err)
	}

	if h.useProxyProto {
		h.listener = &common.ProxyProtoListener{Listener: h.listener}
	}

	h.logger.Info("TPROXY listening", zap.String("addr", h.listenAddr), zap.Bool("proxy_protocol", h.useProxyProto))

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

func (h *TProxyHandler) handle(conn net.Conn, router common.Router) {
	defer conn.Close()

	// TPROXY 环境下，LocalAddr 就是原始的目标地址
	target := conn.LocalAddr().String()

	// 尝试嗅探域名 (SNI/Host)
	sniffedConn, result := common.SniffConn(conn)
	conn = sniffedConn

	if result != nil && result.Domain != "" {
		_, port, _ := net.SplitHostPort(target)
		target = net.JoinHostPort(result.Domain, port)
		h.logger.Debug("Sniffed domain from TPROXY", zap.String("domain", result.Domain))
	}
	
	sessionID := uuid.New().String()
	meta := common.SessionMeta{
		ID:        sessionID,
		Source:    conn.RemoteAddr(),
		Target:    target,
		Network:   "tcp",
		CreatedAt: time.Now(),
	}

	ctx := context.WithValue(context.Background(), "session_id", sessionID)
	outbound, err := router.Route(meta)
	if err != nil { return }

	targetConn, err := outbound.Dial(ctx, meta)
	if err != nil { return }
	defer targetConn.Close()

	common.DualRelay(ctx, conn, targetConn, "tproxy", nil, nil)
}

func (h *TProxyHandler) Stop() error {
	if h.listener != nil { return h.listener.Close() }
	return nil
}

func (h *TProxyHandler) Addr() net.Addr { return h.listener.Addr() }
