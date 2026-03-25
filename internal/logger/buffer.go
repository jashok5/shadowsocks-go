package logger

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type LogEntry struct {
	ID        int64  `json:"id"`
	Timestamp int64  `json:"timestamp"`
	Text      string `json:"text"`
}

type RingBuffer struct {
	mu      sync.RWMutex
	entries []LogEntry
	cap     int
	head    int
	count   int
	nextID  atomic.Int64
	partial string
}

var globalBuffer *RingBuffer

func Buffer() *RingBuffer {
	return globalBuffer
}

func newRingBuffer(capacity int) *RingBuffer {
	if capacity <= 0 {
		capacity = 2000
	}
	b := &RingBuffer{
		entries: make([]LogEntry, capacity),
		cap:     capacity,
	}
	return b
}

func (b *RingBuffer) Write(p []byte) (int, error) {
	if b == nil || len(p) == 0 {
		return len(p), nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	chunk := b.partial + string(p)
	parts := strings.Split(chunk, "\n")
	if len(parts) == 0 {
		return len(p), nil
	}
	b.partial = parts[len(parts)-1]
	for i := 0; i < len(parts)-1; i++ {
		line := strings.TrimRight(parts[i], "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		b.appendLocked(LogEntry{
			ID:        b.nextID.Add(1),
			Timestamp: time.Now().UnixMilli(),
			Text:      line,
		})
	}
	return len(p), nil
}

func (b *RingBuffer) appendLocked(e LogEntry) {
	if b.count < b.cap {
		idx := (b.head + b.count) % b.cap
		b.entries[idx] = e
		b.count++
		return
	}
	b.entries[b.head] = e
	b.head = (b.head + 1) % b.cap
}

func (b *RingBuffer) Tail(limit int) []LogEntry {
	if b == nil {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if limit <= 0 || limit > b.count {
		limit = b.count
	}
	start := b.count - limit
	out := make([]LogEntry, 0, limit)
	for i := start; i < b.count; i++ {
		idx := (b.head + i) % b.cap
		out = append(out, b.entries[idx])
	}
	return out
}

func (b *RingBuffer) Since(afterID int64, limit int) []LogEntry {
	if b == nil {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if limit <= 0 {
		limit = 200
	}
	out := make([]LogEntry, 0, min(limit, b.count))
	for i := 0; i < b.count; i++ {
		idx := (b.head + i) % b.cap
		entry := b.entries[idx]
		if entry.ID <= afterID {
			continue
		}
		out = append(out, entry)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (b *RingBuffer) LatestID() int64 {
	if b == nil {
		return 0
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.count == 0 {
		return 0
	}
	idx := (b.head + b.count - 1) % b.cap
	return b.entries[idx].ID
}
