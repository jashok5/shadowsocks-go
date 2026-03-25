package cache

import (
	"sync"
	"sync/atomic"
	"time"
)

type LRU struct {
	*lrucache
}

type lrucache struct {
	mapping      sync.Map
	TTL          time.Duration
	cleanupEvery time.Duration
	nextCleanup  atomic.Int64
}

type lruelement struct {
	Expired time.Time
	Payload any
	TTL     time.Duration
}

func (l *lrucache) Put(key any, payload any) {
	l.maybeCleanup(time.Now())
	l.mapping.Store(key, &lruelement{
		Payload: payload,
		Expired: time.Now().Add(l.TTL),
		TTL:     l.TTL,
	})
}

func (l *lrucache) Get(key any) any {
	l.maybeCleanup(time.Now())
	item, exist := l.mapping.Load(key)
	if !exist {
		return nil
	}
	elm := item.(*lruelement)

	if time.Now().After(elm.Expired) {
		l.mapping.Delete(key)
		return nil
	}

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
	l.maybeCleanup(time.Now())
	var result any = nil
	l.mapping.Range(func(key, value any) bool {
		result = key
		return false
	})
	return result
}

func (l *lrucache) Len() int {
	l.maybeCleanup(time.Now())
	var result = 0
	l.mapping.Range(func(key, valye any) bool {
		result++
		return true
	})
	return result
}

func (l *lrucache) Clean() {
	now := time.Now()
	l.mapping.Range(func(k, v any) bool {
		elm := v.(*lruelement)
		if now.After(elm.Expired) {
			l.mapping.Delete(k)
		}
		return true
	})
}

func NewLruCache(interval time.Duration) *LRU {
	if interval <= 0 {
		interval = time.Second
	}
	c := &lrucache{
		TTL:          interval,
		cleanupEvery: interval,
	}
	c.nextCleanup.Store(time.Now().Add(interval).UnixNano())
	lru := &LRU{c}
	return lru
}

func (l *lrucache) maybeCleanup(now time.Time) {
	if l == nil {
		return
	}
	next := l.nextCleanup.Load()
	if next != 0 && now.UnixNano() < next {
		return
	}
	newNext := now.Add(l.cleanupEvery).UnixNano()
	if !l.nextCleanup.CompareAndSwap(next, newNext) {
		return
	}
	l.Clean()
}
