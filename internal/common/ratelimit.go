package common

import (
	"context"
	"sync"

	"golang.org/x/time/rate"
)

// BandwidthLimiter 包装了令牌桶限速器
type BandwidthLimiter struct {
	limiter *rate.Limiter
}

func NewBandwidthLimiter(bytesPerSec int) *BandwidthLimiter {
	if bytesPerSec <= 0 {
		return nil
	}
	return &BandwidthLimiter{
		limiter: rate.NewLimiter(rate.Limit(bytesPerSec), bytesPerSec/4+1024*1024),
	}
}

func (l *BandwidthLimiter) Wait(ctx context.Context, n int) error {
	if l == nil || l.limiter == nil {
		return nil
	}
	return l.limiter.WaitN(ctx, n)
}

// LimiterManager 管理多租户限速器
type LimiterManager struct {
	mu       sync.RWMutex
	limiters map[string]*BandwidthLimiter
	defaultRate int
}

func NewLimiterManager(defaultMBps int) *LimiterManager {
	return &LimiterManager{
		limiters:    make(map[string]*BandwidthLimiter),
		defaultRate: defaultMBps * 1024 * 1024,
	}
}

func (m *LimiterManager) GetOrCreate(userID string) *BandwidthLimiter {
	if userID == "" {
		return nil
	}
	m.mu.RLock()
	l, ok := m.limiters[userID]
	m.mu.RUnlock()
	if ok {
		return l
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	// 双检查
	if l, ok = m.limiters[userID]; ok {
		return l
	}
	l = NewBandwidthLimiter(m.defaultRate)
	m.limiters[userID] = l
	return l
}

func (m *LimiterManager) Update(userID string, mbps int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.limiters[userID] = NewBandwidthLimiter(mbps * 1024 * 1024)
}

// LimitReader 包装一个 io.Reader 以实现读取限速
type LimitReader struct {
	r       interface{ Read([]byte) (int, error) }
	limiter *BandwidthLimiter
	ctx     context.Context
}

func NewLimitReader(ctx context.Context, r interface{ Read([]byte) (int, error) }, l *BandwidthLimiter) *LimitReader {
	return &LimitReader{r: r, limiter: l, ctx: ctx}
}

func (lr *LimitReader) Read(p []byte) (int, error) {
	n, err := lr.r.Read(p)
	if n > 0 && lr.limiter != nil {
		if werr := lr.limiter.Wait(lr.ctx, n); werr != nil {
			return n, werr
		}
	}
	return n, err
}
