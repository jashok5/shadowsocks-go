package cache

import (
	"runtime"
	"sync"
	"time"
)

type LRU struct {
	*lrucache
}

type lrucache struct {
	mapping    sync.Map
	lruJanitor *lruJanitor
	TTL        time.Duration
}

type lruelement struct {
	Expired time.Time
	Payload any
	TTL     time.Duration
}

func (l *lrucache) Put(key any, payload any) {
	l.mapping.Store(key, &lruelement{
		Payload: payload,
		Expired: time.Now().Add(l.TTL),
		TTL:     l.TTL,
	})
}

func (l *lrucache) Get(key any) any {
	item, exist := l.mapping.Load(key)
	if !exist {
		return nil
	}
	elm := item.(*lruelement)
	// expired
	if time.Since(elm.Expired) > 0 {
		l.mapping.Delete(key)
		return nil
	}
	// lru strategy
	elm.Expired = time.Now().Add(elm.TTL)
	l.mapping.Store(key, elm)
	return elm.Payload
}

func (l *lrucache) Delete(key any) {
	l.mapping.Delete(key)
}

func (l *lrucache) IsExist(key any) bool {
	_, exist := l.mapping.Load(key)
	return exist
}

func (l *lrucache) First() any {
	var result any = nil
	l.mapping.Range(func(key, value any) bool {
		result = key
		return false
	})
	return result
}

func (l *lrucache) Len() int {
	var result = 0
	l.mapping.Range(func(key, valye any) bool {
		result++
		return true
	})
	return result
}

func (l *lrucache) Clean() {
	l.mapping.Range(func(k, v any) bool {
		// key := k.(string)
		elm := v.(*lruelement)
		if time.Since(elm.Expired) > 0 {
			l.mapping.Delete(k)
		}
		return true
	})
}

type lruJanitor struct {
	interval time.Duration
	stop     chan struct{}
	stopOnce sync.Once
}

func (j *lruJanitor) process(c *lrucache) {
	ticker := time.NewTicker(j.interval)
	for {
		select {
		case <-ticker.C:
			c.Clean()
		case <-j.stop:
			ticker.Stop()
			return
		}
	}
}

func (j *lruJanitor) Stop() {
	j.stopOnce.Do(func() {
		close(j.stop)
	})
}

func NewLruCache(interval time.Duration) *LRU {
	j := &lruJanitor{
		interval: interval,
		stop:     make(chan struct{}),
	}
	c := &lrucache{
		lruJanitor: j,
		TTL:        interval,
	}
	go j.process(c)
	lru := &LRU{c}
	runtime.AddCleanup(lru, func(j *lruJanitor) {
		j.Stop()
	}, j)
	return lru
}
