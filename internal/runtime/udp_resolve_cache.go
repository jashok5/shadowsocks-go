package runtime

import (
	"errors"
	"net"
	"sync"
	"time"
)

func resolveUDPAddrFromCache(mu *sync.Mutex, cache *sessionCache, target string, resolver func(string) (*net.UDPAddr, error), invalidTypeErr string) (*net.UDPAddr, error) {
	mu.Lock()
	defer mu.Unlock()
	if cache == nil {
		return resolver(target)
	}
	v, err := cache.GetOrCreate(target, func() (any, error) {
		return resolver(target)
	})
	if err != nil {
		return nil, err
	}
	addr, ok := v.(*net.UDPAddr)
	if !ok || addr == nil {
		if invalidTypeErr == "" {
			invalidTypeErr = "invalid udp resolve cache item type"
		}
		return nil, errors.New(invalidTypeErr)
	}
	return addr, nil
}

func sweepSessionCache(mu *sync.Mutex, cache *sessionCache, now time.Time) {
	mu.Lock()
	defer mu.Unlock()
	if cache != nil {
		cache.SweepExpired(now)
	}
}

func closeSessionCache(mu *sync.Mutex, cache *sessionCache) {
	mu.Lock()
	defer mu.Unlock()
	if cache != nil {
		cache.CloseAll()
	}
}
