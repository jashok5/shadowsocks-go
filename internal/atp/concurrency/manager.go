package concurrency

import "sync"

type Manager struct {
	mu     sync.Mutex
	active map[int32]int
	limits map[int32]int
}

func NewManager() *Manager {
	return &Manager{active: make(map[int32]int), limits: make(map[int32]int)}
}

func (m *Manager) SetLimit(userID int32, limit int) {
	m.mu.Lock()
	if limit <= 0 {
		delete(m.limits, userID)
	} else {
		m.limits[userID] = limit
	}
	m.mu.Unlock()
}

func (m *Manager) Enter(userID int32) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	limit, ok := m.limits[userID]
	if ok && limit > 0 && m.active[userID] >= limit {
		return false
	}
	m.active[userID]++
	return true
}

func (m *Manager) Leave(userID int32) {
	m.mu.Lock()
	if cur := m.active[userID]; cur <= 1 {
		delete(m.active, userID)
	} else {
		m.active[userID] = cur - 1
	}
	m.mu.Unlock()
}
