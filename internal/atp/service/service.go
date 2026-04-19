package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"go.uber.org/zap"

	"github.com/jashok5/shadowsocks-go/internal/atp/atperr"
	"github.com/jashok5/shadowsocks-go/internal/atp/flow"
	"github.com/jashok5/shadowsocks-go/internal/atp/protocol"
	"github.com/jashok5/shadowsocks-go/internal/atp/recovery"
	"github.com/jashok5/shadowsocks-go/internal/atp/session"
	"github.com/jashok5/shadowsocks-go/internal/atp/stream"
	"github.com/jashok5/shadowsocks-go/internal/atp/transport"
)

const (
	TLVClientName   uint16 = 1
	TLVClientNonce  uint16 = 2
	TLVAuthToken    uint16 = 3
	TLVStatus       uint16 = 4
	TLVSessionID    uint16 = 5
	TLVServerNonce  uint16 = 6
	TLVErrorCode    uint16 = 7
	TLVErrorReason  uint16 = 8
	TLVResumeTicket uint16 = 9
	TLVResumeReq    uint16 = 10
)

type HandshakeConfig struct {
	Timeout time.Duration
}

func (c HandshakeConfig) withDefaults() HandshakeConfig {
	if c.Timeout <= 0 {
		c.Timeout = 10 * time.Second
	}
	return c
}

type ServerConfig struct {
	ExpectedToken string
	Handshake     HandshakeConfig
	Flow          flow.Config
	Recovery      recovery.Config
	Logger        *zap.Logger
}

type ClientConfig struct {
	ClientName string
	Token      string
	Handshake  HandshakeConfig
	Flow       flow.Config
	Logger     *zap.Logger
	Resume     string
}

type ServerSession struct {
	ID      uint64
	Mux     *stream.Mux
	Ticket  string
	Resumed bool
}

type ClientSession struct {
	ID      uint64
	Mux     *stream.Mux
	Ticket  string
	Resumed bool
}

type Server struct {
	config   ServerConfig
	recovery *recovery.Manager
}

func NewServer(cfg ServerConfig) (*Server, error) {
	if cfg.Logger == nil {
		cfg.Logger = zap.NewNop()
	}
	rm, err := recovery.NewManager(cfg.Recovery)
	if err != nil {
		return nil, err
	}
	return &Server{config: cfg, recovery: rm}, nil
}

func (s *Server) AcceptAndHandshake(ctx context.Context, conn transport.Conn) (*ServerSession, error) {
	cfg := s.config.Handshake.withDefaults()
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	traceID := newTraceID()
	seq := session.NewSequencer(0)

	hello, err := conn.ReadFrame(ctx)
	if err != nil {
		s.config.Logger.Warn("server_read_hello_failed", zap.String("trace_id", traceID), zap.Error(err))
		return nil, err
	}
	if hello.Header.Type != protocol.TypeHello {
		_ = writeErrorFrame(ctx, conn, seq.Next(), 0, atperr.CodeInvalidFrame, "expected hello")
		return nil, atperrFrame(atperr.CodeInvalidFrame, "expected hello")
	}

	helloMap, err := parseTLV(hello.Payload)
	if err != nil {
		_ = writeErrorFrame(ctx, conn, seq.Next(), 0, atperr.CodeInvalidFrame, "invalid hello tlv")
		return nil, err
	}
	if len(helloMap[TLVClientNonce]) == 0 {
		_ = writeErrorFrame(ctx, conn, seq.Next(), 0, atperr.CodeBadRequest, "missing client nonce")
		return nil, atperrFrame(atperr.CodeBadRequest, "missing client nonce")
	}

	binding := conn.RemoteAddr()
	resumed := false
	sid := uint64(0)
	if raw := helloMap[TLVResumeReq]; len(raw) > 0 {
		sid, err = s.recovery.Consume(string(raw), binding)
		if err == nil {
			resumed = true
		} else {
			s.config.Logger.Info("server_resume_rejected", zap.String("trace_id", traceID), zap.String("remote", binding), zap.Error(err))
		}
	}
	if sid == 0 {
		sid, err = session.NewSessionID()
		if err != nil {
			return nil, err
		}
	}

	serverNonce := make([]byte, 16)
	if _, err := rand.Read(serverNonce); err != nil {
		return nil, err
	}

	sidBytes := make([]byte, 8)
	binaryPutUint64(sidBytes, sid)
	helloPayload, err := protocol.EncodeTLVs([]protocol.TLV{{Type: TLVSessionID, Value: sidBytes}, {Type: TLVServerNonce, Value: serverNonce}})
	if err != nil {
		return nil, err
	}
	if err := conn.WriteFrame(ctx, &protocol.Frame{Header: protocol.Header{Magic: protocol.Magic, Version: protocol.VersionV1, Type: protocol.TypeHello, SessionID: sid, Seq: seq.Next()}, Payload: helloPayload}); err != nil {
		return nil, err
	}

	if !resumed {
		auth, err := conn.ReadFrame(ctx)
		if err != nil {
			return nil, err
		}
		if auth.Header.Type != protocol.TypeAuth || auth.Header.SessionID != sid {
			_ = writeErrorFrame(ctx, conn, seq.Next(), sid, atperr.CodeInvalidFrame, "expected auth")
			return nil, atperrFrame(atperr.CodeInvalidFrame, "expected auth")
		}
		authMap, err := parseTLV(auth.Payload)
		if err != nil {
			_ = writeErrorFrame(ctx, conn, seq.Next(), sid, atperr.CodeInvalidFrame, "invalid auth tlv")
			return nil, err
		}
		if string(authMap[TLVAuthToken]) != s.config.ExpectedToken {
			_ = writeErrorFrame(ctx, conn, seq.Next(), sid, atperr.CodeAuthFailed, "auth_failed")
			return nil, atperrFrame(atperr.CodeAuthFailed, "auth_failed")
		}
	}

	ticket, err := s.recovery.Issue(sid, binding)
	if err != nil {
		return nil, err
	}
	statusPayload, err := protocol.EncodeTLVs([]protocol.TLV{{Type: TLVStatus, Value: []byte("ok")}, {Type: TLVResumeTicket, Value: []byte(ticket)}})
	if err != nil {
		return nil, err
	}
	if err := conn.WriteFrame(ctx, &protocol.Frame{Header: protocol.Header{Magic: protocol.Magic, Version: protocol.VersionV1, Type: protocol.TypeAuth, SessionID: sid, Seq: seq.Next()}, Payload: statusPayload}); err != nil {
		return nil, err
	}

	fc := flow.NewController(s.config.Flow)
	mux := stream.NewMuxWithFrameWriter(sid, 2, fc, func(f *protocol.Frame) error { return conn.WriteFrame(context.Background(), f) })
	s.config.Logger.Info("server_handshake_success", zap.String("trace_id", traceID), zap.String("remote", conn.RemoteAddr()), zap.Uint64("session_id", sid), zap.Bool("resumed", resumed))
	return &ServerSession{ID: sid, Mux: mux, Ticket: ticket, Resumed: resumed}, nil
}

func (s *Server) ServeEcho(ctx context.Context, conn transport.Conn, ss *ServerSession) error {
	dataFrames := 0
	for {
		f, err := conn.ReadFrame(ctx)
		if err != nil {
			s.logFlowStats("server_flow_final", ss.Mux, zap.String("remote", conn.RemoteAddr()), zap.Uint64("session_id", ss.ID), zap.String("reason", "read_error"), zap.Error(err))
			return err
		}
		ev, err := ss.Mux.HandleFrame(f)
		if err != nil {
			s.logFlowStats("server_flow_final", ss.Mux, zap.String("remote", conn.RemoteAddr()), zap.Uint64("session_id", ss.ID), zap.String("reason", "handle_error"), zap.Error(err))
			return err
		}
		switch ev.Type {
		case stream.EventData:
			if err := ss.Mux.SendDataContext(ctx, ev.StreamID, []byte("echo:"+string(ev.Data))); err != nil {
				s.logFlowStats("server_flow_final", ss.Mux, zap.String("remote", conn.RemoteAddr()), zap.Uint64("session_id", ss.ID), zap.String("reason", "send_error"), zap.Error(err))
				return err
			}
			_ = ss.Mux.SendWindowUpdate(ev.StreamID, uint32(len(ev.Data)))
			_ = ss.Mux.SendSessionWindowUpdate(uint32(len(ev.Data)))
			dataFrames++
			if dataFrames%10 == 0 {
				s.logFlowStats("server_flow_progress", ss.Mux, zap.String("remote", conn.RemoteAddr()), zap.Uint64("session_id", ss.ID), zap.Int("data_frames", dataFrames))
			}
		case stream.EventClose:
			s.logFlowStats("server_flow_final", ss.Mux, zap.String("remote", conn.RemoteAddr()), zap.Uint64("session_id", ss.ID), zap.String("reason", "peer_closed"), zap.Int("data_frames", dataFrames))
			return nil
		}
	}
}

type Client struct {
	config ClientConfig
}

func NewClient(cfg ClientConfig) *Client {
	if cfg.Logger == nil {
		cfg.Logger = zap.NewNop()
	}
	return &Client{config: cfg}
}

func (c *Client) Handshake(ctx context.Context, conn transport.Conn) (*ClientSession, error) {
	cfg := c.config.Handshake.withDefaults()
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	traceID := newTraceID()
	seq := session.NewSequencer(0)
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}

	helloTLVs := []protocol.TLV{{Type: TLVClientName, Value: []byte(c.config.ClientName)}, {Type: TLVClientNonce, Value: nonce}}
	if c.config.Resume != "" {
		helloTLVs = append(helloTLVs, protocol.TLV{Type: TLVResumeReq, Value: []byte(c.config.Resume)})
	}
	helloPayload, err := protocol.EncodeTLVs(helloTLVs)
	if err != nil {
		return nil, err
	}
	if err := conn.WriteFrame(ctx, &protocol.Frame{Header: protocol.Header{Magic: protocol.Magic, Version: protocol.VersionV1, Type: protocol.TypeHello, Seq: seq.Next()}, Payload: helloPayload}); err != nil {
		return nil, err
	}

	helloResp, err := conn.ReadFrame(ctx)
	if err != nil {
		return nil, err
	}
	if helloResp.Header.Type == protocol.TypeError {
		return nil, mapErrorFrame(helloResp)
	}
	if helloResp.Header.Type != protocol.TypeHello {
		return nil, atperrFrame(atperr.CodeInvalidFrame, "expected hello response")
	}
	helloMap, err := parseTLV(helloResp.Payload)
	if err != nil {
		return nil, err
	}
	if len(helloMap[TLVSessionID]) != 8 {
		return nil, atperrFrame(atperr.CodeInvalidFrame, "missing session id")
	}
	sid := binaryUint64(helloMap[TLVSessionID])

	resumed := false
	if c.config.Resume == "" {
		authPayload, err := protocol.EncodeTLVs([]protocol.TLV{{Type: TLVAuthToken, Value: []byte(c.config.Token)}})
		if err != nil {
			return nil, err
		}
		if err := conn.WriteFrame(ctx, &protocol.Frame{Header: protocol.Header{Magic: protocol.Magic, Version: protocol.VersionV1, Type: protocol.TypeAuth, SessionID: sid, Seq: seq.Next()}, Payload: authPayload}); err != nil {
			return nil, err
		}
	} else {
		resumed = true
	}

	authResp, err := conn.ReadFrame(ctx)
	if err != nil {
		return nil, err
	}
	if authResp.Header.Type == protocol.TypeError {
		return nil, mapErrorFrame(authResp)
	}
	if authResp.Header.Type != protocol.TypeAuth {
		return nil, atperrFrame(atperr.CodeInvalidFrame, "expected auth response")
	}
	authMap, err := parseTLV(authResp.Payload)
	if err != nil {
		return nil, err
	}
	if string(authMap[TLVStatus]) != "ok" {
		return nil, atperrFrame(atperr.CodeAuthFailed, "auth_failed")
	}
	ticket := string(authMap[TLVResumeTicket])

	fc := flow.NewController(c.config.Flow)
	mux := stream.NewMuxWithFrameWriter(sid, 1, fc, func(f *protocol.Frame) error { return conn.WriteFrame(context.Background(), f) })
	c.config.Logger.Info("client_handshake_success", zap.String("trace_id", traceID), zap.String("remote", conn.RemoteAddr()), zap.Uint64("session_id", sid), zap.Bool("resumed", resumed))
	return &ClientSession{ID: sid, Mux: mux, Ticket: ticket, Resumed: resumed}, nil
}

func (c *Client) Exchange(ctx context.Context, conn transport.Conn, cs *ClientSession, messages []string) ([]string, error) {
	if len(messages) == 0 {
		return nil, nil
	}
	streamID, err := cs.Mux.OpenStream()
	if err != nil {
		return nil, err
	}
	responses := make([]string, 0, len(messages))
	for _, msg := range messages {
		if err := cs.Mux.SendDataContext(ctx, streamID, []byte(msg)); err != nil {
			c.logFlowStats("client_flow_final", cs.Mux, zap.String("remote", conn.RemoteAddr()), zap.Uint64("session_id", cs.ID), zap.String("reason", "send_error"), zap.Error(err))
			return nil, err
		}
		for {
			f, err := conn.ReadFrame(ctx)
			if err != nil {
				c.logFlowStats("client_flow_final", cs.Mux, zap.String("remote", conn.RemoteAddr()), zap.Uint64("session_id", cs.ID), zap.String("reason", "read_error"), zap.Error(err))
				return nil, err
			}
			ev, err := cs.Mux.HandleFrame(f)
			if err != nil {
				c.logFlowStats("client_flow_final", cs.Mux, zap.String("remote", conn.RemoteAddr()), zap.Uint64("session_id", cs.ID), zap.String("reason", "handle_error"), zap.Error(err))
				return nil, err
			}
			if ev.Type == stream.EventData && ev.StreamID == streamID {
				responses = append(responses, string(ev.Data))
				_ = cs.Mux.SendWindowUpdate(streamID, uint32(len(msg)))
				_ = cs.Mux.SendSessionWindowUpdate(uint32(len(msg)))
				if len(responses)%10 == 0 {
					c.logFlowStats("client_flow_progress", cs.Mux, zap.String("remote", conn.RemoteAddr()), zap.Uint64("session_id", cs.ID), zap.Int("responses", len(responses)))
				}
				break
			}
		}
	}
	if err := cs.Mux.SendCloseStream(streamID); err != nil {
		c.logFlowStats("client_flow_final", cs.Mux, zap.String("remote", conn.RemoteAddr()), zap.Uint64("session_id", cs.ID), zap.String("reason", "close_error"), zap.Error(err))
		return nil, err
	}
	c.logFlowStats("client_flow_final", cs.Mux, zap.String("remote", conn.RemoteAddr()), zap.Uint64("session_id", cs.ID), zap.String("reason", "done"), zap.Int("responses", len(responses)))
	return responses, nil
}

func (s *Server) logFlowStats(msg string, mux *stream.Mux, fields ...zap.Field) {
	st, ok := mux.FlowStats()
	if !ok {
		s.config.Logger.Info(msg, fields...)
		return
	}
	fields = append(fields,
		zap.Uint32("session_credit", st.SessionSendCredit),
		zap.Int("open_streams", st.OpenStreams),
		zap.Int("queue_depth", st.QueueDepth),
		zap.Int("max_queue_depth", st.MaxQueueDepth),
		zap.Uint64("immediate_grant_total", st.ImmediateGrantTotal),
		zap.Uint64("reserve_reject_total", st.ReserveRejectTotal),
		zap.Uint64("wait_enqueue_total", st.WaitEnqueueTotal),
		zap.Uint64("wait_grant_total", st.WaitGrantTotal),
		zap.Uint64("wait_cancel_total", st.WaitCancelTotal),
		zap.Uint64("wait_error_total", st.WaitErrorTotal),
		zap.Duration("wait_duration_total", st.TotalWaitDuration),
	)
	s.config.Logger.Info(msg, fields...)
}

func (c *Client) logFlowStats(msg string, mux *stream.Mux, fields ...zap.Field) {
	st, ok := mux.FlowStats()
	if !ok {
		c.config.Logger.Info(msg, fields...)
		return
	}
	fields = append(fields,
		zap.Uint32("session_credit", st.SessionSendCredit),
		zap.Int("open_streams", st.OpenStreams),
		zap.Int("queue_depth", st.QueueDepth),
		zap.Int("max_queue_depth", st.MaxQueueDepth),
		zap.Uint64("immediate_grant_total", st.ImmediateGrantTotal),
		zap.Uint64("reserve_reject_total", st.ReserveRejectTotal),
		zap.Uint64("wait_enqueue_total", st.WaitEnqueueTotal),
		zap.Uint64("wait_grant_total", st.WaitGrantTotal),
		zap.Uint64("wait_cancel_total", st.WaitCancelTotal),
		zap.Uint64("wait_error_total", st.WaitErrorTotal),
		zap.Duration("wait_duration_total", st.TotalWaitDuration),
	)
	c.config.Logger.Info(msg, fields...)
}

func parseTLV(payload []byte) (map[uint16][]byte, error) {
	items, err := protocol.DecodeTLVs(payload)
	if err != nil {
		return nil, err
	}
	m := make(map[uint16][]byte, len(items))
	for _, it := range items {
		m[it.Type] = it.Value
	}
	return m, nil
}

func writeErrorFrame(ctx context.Context, conn transport.Conn, seq uint32, sid uint64, code atperr.Code, reason string) error {
	cb := make([]byte, 2)
	binaryPutUint16(cb, uint16(code))
	p, err := protocol.EncodeTLVs([]protocol.TLV{{Type: TLVErrorCode, Value: cb}, {Type: TLVErrorReason, Value: []byte(reason)}})
	if err != nil {
		return err
	}
	return conn.WriteFrame(ctx, &protocol.Frame{Header: protocol.Header{Magic: protocol.Magic, Version: protocol.VersionV1, Type: protocol.TypeError, SessionID: sid, Seq: seq}, Payload: p})
}

func mapErrorFrame(f *protocol.Frame) error {
	m, err := parseTLV(f.Payload)
	if err != nil {
		return err
	}
	if len(m[TLVErrorCode]) == 2 {
		code := atperr.Code(binaryUint16(m[TLVErrorCode]))
		return &atperr.Error{Code: code, Reason: string(m[TLVErrorReason])}
	}
	return atperrFrame(atperr.CodeInternal, string(m[TLVErrorReason]))
}

func atperrFrame(code atperr.Code, reason string) error {
	return &atperr.Error{Code: code, Reason: reason}
}

func newTraceID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "trace-fallback"
	}
	return hex.EncodeToString(b)
}

func binaryPutUint64(dst []byte, v uint64) {
	dst[0] = byte(v >> 56)
	dst[1] = byte(v >> 48)
	dst[2] = byte(v >> 40)
	dst[3] = byte(v >> 32)
	dst[4] = byte(v >> 24)
	dst[5] = byte(v >> 16)
	dst[6] = byte(v >> 8)
	dst[7] = byte(v)
}

func binaryPutUint16(dst []byte, v uint16) {
	dst[0] = byte(v >> 8)
	dst[1] = byte(v)
}

func binaryUint64(src []byte) uint64 {
	return uint64(src[0])<<56 | uint64(src[1])<<48 | uint64(src[2])<<40 | uint64(src[3])<<32 | uint64(src[4])<<24 | uint64(src[5])<<16 | uint64(src[6])<<8 | uint64(src[7])
}

func binaryUint16(src []byte) uint16 {
	return uint16(src[0])<<8 | uint16(src[1])
}
