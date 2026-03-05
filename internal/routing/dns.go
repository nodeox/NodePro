package routing

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
	"github.com/nodeox/NodePro/internal/common"
	"go.uber.org/zap"
)

// DNSUpstream DNS 上游接口
type DNSUpstream interface {
	Exchange(ctx context.Context, m *dns.Msg) (*dns.Msg, error)
	Address() string
}

// UDPUpstream 传统 UDP DNS
type UDPUpstream struct {
	addr string
}

func (u *UDPUpstream) Address() string { return u.addr }
func (u *UDPUpstream) Exchange(ctx context.Context, m *dns.Msg) (*dns.Msg, error) {
	client := &dns.Client{Net: "udp", Timeout: 2 * time.Second}
	resp, _, err := client.ExchangeContext(ctx, m, u.addr)
	return resp, err
}

// DoHUpstream DNS over HTTPS
type DoHUpstream struct {
	url    string
	client *http.Client
}

func (u *DoHUpstream) Address() string { return u.url }
func (u *DoHUpstream) Exchange(ctx context.Context, m *dns.Msg) (*dns.Msg, error) {
	buf, err := m.Pack()
	if err != nil { return nil, err }

	req, err := http.NewRequestWithContext(ctx, "POST", u.url, strings.NewReader(string(buf)))
	if err != nil { return nil, err }
	req.Header.Set("Content-Type", "application/dns-message")
	req.Header.Set("Accept", "application/dns-message")

	resp, err := u.client.Do(req)
	if err != nil { return nil, err }
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("doh status code: %d", resp.StatusCode)
	}

	respBuf, err := io.ReadAll(resp.Body)
	if err != nil { return nil, err }

	respMsg := new(dns.Msg)
	if err := respMsg.Unpack(respBuf); err != nil { return nil, err }
	return respMsg, nil
}

// DoTUpstream DNS over TLS
type DoTUpstream struct {
	addr      string
	tlsConfig *tls.Config
}

func (u *DoTUpstream) Address() string { return u.addr }
func (u *DoTUpstream) Exchange(ctx context.Context, m *dns.Msg) (*dns.Msg, error) {
	client := &dns.Client{
		Net:       "tcp-tls",
		TLSConfig: u.tlsConfig,
		Timeout:   2 * time.Second,
	}
	resp, _, err := client.ExchangeContext(ctx, m, u.addr)
	return resp, err
}

type dnsCacheEntry struct {
	msg    *dns.Msg
	expiry time.Time
}

// FakeIPPool 管理 Fake IP 的分配与映射
type FakeIPPool struct {
	mu         sync.Mutex
	network    *net.IPNet
	minIP      uint32
	maxIP      uint32
	current    uint32
	ipToDomain map[uint32]string
	domainToIP map[string]uint32
}

func NewFakeIPPool(cidr string) (*FakeIPPool, error) {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, err
	}
	ones, _ := ipNet.Mask.Size()
	minIP := binary.BigEndian.Uint32(ipNet.IP.To4())
	maxIP := minIP | (uint32(0xFFFFFFFF) >> uint32(ones))

	return &FakeIPPool{
		network:    ipNet,
		minIP:      minIP + 1, // 跳过网络号
		maxIP:      maxIP - 1, // 跳过广播地址
		current:    minIP + 1,
		ipToDomain: make(map[uint32]string),
		domainToIP: make(map[string]uint32),
	}, nil
}

func (p *FakeIPPool) Get(domain string) net.IP {
	p.mu.Lock()
	defer p.mu.Unlock()

	if ip, ok := p.domainToIP[domain]; ok {
		return uint32ToIP(ip)
	}

	ip := p.current
	p.ipToDomain[ip] = domain
	p.domainToIP[domain] = ip

	p.current++
	if p.current > p.maxIP {
		p.current = p.minIP
	}

	return uint32ToIP(ip)
}

func (p *FakeIPPool) Lookup(ip net.IP) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	domain, ok := p.ipToDomain[binary.BigEndian.Uint32(ip.To4())]
	return domain, ok
}

func uint32ToIP(n uint32) net.IP {
	ip := make(net.IP, 4)
	binary.BigEndian.PutUint32(ip, n)
	return ip
}

// SmartResolver 智能 DNS 解析器
type SmartResolver struct {
	router    common.Router
	mu        sync.RWMutex
	cache     map[string]*dnsCacheEntry
	upstreams map[string][]DNSUpstream // group -> upstreams
	logger    *zap.Logger

	fakeIPEnabled bool
	fakeIPPool    *FakeIPPool
}

func NewSmartResolver(router common.Router, logger *zap.Logger) *SmartResolver {
	return &SmartResolver{
		router:    router,
		cache:     make(map[string]*dnsCacheEntry),
		upstreams: make(map[string][]DNSUpstream),
		logger:    logger,
	}
}

func (sr *SmartResolver) EnableFakeIP(cidr string) error {
	pool, err := NewFakeIPPool(cidr)
	if err != nil {
		return err
	}
	sr.fakeIPEnabled = true
	sr.fakeIPPool = pool
	return nil
}

// AddUpstreamByString 解析字符串并添加上游
func (sr *SmartResolver) AddUpstreamByString(group string, addr string) error {
	var u DNSUpstream
	if strings.HasPrefix(addr, "https://") {
		u = &DoHUpstream{url: addr, client: &http.Client{Timeout: 5 * time.Second}}
	} else if strings.HasPrefix(addr, "tls://") {
		target := strings.TrimPrefix(addr, "tls://")
		serverName := target
		if host, _, err := net.SplitHostPort(target); err == nil {
			serverName = host
		}
		u = &DoTUpstream{
			addr:      target,
			tlsConfig: &tls.Config{ServerName: serverName},
		}
	} else {
		// 默认为 UDP
		if !strings.Contains(addr, ":") { addr += ":53" }
		u = &UDPUpstream{addr: addr}
	}

	sr.mu.Lock()
	sr.upstreams[group] = append(sr.upstreams[group], u)
	sr.mu.Unlock()
	return nil
}

func (sr *SmartResolver) LookupIP(ctx context.Context, host string) ([]net.IP, error) {
	if net.ParseIP(host) != nil { return []net.IP{net.ParseIP(host)}, nil }
	
	// 如果开启了 Fake IP，直接返回 Fake IP
	if sr.fakeIPEnabled {
		return []net.IP{sr.fakeIPPool.Get(host)}, nil
	}

	fqdn := host
	if !strings.HasSuffix(fqdn, ".") { fqdn += "." }
	
	m := new(dns.Msg)
	m.SetQuestion(fqdn, dns.TypeA)
	
	resp, err := sr.Query(ctx, m)
	if err != nil { return nil, err }
	
	var ips []net.IP
	for _, ans := range resp.Answer {
		if a, ok := ans.(*dns.A); ok {
			ips = append(ips, a.A)
		} else if aaaa, ok := ans.(*dns.AAAA); ok {
			ips = append(ips, aaaa.AAAA)
		}
	}
	return ips, nil
}

func (sr *SmartResolver) ResolveFakeIP(ip net.IP) (string, bool) {
	if !sr.fakeIPEnabled {
		return "", false
	}
	return sr.fakeIPPool.Lookup(ip)
}

func (sr *SmartResolver) Query(ctx context.Context, m *dns.Msg) (*dns.Msg, error) {
	if len(m.Question) == 0 { return nil, fmt.Errorf("no question") }
	q := m.Question[0]
	cacheKey := fmt.Sprintf("%s-%d", q.Name, q.Qtype)

	// 如果开启了 Fake IP 且是 A 记录查询，返回 Fake IP 响应
	if sr.fakeIPEnabled && q.Qtype == dns.TypeA {
		domain := strings.TrimSuffix(q.Name, ".")
		fakeIP := sr.fakeIPPool.Get(domain)
		
		resp := new(dns.Msg)
		resp.SetReply(m)
		resp.Answer = append(resp.Answer, &dns.A{
			Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
			A:   fakeIP,
		})
		return resp, nil
	}

	// 1. 查询缓存
	sr.mu.RLock()
	if entry, ok := sr.cache[cacheKey]; ok && time.Now().Before(entry.expiry) {
		sr.mu.RUnlock()
		resp := entry.msg.Copy()
		resp.Id = m.Id
		return resp, nil
	}
	sr.mu.RUnlock()

	// 2. 路由选择上游组
	group := "local"
	meta := common.SessionMeta{Target: net.JoinHostPort(strings.TrimSuffix(q.Name, "."), "443")}
	if out, err := sr.router.Route(meta); err == nil && out.Name() != "direct" {
		group = "remote"
	}

	sr.mu.RLock()
	ups := sr.upstreams[group]
	if len(ups) == 0 { ups = sr.upstreams["local"] }
	sr.mu.RUnlock()

	if len(ups) == 0 {
		ups = []DNSUpstream{&UDPUpstream{addr: "119.29.29.29:53"}, &UDPUpstream{addr: "223.5.5.5:53"}}
	}

	// 3. 并行 Race 查询
	resp, err := sr.raceQuery(ctx, ups, m)
	if err != nil { return nil, err }

	// 4. 写入缓存
	ttl := uint32(600)
	if len(resp.Answer) > 0 {
		ttl = resp.Answer[0].Header().Ttl
	}
	if ttl < 10 { ttl = 10 }
	if ttl > 3600 { ttl = 3600 }

	sr.mu.Lock()
	sr.cache[cacheKey] = &dnsCacheEntry{
		msg:    resp.Copy(),
		expiry: time.Now().Add(time.Duration(ttl) * time.Second),
	}
	sr.mu.Unlock()

	return resp, nil
}

func (sr *SmartResolver) Flush() {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	sr.cache = make(map[string]*dnsCacheEntry)
}

func (sr *SmartResolver) raceQuery(ctx context.Context, upstreams []DNSUpstream, m *dns.Msg) (*dns.Msg, error) {
	if len(upstreams) == 1 {
		return upstreams[0].Exchange(ctx, m)
	}

	resCh := make(chan *dns.Msg, len(upstreams))
	errCh := make(chan error, len(upstreams))
	
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	for _, u := range upstreams {
		go func(u DNSUpstream) {
			resp, err := u.Exchange(ctx, m)
			if err != nil {
				errCh <- err
				return
			}
			resCh <- resp
		}(u)
	}

	var lastErr error
	for i := 0; i < len(upstreams); i++ {
		select {
		case res := <-resCh:
			return res, nil
		case err := <-errCh:
			lastErr = err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, lastErr
}
