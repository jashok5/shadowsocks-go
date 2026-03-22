package runtime

import (
	"net"
	"sync"
)

type connTracker struct {
	mu    sync.Mutex
	conns map[net.Conn]struct{}
}

func newConnTracker() *connTracker {
	return &connTracker{conns: make(map[net.Conn]struct{})}
}

func (t *connTracker) Add(conn net.Conn) {
	if t == nil || conn == nil {
		return
	}
	t.mu.Lock()
	t.conns[conn] = struct{}{}
	t.mu.Unlock()
}

func (t *connTracker) Remove(conn net.Conn) {
	if t == nil || conn == nil {
		return
	}
	t.mu.Lock()
	delete(t.conns, conn)
	t.mu.Unlock()
}

func (t *connTracker) CloseAll() {
	if t == nil {
		return
	}
	t.mu.Lock()
	conns := make([]net.Conn, 0, len(t.conns))
	for c := range t.conns {
		conns = append(conns, c)
	}
	t.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}
