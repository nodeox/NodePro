package common

import (
	"sync"
	"sync/atomic"
)

// QuotaManager 增加重置功能
type QuotaManager struct {
	mu     sync.RWMutex
	used   sync.Map         
	limits map[string]int64 
}

func NewQuotaManager() *QuotaManager {
	return &QuotaManager{limits: make(map[string]int64)}
}

func (m *QuotaManager) AddUsage(userID string, n int64) bool {
	actual, _ := m.used.LoadOrStore(userID, &atomic.Int64{})
	counter := actual.(*atomic.Int64)
	m.mu.RLock()
	limit := m.limits[userID]
	m.mu.RUnlock()
	current := counter.Add(n)
	return limit > 0 && current > limit
}

func (m *QuotaManager) SetLimit(userID string, limit int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.limits[userID] = limit
}

// Reset 允许重置特定用户的配额
func (m *QuotaManager) Reset(userID string) {
	if actual, ok := m.used.Load(userID); ok {
		actual.(*atomic.Int64).Store(0)
	}
}

func (m *QuotaManager) GetStatus(userID string) (used int64, limit int64) {
	if actual, ok := m.used.Load(userID); ok {
		used = actual.(*atomic.Int64).Load()
	}
	m.mu.RLock()
	limit = m.limits[userID]
	m.mu.RUnlock()
	return
}
