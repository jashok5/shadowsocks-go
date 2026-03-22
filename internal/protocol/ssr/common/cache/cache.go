package cache

import (
	"runtime"
	"sync"
	"time"
)

type Cache struct {
	*cache
}

type cache struct {
	mapping sync.Map
	janitor *janitor
}

type element struct {
	Expired time.Time
	Payload any
}

func (c *cache) Put(key any, payload any, ttl time.Duration) {
	c.mapping.Store(key, &element{
		Payload: payload,
		Expired: time.Now().Add(ttl),
	})
}

func (c *cache) Get(key any) any {
	item, exist := c.mapping.Load(key)
	if !exist {
		return nil
	}
	elm := item.(*element)
	// expired
	if time.Since(elm.Expired) > 0 {
		c.mapping.Delete(key)
		return nil
	}
	return elm.Payload
}

func (c *cache) Range(callback func(key, value any)) {
	c.mapping.Range(func(k, v any) bool {
		elm := v.(*element)
		if time.Since(elm.Expired) > 0 {
			c.mapping.Delete(k)
		} else {
			callback(k, elm.Payload)
		}
		return true
	})
}

func (c *cache) cleanup() {
	c.mapping.Range(func(k, v any) bool {
		elm := v.(*element)
		if time.Since(elm.Expired) > 0 {
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

type janitor struct {
	interval time.Duration
	stop     chan struct{}
	stopOnce sync.Once
}

func (j *janitor) Stop() {
	j.stopOnce.Do(func() {
		close(j.stop)
	})
}

func (j *janitor) process(c *cache) {
	ticker := time.NewTicker(j.interval)
	for {
		select {
		case <-ticker.C:
			c.cleanup()
		case <-j.stop:
			ticker.Stop()
			return
		}
	}
}

func New(interval time.Duration) *Cache {
	j := &janitor{
		interval: interval,
		stop:     make(chan struct{}),
	}
	c := &cache{janitor: j}
	go j.process(c)
	C := &Cache{c}

	runtime.AddCleanup(C, func(j *janitor) {
		j.Stop()
	}, j)
	return C
}
