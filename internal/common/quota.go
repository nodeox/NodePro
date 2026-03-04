package common

import (
	"sync"
	"sync/atomic"
)

// QuotaManager 修复并发写导致的统计丢失
type QuotaManager struct {
	mu     sync.RWMutex
	used   sync.Map         // userID -> *atomic.Int64
	limits map[string]int64 // userID -> max bytes
}

func NewQuotaManager() *QuotaManager {
	return &QuotaManager{
		limits: make(map[string]int64),
	}
}

func (m *QuotaManager) AddUsage(userID string, n int64) bool {
	actual, _ := m.used.LoadOrStore(userID, &atomic.Int64{})
	counter := actual.(*atomic.Int64)

	m.mu.RLock()
	limit := m.limits[userID]
	m.mu.RUnlock()

	current := counter.Add(n)
	if limit > 0 && current > limit {
		return true 
	}
	return false
}

func (m *QuotaManager) SetLimit(userID string, limit int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.limits[userID] = limit
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
