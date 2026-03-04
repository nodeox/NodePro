package routing

import (
	"github.com/nodeox/NodePro/internal/common"
)

// GenerateDefaultSplitConfig 生成推荐的分流配置
func GenerateDefaultSplitConfig(proxyOutbound string) []common.RoutingRule {
	return []common.RoutingRule{
		// 1. 本地/私有地址直连
		{Type: "ip", Pattern: "127.0.0.0/8", Outbound: "direct"},
		{Type: "ip", Pattern: "192.168.0.0/16", Outbound: "direct"},
		{Type: "ip", Pattern: "10.0.0.0/8", Outbound: "direct"},
		
		// 2. 中国大陆流量直连
		{Type: "geoip", Pattern: "CN", Outbound: "direct"},
		
		// 3. 其他默认走代理
		{Type: "default", Outbound: proxyOutbound},
	}
}
