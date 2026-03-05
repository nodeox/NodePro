package routing

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/nodeox/NodePro/internal/common"
)

// BaseRouter 基础路由器实现
type BaseRouter struct {
	mu        sync.RWMutex
	rules     []common.RoutingRule
	outbounds map[string]common.OutboundHandler
	geoip     *GeoIPMatcher
	resolver  common.Resolver // 智能解析器接口
}

func NewRouter() *BaseRouter {
	return &BaseRouter{
		outbounds: make(map[string]common.OutboundHandler),
	}
}

func (r *BaseRouter) SetResolver(res common.Resolver) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resolver = res
}

func (r *BaseRouter) Route(meta common.SessionMeta) (common.OutboundHandler, error) {
	// 0. 处理 Fake IP 还原
	host, port, _ := net.SplitHostPort(meta.Target)
	if ip := net.ParseIP(host); ip != nil && r.resolver != nil {
		if domain, ok := r.resolver.ResolveFakeIP(ip); ok {
			meta.Target = net.JoinHostPort(domain, port)
		}
	}

	// 1. 在锁内匹配规则并确定出站或组
	r.mu.RLock()
	var matchedRule *common.RoutingRule
	for _, rule := range r.rules {
		if r.match(rule, meta) {
			matchedRule = &rule
			break
		}
	}

	// 如果没有规则匹配，尝试找默认出站
	if matchedRule == nil {
		out, ok := r.outbounds["default"]
		r.mu.RUnlock()
		if ok {
			return out, nil
		}
		return nil, common.ErrNoAvailableOutbound
	}

	// 处理指定出站名
	if matchedRule.Outbound != "" {
		out, ok := r.outbounds[matchedRule.Outbound]
		r.mu.RUnlock()
		if ok {
			return out, nil
		}
		return nil, common.ErrNoAvailableOutbound
	}

	// 处理出站组逻辑
	group := matchedRule.OutboundGroup
	strategy := matchedRule.Strategy
	
	// 收集候选者
	var candidates []common.OutboundHandler
	for _, out := range r.outbounds {
		if out.Group() == group {
			candidates = append(candidates, out)
		}
	}
	r.mu.RUnlock()

	if len(candidates) == 0 {
		return nil, common.ErrNoAvailableOutbound
	}

	// 2. 在锁外执行可能的耗时策略（如健康检查）
	return r.selectByStrategy(candidates, strategy)
}

func (r *BaseRouter) selectByStrategy(candidates []common.OutboundHandler, strategy string) (common.OutboundHandler, error) {
	if strategy == "lowest-latency" {
		var best common.OutboundHandler
		maxScore := -1.0
		for _, c := range candidates {
			// 在锁外调用 HealthCheck，避免阻塞其他 Goroutine 路由
			score := c.HealthCheck(context.Background())
			if score > maxScore {
				maxScore = score
				best = c
			}
		}
		if best != nil {
			return best, nil
		}
	}
	return candidates[0], nil
}

func (r *BaseRouter) match(rule common.RoutingRule, meta common.SessionMeta) bool {
	switch rule.Type {
	case "default":
		return true
	case "domain":
		host, _, _ := net.SplitHostPort(meta.Target)
		return strings.HasSuffix(host, strings.TrimPrefix(rule.Pattern, "*"))
	case "ip":
		host, _, _ := net.SplitHostPort(meta.Target)
		ip := net.ParseIP(host)
		_, ipNet, _ := net.ParseCIDR(rule.Pattern)
		return ipNet != nil && ipNet.Contains(ip)
	case "geoip":
		host, _, _ := net.SplitHostPort(meta.Target)
		ip := net.ParseIP(host)
		if ip != nil && r.geoip != nil {
			return r.geoip.Match(ip, rule.Pattern)
		}
	case "asn":
		host, _, _ := net.SplitHostPort(meta.Target)
		ip := net.ParseIP(host)
		if ip != nil && r.geoip != nil {
			var asn uint
			fmt.Sscanf(rule.Pattern, "%d", &asn)
			return r.geoip.MatchASN(ip, asn)
		}
	}
	return false
}

func (r *BaseRouter) AddOutbound(out common.OutboundHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.outbounds[out.Name()] = out
}

func (r *BaseRouter) RemoveOutbound(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.outbounds, name)
}

func (r *BaseRouter) UpdateRules(rules []common.RoutingRule) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rules = rules
}

func (r *BaseRouter) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rules = nil
	r.outbounds = make(map[string]common.OutboundHandler)
}
