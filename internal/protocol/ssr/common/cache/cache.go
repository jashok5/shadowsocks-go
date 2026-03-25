package cache

import (
	"sync"
	"sync/atomic"
	"time"
)

type Cache struct {
	*cache
}

type cache struct {
	mapping      sync.Map
	cleanupEvery time.Duration
	nextCleanup  atomic.Int64
}

type element struct {
	Expired time.Time
	Payload any
}

func (c *cache) Put(key any, payload any, ttl time.Duration) {
	c.maybeCleanup(time.Now())
	c.mapping.Store(key, &element{
		Payload: payload,
		Expired: time.Now().Add(ttl),
	})
}

func (c *cache) Get(key any) any {
	c.maybeCleanup(time.Now())
	item, exist := c.mapping.Load(key)
	if !exist {
		return nil
	}
	elm := item.(*element)

	if time.Now().After(elm.Expired) {
		c.mapping.Delete(key)
		return nil
	}
	return elm.Payload
}

func (c *cache) Range(callback func(key, value any)) {
	c.maybeCleanup(time.Now())
	c.mapping.Range(func(k, v any) bool {
		elm := v.(*element)
		if time.Now().After(elm.Expired) {
			c.mapping.Delete(k)
		} else {
			callback(k, elm.Payload)
		}
		return true
	})
}

func (c *cache) cleanup() {
	now := time.Now()
	c.mapping.Range(func(k, v any) bool {
		elm := v.(*element)
		if now.After(elm.Expired) {
			c.mapping.Delete(k)
		}
		return true
	})
}

func (c *cache) Size() int {
	var result int
	c.Range(func(k, v any) {
		result++
	})
	return result
}

func New(interval time.Duration) *Cache {
	if interval <= 0 {
		interval = time.Second
	}
	c := &cache{cleanupEvery: interval}
	c.nextCleanup.Store(time.Now().Add(interval).UnixNano())
	return &Cache{c}
}

func (c *cache) maybeCleanup(now time.Time) {
	if c == nil {
		return
	}
	next := c.nextCleanup.Load()
	if next != 0 && now.UnixNano() < next {
		return
	}
	newNext := now.Add(c.cleanupEvery).UnixNano()
	if !c.nextCleanup.CompareAndSwap(next, newNext) {
		return
	}
	c.cleanup()
}
