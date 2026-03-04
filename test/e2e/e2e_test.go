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

func TestEndToEndNPChain(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. 启动 Mock HTTP 目标服务器
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "hello from target")
	}))
	defer ts.Close()

	// 2. 动态端口分配与 Egress 节点配置
	// 避免端口冲突
	egressPort := "10444"
	ingressPort := "11081"

	egressCfg := &common.Config{
		Node: common.NodeConfig{ID: "egress", Type: "egress"},
		Inbounds: []common.InboundConfig{
			{Protocol: "np-chain", Listen: "127.0.0.1:" + egressPort},
		},
		Outbounds: []common.OutboundConfig{
			{Name: "direct", Protocol: "direct", Group: "default"},
		},
		Controller: common.ControllerConfig{
			Enabled:  false,
			Insecure: true,
			CertFile: "../../configs/certs/server.crt",
			KeyFile:  "../../configs/certs/server.key",
			CAFile:   "../../configs/certs/ca.crt",
		},
	}
	
	egressAg, err := agent.New(egressCfg, logger)
	assert.NoError(t, err)
	defer egressAg.Stop() // 确保测试失败也会清理
	
	go func() {
		if err := egressAg.Start(ctx); err != nil {
			logger.Error("egress start failed", zap.Error(err))
		}
	}()
	time.Sleep(1 * time.Second) // 等待监听建立

	// 3. 配置并启动 Ingress 节点
	ingressCfg := &common.Config{
		Node: common.NodeConfig{ID: "ingress", Type: "ingress"},
		Inbounds: []common.InboundConfig{
			{Protocol: "socks5", Listen: "127.0.0.1:" + ingressPort, Auth: common.AuthConfig{Enabled: false}},
		},
		Outbounds: []common.OutboundConfig{
			{
				Name:      "proxy",
				Protocol:  "np-chain",
				Address:   "127.0.0.1:" + egressPort,
				Transport: "quic",
			},
		},
		Routing: common.RoutingConfig{
			Rules: []common.RoutingRuleConfig{
				{Type: "default", Outbound: "proxy"},
			},
		},
		Controller: common.ControllerConfig{
			Enabled:  false,
			Insecure: true,
			CertFile: "../../configs/certs/client.crt",
			KeyFile:  "../../configs/certs/client.key",
			CAFile:   "../../configs/certs/ca.crt",
		},
	}
	
	ingressAg, err := agent.New(ingressCfg, logger)
	assert.NoError(t, err)
	defer ingressAg.Stop() // 确保清理

	go func() {
		if err := ingressAg.Start(ctx); err != nil {
			logger.Error("ingress start failed", zap.Error(err))
		}
	}()
	time.Sleep(1 * time.Second)

	// 4. 使用 SOCKS5 代理发送请求
	dialer, err := proxy.SOCKS5("tcp", "127.0.0.1:"+ingressPort, nil, proxy.Direct)
	assert.NoError(t, err)

	httpClient := &http.Client{
		Transport: &http.Transport{
			Dial:              dialer.Dial,
			DisableKeepAlives: true,
		},
		Timeout: 5 * time.Second,
	}

	resp, err := httpClient.Get(ts.URL)
	if !assert.NoError(t, err) {
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, "hello from target", string(body))
}
