package outbound

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"time"

	"github.com/nodeox/NodePro/internal/common"
	"github.com/nodeox/NodePro/internal/protocol/npchain"
	"github.com/nodeox/NodePro/internal/transport"
)

// NPChainHandler NP-Chain 出站处理器，支持健康探测
type NPChainHandler struct {
	name      string
	group     string
	address   string
	transport string
	dialer    *transport.QUICDialer
	aggregate bool
	
	latencyMs atomic.Int64 // 记录最近一次探测延迟
}

// NewNPChainHandler 创建一个新的 NP-Chain 出站处理器并启动探测
func NewNPChainHandler(name, group, address, transportType string, dialer *transport.QUICDialer) *NPChainHandler {
	h := &NPChainHandler{
		name:      name,
		group:     group,
		address:   address,
		transport: transportType,
		dialer:    dialer,
		aggregate: false,
	}
	go h.probeLoop()
	return h
}

func (n *NPChainHandler) probeLoop() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		start := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		conn, err := n.dialer.Dial(ctx, n.address)
		cancel()

		if err == nil {
			elapsed := time.Since(start).Milliseconds()
			n.latencyMs.Store(elapsed)
			common.NodeLatencyMs.WithLabelValues(n.name).Set(float64(elapsed))
			conn.Close()
		} else {
			n.latencyMs.Store(9999)
			common.NodeLatencyMs.WithLabelValues(n.name).Set(9999)
		}
	}
}

// Dial 拨号到下一跳并发送 NP-Chain 头部
func (n *NPChainHandler) Dial(ctx context.Context, meta common.SessionMeta) (net.Conn, error) {
	if n.transport != "quic" {
		return nil, fmt.Errorf("unsupported transport: %s", n.transport)
	}

	conn, err := n.dialer.Dial(ctx, n.address)
	if err != nil {
		return nil, fmt.Errorf("dial failed: %w", err)
	}

	header, err := npchain.EncodeHeader(meta)
	if err != nil {
		conn.Close()
		return nil, err
	}

	if _, err := conn.Write(header); err != nil {
		conn.Close()
		return nil, err
	}

	return conn, nil
}

// HealthCheck 返回健康分值 (0-1)，延迟越低分越高
func (n *NPChainHandler) HealthCheck(ctx context.Context) float64 {
	lat := n.latencyMs.Load()
	if lat <= 0 { return 0.5 } // 初始状态给个中等分
	if lat >= 2000 { return 0.1 }
	return 1.0 - float64(lat)/2000.0
}

func (n *NPChainHandler) Name() string { return n.name }
func (n *NPChainHandler) Group() string { return n.group }
