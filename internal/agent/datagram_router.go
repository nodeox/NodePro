package agent

import (
	"sync"

	"github.com/quic-go/quic-go"
)

// DatagramRouter 维护 SessionID 到物理连接的映射，用于中继转发 UDP
type DatagramRouter struct {
	mu    sync.RWMutex
	table map[string]*quic.Conn // SessionID -> 下一跳连接
}

func NewDatagramRouter() *DatagramRouter {
	return &DatagramRouter{
		table: make(map[string]*quic.Conn),
	}
}

// Register 关联会话与连接
func (r *DatagramRouter) Register(sessionID string, conn *quic.Conn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.table[sessionID] = conn
}

// Get 获取关联连接
func (r *DatagramRouter) Get(sessionID string) *quic.Conn {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.table[sessionID]
}

// Unregister 移除关联
func (r *DatagramRouter) Unregister(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.table, sessionID)
}
