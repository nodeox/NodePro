package inbound

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nodeox/NodePro/internal/common"
	"github.com/nodeox/NodePro/internal/protocol/socks5"
	"github.com/nodeox/NodePro/internal/transport"
)

// udpSession 维护入站 UDP 关联的状态
type udpSession struct {
	mu           sync.Mutex
	outbound     common.OutboundHandler
	outboundConn net.Conn
	sessionID    string
	lastSeen     time.Time
	
	// 用于回传的通道信息
	clientAddr   net.Addr
	inboundConn  *net.UDPConn
	isSocks5     bool
}

// UDPSessionManager 提供通用的 UDP 会话管理和清理逻辑
type UDPSessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*udpSession
	timeout  time.Duration
}

func NewUDPSessionManager(timeout time.Duration) *UDPSessionManager {
	if timeout == 0 { timeout = 2 * time.Minute }
	return &UDPSessionManager{
		sessions: make(map[string]*udpSession),
		timeout:  timeout,
	}
}

func (m *UDPSessionManager) GetOrCreate(key string, target string, raddr net.Addr, uconn *net.UDPConn, isSocks5 bool, router common.Router) (*udpSession, bool, error) {
	m.mu.RLock()
	s, ok := m.sessions[key]
	m.mu.RUnlock()

	if ok {
		s.lastSeen = time.Now()
		return s, false, nil
	}

	sessionID := uuid.New().String()
	meta := common.SessionMeta{ID: sessionID, Target: target, HopChain: []string{target}, Network: "udp"}
	out, err := router.Route(meta)
	if err != nil { return nil, false, err }

	s = &udpSession{
		outbound:    out,
		sessionID:   sessionID,
		lastSeen:    time.Now(),
		clientAddr:  raddr,
		inboundConn: uconn,
		isSocks5:    isSocks5,
	}
	
	m.mu.Lock()
	m.sessions[key] = s
	m.mu.Unlock()
	return s, true, nil
}

func (m *UDPSessionManager) CleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.mu.Lock()
			for k, s := range m.sessions {
				if time.Since(s.lastSeen) > m.timeout {
					s.mu.Lock()
					if s.outboundConn != nil { s.outboundConn.Close() }
					s.mu.Unlock()
					delete(m.sessions, k)
				}
			}
			m.mu.Unlock()
		case <-ctx.Done(): return
		}
	}
}

// ForwardPacket 处理 UDP 转发，包括出站拨号缓存和反向流量回传
func (m *UDPSessionManager) ForwardPacket(ctx context.Context, s *udpSession, data []byte, target string, userID string, limiter *common.BandwidthLimiter, quota *common.QuotaManager) {
	if quota != nil && userID != "" {
		if exceeded := quota.AddUsage(userID, int64(len(data))); exceeded { return }
	}
	if limiter != nil { limiter.Wait(ctx, len(data)) }

	s.mu.Lock()
	if s.outboundConn == nil {
		meta := common.SessionMeta{ID: s.sessionID, Target: target, Network: "udp", UserID: userID}
		conn, err := s.outbound.Dial(ctx, meta)
		if err != nil {
			s.mu.Unlock()
			return
		}
		s.outboundConn = conn
		go m.handleReverseFlow(s, target)
	}
	conn := s.outboundConn
	s.mu.Unlock()

	if qc, ok := conn.(*transport.StreamConn); ok {
		qc.SendUDP(data)
	} else {
		conn.Write(data)
	}
	
	common.BytesTransferred.WithLabelValues("udp", "ingress").Add(float64(len(data)))
	common.AddBytesIn(int64(len(data)))
}

func (m *UDPSessionManager) handleReverseFlow(s *udpSession, originalTarget string) {
	buf := common.GetBuf()
	defer common.PutBuf(buf)
	
	for {
		s.mu.Lock()
		conn := s.outboundConn
		s.mu.Unlock()
		if conn == nil { return }

		conn.SetReadDeadline(time.Now().Add(m.timeout))
		n, err := conn.Read(buf)
		if err != nil { return }
		
		var backData []byte
		if s.isSocks5 {
			backData, _ = socks5.PackUDPPacket(originalTarget, buf[:n])
		} else {
			backData = buf[:n]
		}
		
		s.inboundConn.WriteTo(backData, s.clientAddr)
		common.BytesTransferred.WithLabelValues("udp", "egress").Add(float64(len(backData)))
		common.AddBytesOut(int64(len(backData)))
		s.lastSeen = time.Now()
	}
}
