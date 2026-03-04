package common

import "errors"

var (
	// ErrInvalidConfig 无效配置
	ErrInvalidConfig = errors.New("invalid config")
	
	// ErrNoAvailableOutbound 无可用出站处理器
	ErrNoAvailableOutbound = errors.New("no available outbound")
	
	// ErrDialTimeout 拨号超时
	ErrDialTimeout = errors.New("dial timeout")
	
	// ErrAuthFailed 身份认证失败
	ErrAuthFailed = errors.New("authentication failed")
	
	// ErrRateLimitExceeded 触发速率限制
	ErrRateLimitExceeded = errors.New("rate limit exceeded")
	
	// ErrCircuitBreakerOpen 熔断器已开启
	ErrCircuitBreakerOpen = errors.New("circuit breaker open")
	
	// ErrInvalidProtocol 协议格式错误或不支持
	ErrInvalidProtocol = errors.New("invalid protocol")
	
	// ErrReplayAttack 检测到重放攻击
	ErrReplayAttack = errors.New("replay attack detected")

	// ErrSessionNotFound 会话不存在
	ErrSessionNotFound = errors.New("session not found")
)
