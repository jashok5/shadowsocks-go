package stream

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"sync"

	"github.com/jashok5/shadowsocks-go/internal/atp/flow"
	"github.com/jashok5/shadowsocks-go/internal/atp/protocol"
	"github.com/jashok5/shadowsocks-go/internal/atp/session"
)

type EventType uint8

const (
	EventOpen EventType = iota + 1
	EventData
	EventDatagram
	EventWindowUpdate
	EventClose
)

type Event struct {
	Type        EventType
	StreamID    uint32
	Data        []byte
	WindowDelta uint32
	Network     uint8
	Host        string
	Port        uint16
}

type Mux struct {
	sessionID    uint64
	seq          *session.Sequencer
	writer       io.Writer
	writeFn      func(*protocol.Frame) error
	flow         *flow.Controller
	mu           sync.Mutex
	writeMu      sync.Mutex
	nextStreamID uint32
	opened       map[uint32]struct{}
}

type FlowStats = flow.Stats

func NewMux(sessionID uint64, writer io.Writer, initialStreamID uint32) *Mux {
	return NewMuxWithFlow(sessionID, writer, initialStreamID, nil)
}

func NewMuxWithFlow(sessionID uint64, writer io.Writer, initialStreamID uint32, fc *flow.Controller) *Mux {
	return &Mux{
		sessionID:    sessionID,
		seq:          session.NewSequencer(0),
		writer:       writer,
		flow:         fc,
		nextStreamID: initialStreamID,
		opened:       make(map[uint32]struct{}),
	}
}

func NewMuxWithFrameWriter(sessionID uint64, initialStreamID uint32, fc *flow.Controller, writeFn func(*protocol.Frame) error) *Mux {
	return &Mux{
		sessionID:    sessionID,
		seq:          session.NewSequencer(0),
		writeFn:      writeFn,
		flow:         fc,
		nextStreamID: initialStreamID,
		opened:       make(map[uint32]struct{}),
	}
}

func (m *Mux) OpenStream() (uint32, error) {
	return m.OpenStreamWithRequest(protocol.OpenStreamRequest{Network: protocol.NetworkTCP, Host: "stream.local", Port: 1})
}

func (m *Mux) OpenStreamWithRequest(req protocol.OpenStreamRequest) (uint32, error) {
	payload, err := protocol.EncodeOpenStreamPayload(req)
	if err != nil {
		return 0, err
	}
	m.mu.Lock()
	streamID := m.nextStreamID
	m.nextStreamID += 2
	m.opened[streamID] = struct{}{}
	m.mu.Unlock()
	if m.flow != nil {
		m.flow.OpenStream(streamID)
	}

	frame := &protocol.Frame{
		Header: protocol.Header{
			Magic:     protocol.Magic,
			Version:   protocol.VersionV1,
			Type:      protocol.TypeOpenStream,
			SessionID: m.sessionID,
			StreamID:  streamID,
			Seq:       m.seq.Next(),
		},
		Payload: payload,
	}

	if err := m.writeFrame(frame); err != nil {
		return 0, err
	}
	return streamID, nil
}

func (m *Mux) SendData(streamID uint32, payload []byte) error {
	return m.SendDataContext(context.Background(), streamID, payload)
}

func (m *Mux) SendDatagram(streamID uint32, payload []byte) error {
	if !m.isOpened(streamID) {
		return fmt.Errorf("stream %d not opened", streamID)
	}
	frame := &protocol.Frame{
		Header: protocol.Header{
			Magic:     protocol.Magic,
			Version:   protocol.VersionV1,
			Type:      protocol.TypeDatagram,
			SessionID: m.sessionID,
			StreamID:  streamID,
			Seq:       m.seq.Next(),
		},
		Payload: payload,
	}
	return m.writeFrame(frame)
}

func (m *Mux) SendDataContext(ctx context.Context, streamID uint32, payload []byte) error {
	if !m.isOpened(streamID) {
		return fmt.Errorf("stream %d not opened", streamID)
	}
	if m.flow != nil {
		if err := m.flow.ReserveSendWait(ctx, streamID, uint32(len(payload))); err != nil {
			return err
		}
	}
	frame := &protocol.Frame{
		Header: protocol.Header{
			Magic:     protocol.Magic,
			Version:   protocol.VersionV1,
			Type:      protocol.TypeData,
			SessionID: m.sessionID,
			StreamID:  streamID,
			Seq:       m.seq.Next(),
		},
		Payload: payload,
	}
	return m.writeFrame(frame)
}

func (m *Mux) SendSessionWindowUpdate(delta uint32) error {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, delta)
	frame := &protocol.Frame{
		Header: protocol.Header{
			Magic:     protocol.Magic,
			Version:   protocol.VersionV1,
			Type:      protocol.TypeWindowUp,
			SessionID: m.sessionID,
			StreamID:  0,
			Seq:       m.seq.Next(),
		},
		Payload: b,
	}
	return m.writeFrame(frame)
}

func (m *Mux) SendWindowUpdate(streamID uint32, delta uint32) error {
	if !m.isOpened(streamID) {
		return fmt.Errorf("stream %d not opened", streamID)
	}
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, delta)
	frame := &protocol.Frame{
		Header: protocol.Header{
			Magic:     protocol.Magic,
			Version:   protocol.VersionV1,
			Type:      protocol.TypeWindowUp,
			SessionID: m.sessionID,
			StreamID:  streamID,
			Seq:       m.seq.Next(),
		},
		Payload: b,
	}
	return m.writeFrame(frame)
}

func (m *Mux) SendCloseStream(streamID uint32) error {
	if !m.isOpened(streamID) {
		return fmt.Errorf("stream %d not opened", streamID)
	}
	frame := &protocol.Frame{
		Header: protocol.Header{
			Magic:     protocol.Magic,
			Version:   protocol.VersionV1,
			Type:      protocol.TypeCloseStream,
			SessionID: m.sessionID,
			StreamID:  streamID,
			Seq:       m.seq.Next(),
		},
	}
	if err := m.writeFrame(frame); err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.opened, streamID)
	m.mu.Unlock()
	return nil
}

func (m *Mux) HandleFrame(f *protocol.Frame) (Event, error) {
	if f.Header.SessionID != m.sessionID {
		return Event{}, fmt.Errorf("session mismatch: got=%d want=%d", f.Header.SessionID, m.sessionID)
	}

	switch f.Header.Type {
	case protocol.TypeOpenStream:
		var openReq protocol.OpenStreamRequest
		if len(f.Payload) > 0 {
			decodedReq, err := protocol.DecodeOpenStreamPayload(f.Payload)
			if err != nil {
				return Event{}, err
			}
			openReq = decodedReq
		}
		m.mu.Lock()
		m.opened[f.Header.StreamID] = struct{}{}
		m.mu.Unlock()
		if m.flow != nil {
			m.flow.OpenStream(f.Header.StreamID)
		}
		return Event{Type: EventOpen, StreamID: f.Header.StreamID, Network: openReq.Network, Host: openReq.Host, Port: openReq.Port}, nil
	case protocol.TypeData:
		if !m.isOpened(f.Header.StreamID) {
			return Event{}, fmt.Errorf("data for unopened stream %d", f.Header.StreamID)
		}
		copyData := make([]byte, len(f.Payload))
		copy(copyData, f.Payload)
		return Event{Type: EventData, StreamID: f.Header.StreamID, Data: copyData}, nil
	case protocol.TypeDatagram:
		if !m.isOpened(f.Header.StreamID) {
			return Event{}, fmt.Errorf("datagram for unopened stream %d", f.Header.StreamID)
		}
		copyData := make([]byte, len(f.Payload))
		copy(copyData, f.Payload)
		return Event{Type: EventDatagram, StreamID: f.Header.StreamID, Data: copyData}, nil
	case protocol.TypeWindowUp:
		if len(f.Payload) != 4 {
			return Event{}, fmt.Errorf("invalid window_update payload")
		}
		delta := binary.BigEndian.Uint32(f.Payload)
		if m.flow != nil {
			if f.Header.StreamID == 0 {
				m.flow.AddSessionCredit(delta)
			} else if err := m.flow.AddStreamCredit(f.Header.StreamID, delta); err != nil {
				return Event{}, err
			}
		}
		return Event{Type: EventWindowUpdate, StreamID: f.Header.StreamID, WindowDelta: delta}, nil
	case protocol.TypeCloseStream:
		m.mu.Lock()
		delete(m.opened, f.Header.StreamID)
		m.mu.Unlock()
		if m.flow != nil {
			m.flow.CloseStream(f.Header.StreamID)
		}
		return Event{Type: EventClose, StreamID: f.Header.StreamID}, nil
	default:
		return Event{}, fmt.Errorf("unsupported frame type %d", f.Header.Type)
	}
}

func (m *Mux) isOpened(streamID uint32) bool {
	m.mu.Lock()
	_, ok := m.opened[streamID]
	m.mu.Unlock()
	return ok
}

func (m *Mux) writeFrame(f *protocol.Frame) error {
	m.writeMu.Lock()
	var err error
	if m.writeFn != nil {
		err = m.writeFn(f)
	} else {
		err = protocol.WriteFrame(m.writer, f)
	}
	m.writeMu.Unlock()
	return err
}

func (m *Mux) FlowStats() (FlowStats, bool) {
	if m.flow == nil {
		return FlowStats{}, false
	}
	return m.flow.Stats(), true
}
