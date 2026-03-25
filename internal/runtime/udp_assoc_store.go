package runtime

import (
	"sync"
	"time"
)

type udpAssocStore struct {
	mu    sync.Mutex
	cache *sessionCache
}

func newUDPAssocStore(maxEntries int, ttl time.Duration, onEvict func(string, any)) *udpAssocStore {
	return &udpAssocStore{cache: newSessionCache(maxEntries, ttl, onEvict)}
}

func (s *udpAssocStore) GetOrCreate(key string, creator func() (any, error)) (any, error) {
	if s == nil || s.cache == nil {
		return creator()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cache.GetOrCreate(key, creator)
}

func (s *udpAssocStore) GetOrCreateWithStatus(key string, creator func() (any, error)) (any, bool, error) {
	if s == nil || s.cache == nil {
		v, err := creator()
		return v, err == nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	before := s.cache.creates
	v, err := s.cache.GetOrCreate(key, creator)
	if err != nil {
		return nil, false, err
	}
	return v, s.cache.creates > before, nil
}

func (s *udpAssocStore) Sweep(now time.Time) {
	if s == nil || s.cache == nil {
		return
	}
	s.mu.Lock()
	s.cache.SweepExpired(now)
	s.mu.Unlock()
}

func (s *udpAssocStore) CloseAll() {
	if s == nil || s.cache == nil {
		return
	}
	s.mu.Lock()
	s.cache.CloseAll()
	s.mu.Unlock()
}

func (s *udpAssocStore) Snapshot() SessionCacheSnapshot {
	if s == nil || s.cache == nil {
		return SessionCacheSnapshot{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cache.Snapshot()
}
