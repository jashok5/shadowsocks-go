package flow

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestReserveSendHonorsWindows(t *testing.T) {
	c := NewController(Config{SessionWindow: 10, StreamWindow: 6})
	c.OpenStream(1)

	if err := c.ReserveSend(1, 5); err != nil {
		t.Fatalf("reserve send: %v", err)
	}
	if err := c.ReserveSend(1, 2); err == nil {
		t.Fatalf("expected stream window error")
	}
}

func TestReserveSendSessionWindowAcrossStreams(t *testing.T) {
	c := NewController(Config{SessionWindow: 10, StreamWindow: 10})
	c.OpenStream(1)
	c.OpenStream(3)

	if err := c.ReserveSend(1, 7); err != nil {
		t.Fatalf("reserve stream 1: %v", err)
	}
	if err := c.ReserveSend(3, 4); err == nil {
		t.Fatalf("expected session window error")
	}

	c.AddSessionCredit(2)
	if err := c.ReserveSend(3, 2); err != nil {
		t.Fatalf("reserve after session update: %v", err)
	}
}

func TestAddStreamCredit(t *testing.T) {
	c := NewController(Config{SessionWindow: 10, StreamWindow: 2})
	c.OpenStream(1)

	if err := c.ReserveSend(1, 2); err != nil {
		t.Fatalf("reserve send: %v", err)
	}
	if err := c.AddStreamCredit(1, 5); err != nil {
		t.Fatalf("add stream credit: %v", err)
	}
	if err := c.ReserveSend(1, 5); err != nil {
		t.Fatalf("reserve after stream update: %v", err)
	}
}

func TestReserveSendWaitUnblocksWithCredit(t *testing.T) {
	c := NewController(Config{SessionWindow: 10, StreamWindow: 3})
	c.OpenStream(1)

	if err := c.ReserveSend(1, 3); err != nil {
		t.Fatalf("reserve initial: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- c.ReserveSendWait(context.Background(), 1, 2)
	}()

	select {
	case err := <-errCh:
		t.Fatalf("wait should block, got err=%v", err)
	case <-time.After(40 * time.Millisecond):
	}

	if err := c.AddStreamCredit(1, 2); err != nil {
		t.Fatalf("add credit: %v", err)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("wait err: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("wait did not unblock")
	}

	st := c.Stats()
	if st.WaitEnqueueTotal != 1 || st.WaitGrantTotal != 1 || st.WaitCancelTotal != 0 {
		t.Fatalf("unexpected stats after unblock: %+v", st)
	}
}

func TestReserveSendWaitContextCancel(t *testing.T) {
	c := NewController(Config{SessionWindow: 10, StreamWindow: 1})
	c.OpenStream(1)
	if err := c.ReserveSend(1, 1); err != nil {
		t.Fatalf("reserve initial: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	err := c.ReserveSendWait(ctx, 1, 1)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}

	st := c.Stats()
	if st.WaitEnqueueTotal != 1 || st.WaitCancelTotal != 1 {
		t.Fatalf("unexpected cancel stats: %+v", st)
	}
}

func TestReserveSendWaitNoStarvationAcrossStreams(t *testing.T) {
	c := NewController(Config{SessionWindow: 100, StreamWindow: 1})
	c.OpenStream(1)
	c.OpenStream(3)

	if err := c.ReserveSend(1, 1); err != nil {
		t.Fatalf("reserve stream1 initial: %v", err)
	}
	if err := c.ReserveSend(3, 1); err != nil {
		t.Fatalf("reserve stream3 initial: %v", err)
	}

	tagCh := make(chan string, 2)
	errCh := make(chan error, 2)

	go func() {
		err := c.ReserveSendWait(context.Background(), 1, 1)
		if err == nil {
			tagCh <- "s1"
		}
		errCh <- err
	}()

	time.Sleep(20 * time.Millisecond)
	go func() {
		err := c.ReserveSendWait(context.Background(), 3, 1)
		if err == nil {
			tagCh <- "s3"
		}
		errCh <- err
	}()

	if err := c.AddStreamCredit(3, 1); err != nil {
		t.Fatalf("add stream3 credit: %v", err)
	}

	first := <-tagCh
	if first != "s3" {
		t.Fatalf("expected first grant to ready stream s3, got %s", first)
	}

	select {
	case second := <-tagCh:
		t.Fatalf("unexpected early second grant: %s", second)
	case <-time.After(40 * time.Millisecond):
	}

	if err := c.AddStreamCredit(1, 1); err != nil {
		t.Fatalf("add stream1 credit: %v", err)
	}
	second := <-tagCh
	if second != "s1" {
		t.Fatalf("expected second grant to s1, got %s", second)
	}

	if err := <-errCh; err != nil {
		t.Fatalf("first wait err: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("second wait err: %v", err)
	}

	st := c.Stats()
	if st.WaitGrantTotal != 2 {
		t.Fatalf("expected two wait grants, got %+v", st)
	}
}

func TestReserveSendRejectStats(t *testing.T) {
	c := NewController(Config{SessionWindow: 1, StreamWindow: 10})
	c.OpenStream(1)

	if err := c.ReserveSend(1, 2); !errors.Is(err, ErrSessionWindowExceeded) {
		t.Fatalf("expected session window exceeded, got %v", err)
	}
	st := c.Stats()
	if st.ReserveRejectTotal != 1 {
		t.Fatalf("expected one reject, got %+v", st)
	}
}
