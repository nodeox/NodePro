package outbound

import (
	"context"
	"net"
	"time"

	"github.com/nodeox/NodePro/internal/common"
)

// DirectHandler 直接出站处理器
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

// Dial 直接拨号到目标地址
func (d *DirectHandler) Dial(ctx context.Context, meta common.SessionMeta) (net.Conn, error) {
	return d.dialer.DialContext(ctx, "tcp", meta.Target)
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
