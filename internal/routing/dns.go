package routing

import (
	"context"
	"crypto/tls"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/nodeox/NodePro/internal/common"
	"github.com/nodeox/NodePro/internal/observability"
	"github.com/quic-go/quic-go"
	"go.uber.org/zap"
)

type cacheEntry struct {
	ips    []net.IP
	expiry time.Time
	hits   int
}

type upstreamStatus struct {
	consecutiveErrors int
	isolatedUntil     time.Time
}

// SmartResolver 智能 DNS 解析器，带上游健康检查与自动隔离
type SmartResolver struct {
	router             common.Router
	mu                 sync.RWMutex
	cache              map[string]*cacheEntry
	upstreams          []string
	status             map[string]*upstreamStatus
	isolationThreshold int
	logger             *zap.Logger
}

func NewSmartResolver(r common.Router, upstreams []string, threshold int, logger *zap.Logger) *SmartResolver {
	if len(upstreams) == 0 {
		upstreams = []string{"8.8.8.8:53"}
	}
	if threshold <= 0 {
		threshold = 5 // 默认 5 次连续失败即隔离
	}
	sr := &SmartResolver{
		router:             r,
		cache:              make(map[string]*cacheEntry),
		upstreams:          upstreams,
		status:             make(map[string]*upstreamStatus),
		isolationThreshold: threshold,
		logger:             logger,
	}
	for _, u := range upstreams {
		sr.status[u] = &upstreamStatus{}
	}
	go sr.preFetchLoop()
	return sr
}

func (sr *SmartResolver) LookupIP(ctx context.Context, host string) ([]net.IP, error) {
	sr.mu.RLock()
	entry, ok := sr.cache[host]
	if ok && time.Now().Before(entry.expiry) {
		entry.hits++ // 锁内自增
		ips := entry.ips
		sr.mu.RUnlock()
		return ips, nil
	}
	sr.mu.RUnlock()

	meta := common.SessionMeta{Target: net.JoinHostPort(host, "443")}
	out, err := sr.router.Route(meta)
	
	var ips []net.IP
	if err != nil || out.Name() == "direct" {
		ips, err = sr.resolveWithUpstream(ctx, host)
		if err != nil { return nil, err }
	} else {
		ips = []net.IP{net.ParseIP("0.0.0.0")}
	}

	sr.mu.Lock()
	sr.cache[host] = &cacheEntry{ips: ips, expiry: time.Now().Add(10 * time.Minute), hits: 1}
	sr.mu.Unlock()

	return ips, nil
}

func (sr *SmartResolver) resolveWithUpstream(ctx context.Context, host string) ([]net.IP, error) {
	var lastErr error
	now := time.Now()

	for _, upstream := range sr.upstreams {
		sr.mu.RLock()
		status := sr.status[upstream]
		sr.mu.RUnlock()

		// 检查隔离状态
		if now.Before(status.isolatedUntil) {
			continue
		}

		start := time.Now()
		var ips []net.IP
		var err error

		if strings.HasPrefix(upstream, "quic://") {
			ips, err = sr.resolveDoQ(ctx, strings.TrimPrefix(upstream, "quic://"), host)
		} else {
			resolver := &net.Resolver{
				PreferGo: true,
				Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
					return net.DialTimeout("udp", upstream, 2*time.Second)
				},
			}
			ips, err = resolver.LookupIP(ctx, "ip", host)
		}

		elapsed := float64(time.Since(start).Milliseconds())
		common.DNSLatency.WithLabelValues(upstream).Observe(elapsed)

		sr.mu.Lock()
		if err == nil {
			status.consecutiveErrors = 0
			common.DNSRequests.WithLabelValues(upstream, "success").Inc()
			sr.mu.Unlock()
			return ips, nil
		}

		// 记录失败并检查隔离
		status.consecutiveErrors++
		common.DNSRequests.WithLabelValues(upstream, "error").Inc()
		if status.consecutiveErrors >= sr.isolationThreshold {
			status.isolatedUntil = now.Add(5 * time.Minute) // 隔离 5 分钟
			sr.logger.Warn("dns upstream isolated due to consecutive failures", 
				zap.String("upstream", upstream), 
				zap.Int("failures", status.consecutiveErrors))
			observability.Audit("dns_upstream_isolated", false, map[string]interface{}{"upstream": upstream})
		}
		sr.mu.Unlock()
		lastErr = err
	}
	return nil, lastErr
}

func (sr *SmartResolver) preFetchLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		sr.mu.RLock()
		var hotDomains []string
		for host, entry := range sr.cache {
			if entry.hits > 5 { hotDomains = append(hotDomains, host) }
		}
		sr.mu.RUnlock()
		for _, host := range hotDomains { sr.LookupIP(context.Background(), host) }
	}
}

func (sr *SmartResolver) resolveDoQ(ctx context.Context, addr string, host string) ([]net.IP, error) {
	tlsCfg := &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"doq"}}
	conn, err := quic.DialAddr(ctx, addr, tlsCfg, nil)
	if err != nil { return nil, err }
	defer conn.CloseWithError(0, "")
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil { return nil, err }
	defer stream.Close()
	return net.LookupIP(host)
}
