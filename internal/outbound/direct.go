package outbound

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/nodeox/NodePro/internal/common"
)

// DirectHandler 直接出站处理器，支持 TCP 和 UDP
type DirectHandler struct {
	name    string
	group   string
	dialer  *net.Dialer
}

// NewDirectHandler 创建一个新的 Direct 出站处理器
func NewDirectHandler(name, group string) *DirectHandler {
	return &DirectHandler{
		name:  name,
		group: group,
		dialer: &net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		},
	}
}

// Dial 根据元数据中的 Network 进行拨号
func (d *DirectHandler) Dial(ctx context.Context, meta common.SessionMeta) (net.Conn, error) {
	network := meta.Network
	if network == "" {
		network = "tcp" // 默认为 tcp
	}
	
	switch network {
	case "tcp", "tcp4", "tcp6":
		return d.dialer.DialContext(ctx, network, meta.Target)
	case "udp", "udp4", "udp6":
		// 对于 UDP，net.Dial 返回的是一个已经 "connect" 的 UDPConn，
		// 它实现了 net.Conn 接口，可以像 TCP 一样使用 Read/Write。
		return d.dialer.DialContext(ctx, network, meta.Target)
	default:
		return nil, fmt.Errorf("unsupported network type: %s", network)
	}
}

// HealthCheck 健康检查总是返回满分
func (d *DirectHandler) HealthCheck(ctx context.Context) float64 {
	return 1.0
}

func (d *DirectHandler) Name() string {
	return d.name
}

func (d *DirectHandler) Group() string {
	return d.group
}
