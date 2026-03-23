package runtime

import (
	"time"
)

type sessionCacheEntry struct {
	value      any
	lastAccess time.Time
}

type evictedEntry struct {
	key   string
	value any
}

type sessionCache struct {
	entries    map[string]sessionCacheEntry
	maxEntries int
	ttl        time.Duration
	onEvict    func(string, any)
	hits       int64
	misses     int64
	creates    int64
	evicted    int64
	expired    int64
	sweeps     int64
}

type SessionCacheSnapshot struct {
	Size    int
	Hits    int64
	Misses  int64
	Creates int64
	Evicted int64
	Expired int64
	Sweeps  int64
}

func newSessionCache(maxEntries int, ttl time.Duration, onEvict func(string, any)) *sessionCache {
	return &sessionCache{
		entries:    make(map[string]sessionCacheEntry),
		maxEntries: maxEntries,
		ttl:        ttl,
		onEvict:    onEvict,
	}
}

func (c *sessionCache) GetOrCreate(key string, creator func() (any, error)) (any, error) {
	now := time.Now()
	if ent, ok := c.entries[key]; ok && !c.isExpired(ent, now) {
		c.hits++
		ent.lastAccess = now
		c.entries[key] = ent
		return ent.value, nil
	}
	c.misses++

	created, err := creator()
	if err != nil {
		return nil, err
	}

	now = time.Now()
	evicted := make([]evictedEntry, 0, 2)
	if ent, ok := c.entries[key]; ok && !c.isExpired(ent, now) {
		ent.lastAccess = now
		c.entries[key] = ent
		c.evicted++
		evicted = append(evicted, evictedEntry{key: key, value: created})
		c.runEvictCallbacks(evicted)
		return ent.value, nil
	}

	if c.ttl > 0 {
		evicted = append(evicted, c.collectExpired(now)...)
	}
	if c.maxEntries > 0 {
		for len(c.entries) >= c.maxEntries {
			if k, v, ok := c.evictOldest(); ok {
				c.evicted++
				evicted = append(evicted, evictedEntry{key: k, value: v})
			} else {
				break
			}
		}
	}
	c.entries[key] = sessionCacheEntry{value: created, lastAccess: now}
	c.creates++
	c.runEvictCallbacks(evicted)
	return created, nil
}

func (c *sessionCache) SweepExpired(now time.Time) {
	if c.ttl <= 0 {
		return
	}
	c.sweeps++
	c.runEvictCallbacks(c.collectExpired(now))
}

func (c *sessionCache) CloseAll() {
	evicted := make([]evictedEntry, 0, len(c.entries))
	for key, ent := range c.entries {
		c.evicted++
		evicted = append(evicted, evictedEntry{key: key, value: ent.value})
	}
	c.entries = make(map[string]sessionCacheEntry)
	c.runEvictCallbacks(evicted)
}

func (c *sessionCache) collectExpired(now time.Time) []evictedEntry {
	out := make([]evictedEntry, 0)
	for key, ent := range c.entries {
		if !c.isExpired(ent, now) {
			continue
		}
		c.expired++
		c.evicted++
		out = append(out, evictedEntry{key: key, value: ent.value})
		delete(c.entries, key)
	}
	return out
}

func (c *sessionCache) evictOldest() (string, any, bool) {
	if len(c.entries) == 0 {
		return "", nil, false
	}
	var (
		oldestKey string
		oldest    sessionCacheEntry
		found     bool
	)
	for key, ent := range c.entries {
		if !found || ent.lastAccess.Before(oldest.lastAccess) {
			oldestKey = key
			oldest = ent
			found = true
		}
	}
	if !found {
		return "", nil, false
	}
	delete(c.entries, oldestKey)
	return oldestKey, oldest.value, true
}

func (c *sessionCache) isExpired(ent sessionCacheEntry, now time.Time) bool {
	if c.ttl <= 0 {
		return false
	}
	return now.Sub(ent.lastAccess) >= c.ttl
}

func (c *sessionCache) runEvictCallbacks(evicted []evictedEntry) {
	if len(evicted) == 0 || c.onEvict == nil {
		return
	}
	for _, it := range evicted {
		c.onEvict(it.key, it.value)
	}
}

func (c *sessionCache) Snapshot() SessionCacheSnapshot {
	if c == nil {
		return SessionCacheSnapshot{}
	}
	return SessionCacheSnapshot{
		Size:    len(c.entries),
		Hits:    c.hits,
		Misses:  c.misses,
		Creates: c.creates,
		Evicted: c.evicted,
		Expired: c.expired,
		Sweeps:  c.sweeps,
	}
}
