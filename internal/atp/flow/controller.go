package flow

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrSessionWindowExceeded = errors.New("session window exceeded")
	ErrStreamWindowExceeded  = errors.New("stream window exceeded")
	ErrUnknownStream         = errors.New("unknown stream")
)

type Config struct {
	SessionWindow uint32
	StreamWindow  uint32
}

func (c Config) withDefaults() Config {
	if c.SessionWindow == 0 {
		c.SessionWindow = 8 * 1024 * 1024
	}
	if c.StreamWindow == 0 {
		c.StreamWindow = 512 * 1024
	}
	return c
}

type Stats struct {
	SessionSendCredit   uint32
	OpenStreams         int
	QueueDepth          int
	MaxQueueDepth       int
	ImmediateGrantTotal uint64
	ReserveRejectTotal  uint64
	WaitEnqueueTotal    uint64
	WaitGrantTotal      uint64
	WaitCancelTotal     uint64
	WaitErrorTotal      uint64
	TotalWaitDuration   time.Duration
}

type streamState struct {
	sendCredit uint32
}

type Controller struct {
	mu sync.Mutex
	cv *sync.Cond

	cfg Config

	sessionSendCredit uint32
	streams           map[uint32]*streamState
	waitQueue         []*waitRequest

	immediateGrantTotal uint64
	reserveRejectTotal  uint64
	waitEnqueueTotal    uint64
	waitGrantTotal      uint64
	waitCancelTotal     uint64
	waitErrorTotal      uint64
	totalWaitDuration   time.Duration
	maxQueueDepth       int
}

type waitRequest struct {
	streamID   uint32
	n          uint32
	err        error
	done       bool
	canceled   bool
	enqueuedAt time.Time
}

func NewController(cfg Config) *Controller {
	cfg = cfg.withDefaults()
	c := &Controller{
		cfg:               cfg,
		sessionSendCredit: cfg.SessionWindow,
		streams:           make(map[uint32]*streamState),
	}
	c.cv = sync.NewCond(&c.mu)
	return c
}

func (c *Controller) OpenStream(streamID uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.streams[streamID]; ok {
		return
	}
	c.streams[streamID] = &streamState{sendCredit: c.cfg.StreamWindow}
	c.processWaitQueueLocked(time.Now())
	c.cv.Broadcast()
}

func (c *Controller) CloseStream(streamID uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.streams, streamID)
	c.processWaitQueueLocked(time.Now())
	c.cv.Broadcast()
}

func (c *Controller) ReserveSend(streamID uint32, n uint32) error {
	if n == 0 {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	st, ok := c.streams[streamID]
	if !ok {
		return ErrUnknownStream
	}
	if c.tryReserveLocked(streamID, n) {
		c.immediateGrantTotal++
		return nil
	}
	c.reserveRejectTotal++
	if c.sessionSendCredit < n {
		return ErrSessionWindowExceeded
	}
	if st.sendCredit < n {
		return fmt.Errorf("%w: stream=%d", ErrStreamWindowExceeded, streamID)
	}
	return ErrSessionWindowExceeded
}

func (c *Controller) ReserveSendWait(ctx context.Context, streamID uint32, n uint32) error {
	if n == 0 {
		return nil
	}

	c.mu.Lock()
	if _, ok := c.streams[streamID]; !ok {
		c.mu.Unlock()
		return ErrUnknownStream
	}
	if len(c.waitQueue) == 0 && c.tryReserveLocked(streamID, n) {
		c.immediateGrantTotal++
		c.mu.Unlock()
		return nil
	}

	now := time.Now()
	req := &waitRequest{streamID: streamID, n: n, enqueuedAt: now}
	c.waitQueue = append(c.waitQueue, req)
	c.waitEnqueueTotal++
	if qd := len(c.waitQueue); qd > c.maxQueueDepth {
		c.maxQueueDepth = qd
	}

	var stopNotify chan struct{}
	if ctx != nil {
		stopNotify = make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				c.mu.Lock()
				c.cv.Broadcast()
				c.mu.Unlock()
			case <-stopNotify:
			}
		}()
	}

	for {
		if ctx != nil && ctx.Err() != nil && !req.canceled {
			req.canceled = true
			req.err = ctx.Err()
		}
		c.processWaitQueueLocked(time.Now())
		if req.done {
			if stopNotify != nil {
				close(stopNotify)
			}
			err := req.err
			c.mu.Unlock()
			return err
		}
		c.cv.Wait()
	}
}

func (c *Controller) AddSessionCredit(delta uint32) {
	if delta == 0 {
		return
	}
	c.mu.Lock()
	c.sessionSendCredit += delta
	c.processWaitQueueLocked(time.Now())
	c.cv.Broadcast()
	c.mu.Unlock()
}

func (c *Controller) AddStreamCredit(streamID uint32, delta uint32) error {
	if delta == 0 {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	st, ok := c.streams[streamID]
	if !ok {
		return ErrUnknownStream
	}
	st.sendCredit += delta
	c.processWaitQueueLocked(time.Now())
	c.cv.Broadcast()
	return nil
}

func (c *Controller) Snapshot(streamID uint32) (sessionCredit uint32, streamCredit uint32, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	st, ok := c.streams[streamID]
	if !ok {
		return 0, 0, ErrUnknownStream
	}
	return c.sessionSendCredit, st.sendCredit, nil
}

func (c *Controller) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Stats{
		SessionSendCredit:   c.sessionSendCredit,
		OpenStreams:         len(c.streams),
		QueueDepth:          len(c.waitQueue),
		MaxQueueDepth:       c.maxQueueDepth,
		ImmediateGrantTotal: c.immediateGrantTotal,
		ReserveRejectTotal:  c.reserveRejectTotal,
		WaitEnqueueTotal:    c.waitEnqueueTotal,
		WaitGrantTotal:      c.waitGrantTotal,
		WaitCancelTotal:     c.waitCancelTotal,
		WaitErrorTotal:      c.waitErrorTotal,
		TotalWaitDuration:   c.totalWaitDuration,
	}
}

func (c *Controller) tryReserveLocked(streamID uint32, n uint32) bool {
	st, ok := c.streams[streamID]
	if !ok {
		return false
	}
	if c.sessionSendCredit < n || st.sendCredit < n {
		return false
	}
	c.sessionSendCredit -= n
	st.sendCredit -= n
	return true
}

func (c *Controller) processWaitQueueLocked(now time.Time) {
	if len(c.waitQueue) == 0 {
		return
	}
	out := c.waitQueue[:0]
	for _, req := range c.waitQueue {
		if req.done {
			continue
		}
		if req.canceled {
			req.done = true
			if req.err == nil {
				req.err = context.Canceled
			}
			c.waitCancelTotal++
			c.totalWaitDuration += now.Sub(req.enqueuedAt)
			continue
		}
		if _, ok := c.streams[req.streamID]; !ok {
			req.done = true
			req.err = ErrUnknownStream
			c.waitErrorTotal++
			c.totalWaitDuration += now.Sub(req.enqueuedAt)
			continue
		}
		if c.tryReserveLocked(req.streamID, req.n) {
			req.done = true
			c.waitGrantTotal++
			c.totalWaitDuration += now.Sub(req.enqueuedAt)
			continue
		}
		out = append(out, req)
	}
	c.waitQueue = out
}
