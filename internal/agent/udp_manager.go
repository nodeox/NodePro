package agent

import (
	"net"
	"sync"
)

// UDPPacketConn 定义了能发送 UDP 包的接口
type UDPPacketConn interface {
	WriteTo(p []byte, addr net.Addr) (n int, err error)
}

// UDPSession 记录一个 UDP 转发会话的上下文
type UDPSession struct {
	Conn       UDPPacketConn // 本地监听的连接
	ClientAddr net.Addr      // 原始客户端地址
}

type UDPSessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*UDPSession
}

func NewUDPSessionManager() *UDPSessionManager {
	return &UDPSessionManager{sessions: make(map[string]*UDPSession)}
}

func (m *UDPSessionManager) Add(sessionID string, session *UDPSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[sessionID] = session
}

func (m *UDPSessionManager) Get(sessionID string) *UDPSession {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[sessionID]
}

func (m *UDPSessionManager) Remove(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, sessionID)
}
