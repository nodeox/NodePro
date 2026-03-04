package e2e

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nodeox/NodePro/internal/agent"
	"github.com/nodeox/NodePro/internal/common"
	"go.uber.org/zap"
	"github.com/stretchr/testify/assert"
	"golang.org/x/net/proxy"
)

func TestHotReload(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. 启动目标服务器
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "hello")
	}))
	defer ts.Close()
// 2. 初始配置: 全部走 Direct
// 修复：必须提供有效的证书路径，即使不开启 Controller
listenPort := "13081"
cfg := &common.Config{
	Node: common.NodeConfig{ID: "hot-agent", Type: "ingress"},
	Inbounds: []common.InboundConfig{
		{Protocol: "socks5", Listen: "127.0.0.1:" + listenPort, Auth: common.AuthConfig{Enabled: false}},
	},
	Outbounds: []common.OutboundConfig{
		{Name: "direct", Protocol: "direct", Group: "default"},
	},
	Routing: common.RoutingConfig{
		Rules: []common.RoutingRuleConfig{
			{Type: "default", Outbound: "direct"},
		},
	},
	Observability: common.ObservabilityConfig{
		Metrics: common.MetricsConfig{Enabled: false},
		Logging: common.LoggingConfig{Level: "info"},
	},
	Controller: common.ControllerConfig{
		Enabled:  false,
		Insecure: true,
		CertFile: "../../configs/certs/client.crt",
		KeyFile:  "../../configs/certs/client.key",
		CAFile:   "../../configs/certs/ca.crt",
	},
}

ag, err := agent.New(cfg, logger)
assert.NoError(t, err)
defer ag.Stop()

go func() {
	if err := ag.Start(ctx); err != nil {
		logger.Error("agent start failed", zap.Error(err))
	}
}()
time.Sleep(1 * time.Second) // 增加等待时间确保监听建立

// 3. 验证初始连接正常 (禁用 Keep-Alive 以测试路由)
dialer, err := proxy.SOCKS5("tcp", "127.0.0.1:"+listenPort, nil, proxy.Direct)
assert.NoError(t, err)

httpClient := &http.Client{
	Transport: &http.Transport{
		Dial:              dialer.Dial,
		DisableKeepAlives: true,
	},
	Timeout: 5 * time.Second,
}

	
	resp, err := httpClient.Get(ts.URL)
	if assert.NoError(t, err) {
		body, _ := io.ReadAll(resp.Body)
		assert.Equal(t, "hello", string(body))
		resp.Body.Close()
	}

	// 4. 热更新配置: 更改路由规则到一个不存在的出站
	newCfg := *cfg
	newCfg.Routing.Rules = []common.RoutingRuleConfig{
		{Type: "default", Outbound: "void"}, 
	}
	
	err = ag.ApplyConfig(&newCfg)
	assert.NoError(t, err)
	time.Sleep(200 * time.Millisecond)

	// 5. 验证更新生效 (预期请求失败)
	_, err = httpClient.Get(ts.URL)
	assert.Error(t, err, "should fail as outbound 'void' does not exist")

	// 6. 再次热更新: 恢复正常
	ag.ApplyConfig(cfg)
	time.Sleep(200 * time.Millisecond)
	resp, err = httpClient.Get(ts.URL)
	if assert.NoError(t, err, "should work again after restoring config") {
		resp.Body.Close()
	}

	ag.Stop()
}
