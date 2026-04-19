package stream

import (
	"bytes"
	"testing"

	"github.com/jashok5/shadowsocks-go/internal/atp/protocol"
)

func TestOpenStreamAndSendFrames(t *testing.T) {
	var buf bytes.Buffer
	m := NewMux(99, &buf, 1)

	streamID, err := m.OpenStreamWithRequest(protocol.OpenStreamRequest{Network: protocol.NetworkTCP, Host: "example.com", Port: 443})
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	if streamID != 1 {
		t.Fatalf("stream id=%d want=1", streamID)
	}

	if err := m.SendData(streamID, []byte("hello")); err != nil {
		t.Fatalf("send data: %v", err)
	}
	if err := m.SendWindowUpdate(streamID, 4096); err != nil {
		t.Fatalf("send window update: %v", err)
	}
	if err := m.SendCloseStream(streamID); err != nil {
		t.Fatalf("send close stream: %v", err)
	}

	f1, err := protocol.ReadFrame(&buf)
	if err != nil {
		t.Fatalf("read frame1: %v", err)
	}
	if f1.Header.Type != protocol.TypeOpenStream || f1.Header.StreamID != 1 || f1.Header.Seq != 1 {
		t.Fatalf("unexpected open frame: %+v", f1.Header)
	}
	openReq, err := protocol.DecodeOpenStreamPayload(f1.Payload)
	if err != nil {
		t.Fatalf("decode open payload: %v", err)
	}
	if openReq.Network != protocol.NetworkTCP || openReq.Host != "example.com" || openReq.Port != 443 {
		t.Fatalf("unexpected open payload: %+v", openReq)
	}

	f2, err := protocol.ReadFrame(&buf)
	if err != nil {
		t.Fatalf("read frame2: %v", err)
	}
	if f2.Header.Type != protocol.TypeData || f2.Header.Seq != 2 || string(f2.Payload) != "hello" {
		t.Fatalf("unexpected data frame: %+v payload=%q", f2.Header, string(f2.Payload))
	}

	f3, err := protocol.ReadFrame(&buf)
	if err != nil {
		t.Fatalf("read frame3: %v", err)
	}
	if f3.Header.Type != protocol.TypeWindowUp || f3.Header.Seq != 3 {
		t.Fatalf("unexpected window frame: %+v", f3.Header)
	}

	f4, err := protocol.ReadFrame(&buf)
	if err != nil {
		t.Fatalf("read frame4: %v", err)
	}
	if f4.Header.Type != protocol.TypeCloseStream || f4.Header.Seq != 4 {
		t.Fatalf("unexpected close frame: %+v", f4.Header)
	}

	if err := m.SendData(streamID, []byte("should-fail")); err == nil {
		t.Fatalf("expected send on closed stream to fail")
	}
}

func TestHandleFrameEvents(t *testing.T) {
	m := NewMux(123, &bytes.Buffer{}, 1)

	ev, err := m.HandleFrame(&protocol.Frame{Header: protocol.Header{Magic: protocol.Magic, Version: protocol.VersionV1, Type: protocol.TypeOpenStream, SessionID: 123, StreamID: 2}})
	if err != nil {
		t.Fatalf("handle open: %v", err)
	}
	if ev.Type != EventOpen || ev.StreamID != 2 {
		t.Fatalf("unexpected open event: %+v", ev)
	}

	opPayload, err := protocol.EncodeOpenStreamPayload(protocol.OpenStreamRequest{Network: protocol.NetworkUDP, Host: "8.8.8.8", Port: 53})
	if err != nil {
		t.Fatalf("encode open payload: %v", err)
	}
	ev, err = m.HandleFrame(&protocol.Frame{Header: protocol.Header{Magic: protocol.Magic, Version: protocol.VersionV1, Type: protocol.TypeOpenStream, SessionID: 123, StreamID: 4}, Payload: opPayload})
	if err != nil {
		t.Fatalf("handle open with payload: %v", err)
	}
	if ev.Type != EventOpen || ev.StreamID != 4 || ev.Network != protocol.NetworkUDP || ev.Host != "8.8.8.8" || ev.Port != 53 {
		t.Fatalf("unexpected open event payload: %+v", ev)
	}

	ev, err = m.HandleFrame(&protocol.Frame{Header: protocol.Header{Magic: protocol.Magic, Version: protocol.VersionV1, Type: protocol.TypeData, SessionID: 123, StreamID: 2}, Payload: []byte("x")})
	if err != nil {
		t.Fatalf("handle data: %v", err)
	}
	if ev.Type != EventData || string(ev.Data) != "x" {
		t.Fatalf("unexpected data event: %+v", ev)
	}

	ev, err = m.HandleFrame(&protocol.Frame{Header: protocol.Header{Magic: protocol.Magic, Version: protocol.VersionV1, Type: protocol.TypeDatagram, SessionID: 123, StreamID: 2}, Payload: []byte("u")})
	if err != nil {
		t.Fatalf("handle datagram: %v", err)
	}
	if ev.Type != EventDatagram || string(ev.Data) != "u" {
		t.Fatalf("unexpected datagram event: %+v", ev)
	}

	p := make([]byte, 4)
	p[3] = 10
	ev, err = m.HandleFrame(&protocol.Frame{Header: protocol.Header{Magic: protocol.Magic, Version: protocol.VersionV1, Type: protocol.TypeWindowUp, SessionID: 123, StreamID: 2}, Payload: p})
	if err != nil {
		t.Fatalf("handle window update: %v", err)
	}
	if ev.Type != EventWindowUpdate || ev.WindowDelta != 10 {
		t.Fatalf("unexpected window event: %+v", ev)
	}

	ev, err = m.HandleFrame(&protocol.Frame{Header: protocol.Header{Magic: protocol.Magic, Version: protocol.VersionV1, Type: protocol.TypeCloseStream, SessionID: 123, StreamID: 2}})
	if err != nil {
		t.Fatalf("handle close: %v", err)
	}
	if ev.Type != EventClose || ev.StreamID != 2 {
		t.Fatalf("unexpected close event: %+v", ev)
	}
}

func TestHandleFrameRejectsSessionMismatch(t *testing.T) {
	m := NewMux(1, &bytes.Buffer{}, 1)
	_, err := m.HandleFrame(&protocol.Frame{Header: protocol.Header{Type: protocol.TypeOpenStream, SessionID: 2, StreamID: 1}})
	if err == nil {
		t.Fatalf("expected session mismatch error")
	}
}
