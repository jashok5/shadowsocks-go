package reporter

import (
	"strconv"
	"sync"
	"time"
)

type Traffic struct {
	U int64
	D int64
}

type Collector struct {
	mu      sync.Mutex
	traffic map[int32]Traffic
	aliveIP map[int32]map[string]struct{}
	detect  []DetectRecord
	dedupe  map[string]time.Time
}

type DetectRecord struct {
	UserID int32
	ListID int32
}

func NewCollector() *Collector {
	return &Collector{
		traffic: make(map[int32]Traffic),
		aliveIP: make(map[int32]map[string]struct{}),
		detect:  make([]DetectRecord, 0, 32),
		dedupe:  make(map[string]time.Time),
	}
}

func (c *Collector) AddUpload(userID int32, n int) {
	if userID <= 0 || n <= 0 {
		return
	}
	c.mu.Lock()
	t := c.traffic[userID]
	t.U += int64(n)
	c.traffic[userID] = t
	c.mu.Unlock()
}

func (c *Collector) AddDownload(userID int32, n int) {
	if userID <= 0 || n <= 0 {
		return
	}
	c.mu.Lock()
	t := c.traffic[userID]
	t.D += int64(n)
	c.traffic[userID] = t
	c.mu.Unlock()
}

func (c *Collector) AddAliveIP(userID int32, ip string) {
	if userID <= 0 || ip == "" {
		return
	}
	c.mu.Lock()
	m, ok := c.aliveIP[userID]
	if !ok {
		m = make(map[string]struct{})
		c.aliveIP[userID] = m
	}
	m[ip] = struct{}{}
	c.mu.Unlock()
}

func (c *Collector) FlushTraffic() map[int32]Traffic {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.traffic
	c.traffic = make(map[int32]Traffic)
	return out
}

func (c *Collector) FlushAliveIP() map[int32][]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[int32][]string, len(c.aliveIP))
	for uid, ips := range c.aliveIP {
		arr := make([]string, 0, len(ips))
		for ip := range ips {
			arr = append(arr, ip)
		}
		out[uid] = arr
	}
	c.aliveIP = make(map[int32]map[string]struct{})
	return out
}

func (c *Collector) AddDetectLog(userID, listID int32) {
	if userID <= 0 || listID <= 0 {
		return
	}
	now := time.Now()
	key := strconv.FormatInt(int64(userID), 10) + ":" + strconv.FormatInt(int64(listID), 10)
	c.mu.Lock()
	if exp, ok := c.dedupe[key]; ok && exp.After(now) {
		c.mu.Unlock()
		return
	}
	c.dedupe[key] = now.Add(30 * time.Second)
	c.detect = append(c.detect, DetectRecord{UserID: userID, ListID: listID})
	for k, exp := range c.dedupe {
		if exp.Before(now) {
			delete(c.dedupe, k)
		}
	}
	c.mu.Unlock()
}

func (c *Collector) FlushDetectLog() []DetectRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.detect
	c.detect = make([]DetectRecord, 0, 32)
	return out
}
