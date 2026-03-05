package e2e

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/nodeox/NodePro/internal/agent"
	"github.com/nodeox/NodePro/internal/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestNPChainE2E(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. 启动目标 HTTP Server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "npchain-success")
	}))
	defer ts.Close()

	// 2. 配置并启动 Egress Node
	egressCfg := &common.Config{
		Node: common.NodeConfig{ID: "egress-node", Type: "egress"},
		Inbounds: []common.InboundConfig{
			{Protocol: "npchain", Listen: "127.0.0.1:12443"},
		},
		Outbounds: []common.OutboundConfig{
			{Name: "direct", Protocol: "direct", Group: "default"},
		},
		Routing: common.RoutingConfig{
			Rules: []common.RoutingRuleConfig{{Type: "default", Outbound: "direct"}},
		},
		Controller: common.ControllerConfig{
			Insecure: true,
			CertFile: "../certs/server.crt",
			KeyFile:  "../certs/server.key",
			CAFile:   "../certs/server.crt",
		},
	}

	egressAgent, err := agent.New(egressCfg, logger)
	require.NoError(t, err)
	go egressAgent.Start(ctx)

	// 3. 配置并启动 Ingress Node
	ingressCfg := &common.Config{
		Node: common.NodeConfig{ID: "ingress-node", Type: "ingress"},
		Inbounds: []common.InboundConfig{
			{Protocol: "http", Listen: "127.0.0.1:18080"},
		},
		Outbounds: []common.OutboundConfig{
			{
				Name:      "egress-relay",
				Protocol:  "npchain",
				Group:     "default",
				Address:   "127.0.0.1:12443",
				Transport: "quic",
			},
		},
		Routing: common.RoutingConfig{
			Rules: []common.RoutingRuleConfig{{Type: "default", Outbound: "egress-relay"}},
		},
		Controller: common.ControllerConfig{
			Insecure: true,
			CertFile: "../certs/server.crt",
			KeyFile:  "../certs/server.key",
			CAFile:   "../certs/server.crt",
		},
	}

	ingressAgent, err := agent.New(ingressCfg, logger)
	require.NoError(t, err)
	
	go ingressAgent.Start(ctx)

	time.Sleep(1 * time.Second) // 等待两个节点启动

	// 4. 发送客户端请求到 Ingress 的 HTTP 代理
	t.Run("Ingress_to_Egress", func(t *testing.T) {
		proxyURL, _ := url.Parse("http://127.0.0.1:18080")
		httpClient := &http.Client{
			Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
			Timeout:   2 * time.Second,
		}
		resp, err := httpClient.Get(ts.URL)
		require.NoError(t, err)
		body, _ := io.ReadAll(resp.Body)
		assert.Equal(t, "npchain-success", string(body))
		resp.Body.Close()
	})

	egressAgent.Stop()
	ingressAgent.Stop()
}
