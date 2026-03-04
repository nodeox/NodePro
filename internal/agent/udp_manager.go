package agent

import (
	"net"
	"sync"
)

// UDPSessionManager 管理本地 UDP 会话映射
type UDPSessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*net.UDPConn // SessionID -> Local UDP Conn
}

func NewUDPSessionManager() *UDPSessionManager {
	return &UDPSessionManager{
		sessions: make(map[string]*net.UDPConn),
	}
}

func (m *UDPSessionManager) Add(sessionID string, conn *net.UDPConn) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[sessionID] = conn
}

func (m *UDPSessionManager) Get(sessionID string) *net.UDPConn {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[sessionID]
}

func (m *UDPSessionManager) Remove(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, sessionID)
}
