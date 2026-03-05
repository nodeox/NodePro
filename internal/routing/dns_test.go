package routing

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/nodeox/NodePro/internal/common"
	"go.uber.org/zap"
)

// MockRouter 模拟路由器
type MockRouter struct {
	mock.Mock
}

func (m *MockRouter) Route(meta common.SessionMeta) (common.OutboundHandler, error) {
	args := m.Called(meta)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(common.OutboundHandler), args.Error(1)
}

func (m *MockRouter) AddOutbound(out common.OutboundHandler) {}
func (m *MockRouter) RemoveOutbound(name string)           {}
func (m *MockRouter) UpdateRules(rules []common.RoutingRule) {}
func (m *MockRouter) SetResolver(res common.Resolver) {}

// MockOutbound 模拟出站
type MockOutbound struct {
	name string
}
func (m *MockOutbound) Dial(ctx context.Context, meta common.SessionMeta) (net.Conn, error) { return nil, nil }
func (m *MockOutbound) HealthCheck(ctx context.Context) float64 { return 1.0 }
func (m *MockOutbound) Name() string { return m.name }
func (m *MockOutbound) Group() string { return "default" }

func TestSmartResolver_Basic(t *testing.T) {
	logger := zap.NewNop()
	router := new(MockRouter)
	
	// 设置路由行为：google.com 走代理，baidu.com 直连
	router.On("Route", mock.MatchedBy(func(meta common.SessionMeta) bool {
		return meta.Target == "google.com:443"
	})).Return(&MockOutbound{name: "proxy"}, nil)
	
	router.On("Route", mock.Anything).Return(&MockOutbound{name: "direct"}, nil)

	sr := NewSmartResolver(router, logger)
	
	// 添加上游 (使用公共 DNS 测试，确保网络通畅)
	sr.AddUpstreamByString("local", "223.5.5.5:53")
	sr.AddUpstreamByString("remote", "8.8.8.8:53")

	t.Run("ResolveDirect", func(t *testing.T) {
		ips, err := sr.LookupIP(context.Background(), "baidu.com")
		assert.NoError(t, err)
		assert.NotEmpty(t, ips)
	})

	t.Run("ResolveProxy", func(t *testing.T) {
		ips, err := sr.LookupIP(context.Background(), "google.com")
		assert.NoError(t, err)
		assert.NotEmpty(t, ips)
	})
	
	t.Run("FakeIPTest", func(t *testing.T) {
		sr := NewSmartResolver(router, logger)
		err := sr.EnableFakeIP("198.18.0.0/16")
		assert.NoError(t, err)

		// 1. 解析域名，应该返回 Fake IP
		ips, err := sr.LookupIP(context.Background(), "fake-test.com")
		assert.NoError(t, err)
		assert.Len(t, ips, 1)
		fakeIP := ips[0]
		assert.True(t, fakeIP.String() == "198.18.0.1" || fakeIP.String() == "198.18.0.2")

		// 2. 还原 Fake IP
		domain, ok := sr.ResolveFakeIP(fakeIP)
		assert.True(t, ok)
		assert.Equal(t, "fake-test.com", domain)
	})
}
