package common

import (
	"context"
	"net"
	"time"
)

const (
	DialTimeout = 10 * time.Second
)

// SessionMeta 会话元数据，用于全链路追踪和路由
type SessionMeta struct {
	ID        string            // 唯一会话 ID (UUID)
	TraceID   string            // OpenTelemetry Trace ID
	UserID    string            // 租户/用户 ID
	Source    net.Addr          // 来源地址
	Target    string            // 最终目标地址 (host:port)
	Network   string            // 网络类型 ("tcp", "udp")
	HopChain  []string          // 多跳链路列表
	RouteTags map[string]string // 路由标签
	CreatedAt time.Time         // 创建时间
}

// InboundHandler 入站处理器接口
type InboundHandler interface {
	// Start 启动监听，阻塞直到 ctx 取消或发生错误
	Start(ctx context.Context, router Router) error
	
	// Stop 停止监听
	Stop() error
	
	// Addr 获取监听地址
	Addr() net.Addr
}

// OutboundHandler 出站处理器接口
type OutboundHandler interface {
	// Dial 拨号到下一跳或最终目标
	Dial(ctx context.Context, meta SessionMeta) (net.Conn, error)
	
	// HealthCheck 健康检查，返回 0-1 的健康分值
	HealthCheck(ctx context.Context) float64
	
	// Name 节点名称
	Name() string
	
	// Group 所属节点组
	Group() string
}

// Router 路由器接口
type Router interface {
	// Route 根据会话元数据选择最佳出站处理器
	Route(meta SessionMeta) (OutboundHandler, error)
	
	// AddOutbound 添加出站处理器
	AddOutbound(out OutboundHandler)
	
	// RemoveOutbound 移除出站处理器
	RemoveOutbound(name string)
	
	// UpdateRules 更新路由规则
	UpdateRules(rules []RoutingRule)

	// SetResolver 设置解析器
	SetResolver(res Resolver)
}

// Resolver 解析器接口
type Resolver interface {
	LookupIP(ctx context.Context, host string) ([]net.IP, error)
	ResolveFakeIP(ip net.IP) (string, bool)
}

// RoutingRule 路由规则定义
type RoutingRule struct {
	Type          string   // "domain", "ip", "user", "default"
	Pattern       string   // 匹配模式 (如 *.google.com, 10.0.0.0/8)
	Outbound      string   // 指定出站名称
	OutboundGroup string   // 指定出站组
	Strategy      string   // 选择策略 (如 "round-robin", "lowest-latency")
}

// FlowController 流控接口
type FlowController interface {
	// ShouldPause 是否由于缓冲区压力应暂停读取
	ShouldPause() bool
	
	// OnConsumed 通知系统已从缓冲区消费了 n 字节
	OnConsumed(n int)
	
	// WindowSize 获取当前可用窗口大小
	WindowSize() int
	
	// Reset 重置流控状态
	Reset()
}
