package reporter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type TrafficBatch struct {
	Rows      []TrafficRow `json:"rows"`
	Attempts  int          `json:"attempts"`
	CreatedAt time.Time    `json:"created_at"`
	NextTryAt time.Time    `json:"next_try_at"`
	LastError string       `json:"last_error,omitempty"`
}

type TrafficRow struct {
	UserID int32 `json:"user_id"`
	U      int   `json:"u"`
	D      int   `json:"d"`
}

type RetryQueue struct {
	mu       sync.Mutex
	items    []TrafficBatch
	maxLen   int
	filePath string
}

func NewRetryQueue(maxLen int, filePath string) (*RetryQueue, error) {
	if maxLen <= 0 {
		maxLen = 128
	}
	if filePath == "" {
		filePath = filepath.Join(os.TempDir(), "atp-traffic-retry.json")
	}
	q := &RetryQueue{maxLen: maxLen, filePath: filePath, items: make([]TrafficBatch, 0, maxLen)}
	if err := q.load(); err != nil {
		return nil, err
	}
	return q, nil
}

func (q *RetryQueue) EnqueueRows(rows []TrafficRow) {
	if len(rows) == 0 {
		return
	}
	q.mu.Lock()
	q.pushLocked(TrafficBatch{Rows: rows, Attempts: 0, CreatedAt: time.Now(), NextTryAt: time.Now()})
	_ = q.saveLocked()
	q.mu.Unlock()
}

func (q *RetryQueue) DequeueDue(now time.Time) (TrafficBatch, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return TrafficBatch{}, false
	}
	minIdx := -1
	for i := range q.items {
		if q.items[i].NextTryAt.After(now) {
			continue
		}
		if minIdx < 0 || q.items[i].NextTryAt.Before(q.items[minIdx].NextTryAt) {
			minIdx = i
		}
	}
	if minIdx < 0 {
		return TrafficBatch{}, false
	}
	b := q.items[minIdx]
	q.items = append(q.items[:minIdx], q.items[minIdx+1:]...)
	_ = q.saveLocked()
	return b, true
}

func (q *RetryQueue) RequeueFailed(batch TrafficBatch, errMsg string) {
	if len(batch.Rows) == 0 {
		return
	}
	batch.Attempts++
	batch.LastError = errMsg
	batch.NextTryAt = time.Now().Add(backoff(batch.Attempts))
	q.mu.Lock()
	q.pushLocked(batch)
	_ = q.saveLocked()
	q.mu.Unlock()
}

func backoff(attempt int) time.Duration {
	if attempt <= 0 {
		return 2 * time.Second
	}
	d := time.Second << minInt(attempt, 8)
	if d > 5*time.Minute {
		return 5 * time.Minute
	}
	return d
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (q *RetryQueue) pushLocked(b TrafficBatch) {
	if len(q.items) >= q.maxLen {
		q.items = q.items[1:]
	}
	q.items = append(q.items, b)
}

func (q *RetryQueue) load() error {
	b, err := os.ReadFile(q.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(b) == 0 {
		return nil
	}
	var items []TrafficBatch
	if err = json.Unmarshal(b, &items); err != nil {
		return err
	}
	q.items = items
	return nil
}

func (q *RetryQueue) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(q.filePath), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(q.items)
	if err != nil {
		return err
	}
	tmp := q.filePath + ".tmp"
	if err = os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, q.filePath)
}
