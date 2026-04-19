package limiter

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type Manager struct {
	mu          sync.Mutex
	usersMbps   map[int32]float64
	userLimiter map[int32]*rate.Limiter
	userApplied map[int32]float64
	nodeMbps    float64
}

func NewManager() *Manager {
	return &Manager{
		usersMbps:   make(map[int32]float64),
		userLimiter: make(map[int32]*rate.Limiter),
		userApplied: make(map[int32]float64),
	}
}

func (m *Manager) SetNodeLimit(mbps float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nodeMbps = mbps
}

func (m *Manager) SetUserLimit(userID int32, mbps float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if mbps < 0 {
		mbps = 0
	}
	m.usersMbps[userID] = mbps
	delete(m.userLimiter, userID)
	delete(m.userApplied, userID)
}

func (m *Manager) RemoveUser(userID int32) {
	m.mu.Lock()
	delete(m.usersMbps, userID)
	delete(m.userLimiter, userID)
	delete(m.userApplied, userID)
	m.mu.Unlock()
}

func (m *Manager) WaitN(ctx context.Context, userID int32, n int) error {
	if n <= 0 {
		return nil
	}
	m.mu.Lock()
	userMbps := m.usersMbps[userID]
	if userMbps < 0 {
		userMbps = 0
	}
	effective := minLimit(userMbps, m.nodeMbps)
	if effective <= 0 {
		m.mu.Unlock()
		return nil
	}
	ul := m.userLimiter[userID]
	if ul == nil || m.userApplied[userID] != effective {
		ul = newLimiterFromMbps(effective)
		m.userLimiter[userID] = ul
		m.userApplied[userID] = effective
	}
	m.mu.Unlock()
	if ul == nil {
		return nil
	}
	if err := ul.WaitN(ctx, n); err != nil {
		return fmt.Errorf("rate limit: %w", err)
	}
	return nil
}

func minLimit(userMbps, nodeMbps float64) float64 {
	if userMbps < 0 {
		userMbps = 0
	}
	if nodeMbps < 0 {
		nodeMbps = 0
	}
	if userMbps == 0 && nodeMbps == 0 {
		return 0
	}
	if userMbps == 0 {
		return nodeMbps
	}
	if nodeMbps == 0 {
		return userMbps
	}
	if userMbps < nodeMbps {
		return userMbps
	}
	return nodeMbps
}

func newLimiterFromMbps(mbps float64) *rate.Limiter {
	if mbps <= 0 {
		return nil
	}
	bps := mbps * 125000
	if bps <= 0 || math.IsNaN(bps) || math.IsInf(bps, 0) {
		return nil
	}
	burst := int(bps)
	if burst < 4096 {
		burst = 4096
	}
	if burst > 8*1024*1024 {
		burst = 8 * 1024 * 1024
	}
	return rate.NewLimiter(rate.Limit(bps), burst)
}

func ClampCtx(parent context.Context, n int) (context.Context, context.CancelFunc) {
	t := 5 * time.Second
	if n >= 1024*1024 {
		t = 15 * time.Second
	}
	return context.WithTimeout(parent, t)
}
