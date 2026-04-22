package proxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/jashok5/shadowsocks-go/internal/atp/atperr"
	"github.com/jashok5/shadowsocks-go/internal/atp/audit"
	"github.com/jashok5/shadowsocks-go/internal/atp/concurrency"
	"github.com/jashok5/shadowsocks-go/internal/atp/limiter"
	"github.com/jashok5/shadowsocks-go/internal/atp/protocol"
	"github.com/jashok5/shadowsocks-go/internal/atp/recovery"
	"github.com/jashok5/shadowsocks-go/internal/atp/state"
	"github.com/jashok5/shadowsocks-go/internal/atp/transport"
)

const (
	tlvAuthToken    uint16 = 0x0003
	tlvAuthPassword uint16 = 0x0009
	tlvResumeTicket uint16 = 0x000A
	tlvResumeReq    uint16 = 0x000B
	tlvResumeAccept uint16 = 0x000C
	tlvClientNonce  uint16 = 0x0002
	tlvSessionID    uint16 = 0x0005
	tlvServerNonce  uint16 = 0x0006
	tlvStatus       uint16 = 0x0004
	tlvErrorCode    uint16 = 0x0007
	tlvErrorReason  uint16 = 0x0008
	tlvCoverMode    uint16 = 0x0201
	tlvCoverPadding uint16 = 0x0202
	tlvCoverTS      uint16 = 0x0203
	tlvCoverRandom  uint16 = 0x0204
	tlvCoverToken   uint16 = 0x0205
)

type Config struct {
	Listen           string
	Port             int
	Transport        string
	HandshakeTimeout time.Duration
	IdleTimeout      time.Duration
	HeartbeatTimeout time.Duration
	MaxOpenStreams   int
	TLSConfig        *tls.Config
	Reporter         UsageReporter
	Limiter          *limiter.Manager
	ConnManager      *concurrency.Manager
	AuditEngine      *audit.Engine
	EnableAuditBlock bool
	ResumeTicketTTL  time.Duration
}

type resumeUserEntry struct {
	user      state.UserPolicy
	expiresAt time.Time
}

type UsageReporter interface {
	AddUpload(userID int32, n int)
	AddDownload(userID int32, n int)
	AddAliveIP(userID int32, ip string)
	AddDetectLog(userID, listID int32)
}

type Server struct {
	cfg    Config
	logger *zap.Logger
	store  *state.Store

	recovery   *recovery.Manager
	resumeTTL  time.Duration
	resumeMu   sync.Mutex
	resumeUser map[uint64]resumeUserEntry

	listeners []transport.Listener
	closed    atomic.Bool

	sessionMu    sync.Mutex
	userSessions map[int32]map[uint64]sessionControl
}

type sessionControl struct {
	cancel context.CancelFunc
	conn   transport.Conn
}

func New(cfg Config, logger *zap.Logger, store *state.Store) (*Server, error) {
	if cfg.TLSConfig == nil {
		return nil, fmt.Errorf("tls config is required")
	}
	if cfg.HandshakeTimeout <= 0 {
		cfg.HandshakeTimeout = 10 * time.Second
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 120 * time.Second
	}
	if cfg.HeartbeatTimeout <= 0 {
		cfg.HeartbeatTimeout = 5 * time.Second
	}
	if cfg.MaxOpenStreams <= 0 {
		cfg.MaxOpenStreams = 256
	}
	if cfg.ResumeTicketTTL <= 0 {
		cfg.ResumeTicketTTL = 15 * time.Minute
	}
	if cfg.Transport == "" {
		cfg.Transport = "tls"
	}
	if cfg.Transport != "tls" && cfg.Transport != "quic" {
		return nil, fmt.Errorf("unsupported transport: %s", cfg.Transport)
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	if store == nil {
		return nil, fmt.Errorf("state store is nil")
	}
	rm, err := recovery.NewManager(recovery.Config{TTL: cfg.ResumeTicketTTL})
	if err != nil {
		return nil, fmt.Errorf("init resume manager: %w", err)
	}
	return &Server{cfg: cfg, logger: logger, store: store, recovery: rm, resumeTTL: cfg.ResumeTicketTTL, resumeUser: make(map[uint64]resumeUserEntry), userSessions: make(map[int32]map[uint64]sessionControl)}, nil
}

func (s *Server) Start(ctx context.Context) error {
	addr := net.JoinHostPort(s.cfg.Listen, strconv.Itoa(s.cfg.Port))
	networks := make([]string, 0, 2)
	if strings.EqualFold(strings.TrimSpace(s.cfg.Transport), "quic") {
		networks = append(networks, "tcp", "quic")
	} else {
		networks = append(networks, "tcp")
	}
	for _, network := range networks {
		ln, err := transport.Listen(network, addr, s.cfg.TLSConfig)
		if err != nil {
			_ = s.closeListeners()
			return err
		}
		s.listeners = append(s.listeners, ln)
		s.logger.Info("atp proxy listener started", zap.String("addr", ln.Addr()), zap.String("transport", network))
	}

	errCh := make(chan error, len(s.listeners))
	for _, ln := range s.listeners {
		listener := ln
		go func() {
			for {
				conn, acceptErr := listener.Accept(ctx)
				if acceptErr != nil {
					if ctx.Err() != nil || s.closed.Load() {
						errCh <- nil
						return
					}
					if isRetriableAcceptErr(acceptErr) {
						s.logger.Warn("atp listener accept temporary error, retrying",
							zap.String("addr", listener.Addr()),
							zap.Error(acceptErr),
						)
						select {
						case <-ctx.Done():
							errCh <- nil
							return
						case <-time.After(200 * time.Millisecond):
						}
						continue
					}
					errCh <- acceptErr
					return
				}
				go s.serveConn(ctx, conn)
			}
		}()
	}

	for i := 0; i < cap(errCh); i++ {
		if err := <-errCh; err != nil {
			_ = s.Close()
			return err
		}
	}
	return nil
}

func (s *Server) Close() error {
	s.closed.Store(true)
	return s.closeListeners()
}

func (s *Server) closeListeners() error {
	var closeErr error
	for _, ln := range s.listeners {
		if ln == nil {
			continue
		}
		if err := ln.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	s.listeners = nil
	return closeErr
}

func (s *Server) KickUsers(userIDs []int32) int {
	if len(userIDs) == 0 {
		return 0
	}
	controls := make([]sessionControl, 0, len(userIDs))
	s.sessionMu.Lock()
	for _, uid := range userIDs {
		sessions, ok := s.userSessions[uid]
		if !ok {
			continue
		}
		for sid, ctl := range sessions {
			controls = append(controls, ctl)
			delete(sessions, sid)
		}
		delete(s.userSessions, uid)
	}
	s.sessionMu.Unlock()

	for _, ctl := range controls {
		if ctl.cancel != nil {
			ctl.cancel()
		}
		if ctl.conn != nil {
			_ = ctl.conn.Close()
		}
	}
	return len(controls)
}

func (s *Server) registerSession(userID int32, sessionID uint64, cancel context.CancelFunc, conn transport.Conn) {
	if userID <= 0 || sessionID == 0 {
		return
	}
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	if _, ok := s.userSessions[userID]; !ok {
		s.userSessions[userID] = make(map[uint64]sessionControl)
	}
	s.userSessions[userID][sessionID] = sessionControl{cancel: cancel, conn: conn}
}

func (s *Server) unregisterSession(userID int32, sessionID uint64) {
	if userID <= 0 || sessionID == 0 {
		return
	}
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	sessions, ok := s.userSessions[userID]
	if !ok {
		return
	}
	delete(sessions, sessionID)
	if len(sessions) == 0 {
		delete(s.userSessions, userID)
	}
}

func (s *Server) serveConn(parent context.Context, conn transport.Conn) {
	defer conn.Close()
	alpn := conn.NegotiatedALPN()
	if alpn == "" {
		alpn = "unknown"
	}

	hCtx, cancel := context.WithTimeout(parent, s.cfg.HandshakeTimeout)
	defer cancel()

	sessionID, user, err := s.serverHandshake(hCtx, conn)
	if err != nil {
		s.logger.Warn("handshake failed", zap.Error(err), zap.String("remote", conn.RemoteAddr()), zap.String("local", conn.LocalAddr()), zap.String("alpn", alpn), zap.String("transport", conn.Network()))
		return
	}

	s.logger.Info("handshake success", zap.Uint64("session_id", sessionID), zap.Int32("user_id", user.UserID), zap.String("remote", conn.RemoteAddr()), zap.String("local", conn.LocalAddr()), zap.String("alpn", alpn), zap.String("transport", conn.Network()))
	sessionCtx, sessionCancel := context.WithCancel(parent)
	defer sessionCancel()
	s.registerSession(user.UserID, sessionID, sessionCancel, conn)
	defer s.unregisterSession(user.UserID, sessionID)
	if s.cfg.ConnManager != nil {
		if ok := s.cfg.ConnManager.Enter(user.UserID); !ok {
			_ = (&sessionWriter{conn: conn, sessionID: sessionID}).writeError(0, atperr.CodeAuthFailed, "too many connections")
			return
		}
		defer s.cfg.ConnManager.Leave(user.UserID)
	}
	if s.cfg.Reporter != nil {
		host, _, splitErr := net.SplitHostPort(conn.RemoteAddr())
		if splitErr == nil {
			s.cfg.Reporter.AddAliveIP(user.UserID, host)
		}
	}
	w := &sessionWriter{conn: conn, sessionID: sessionID}
	bridges := make(map[uint32]streamBridge)
	var mu sync.Mutex
	awaitingPong := false
	var pingNonce []byte

	cleanup := func() {
		mu.Lock()
		for _, b := range bridges {
			b.close()
		}
		mu.Unlock()
	}
	defer cleanup()

	for {
		readTimeout := s.cfg.IdleTimeout
		if awaitingPong {
			readTimeout = s.cfg.HeartbeatTimeout
		}
		rCtx, cancelRead := context.WithTimeout(sessionCtx, readTimeout)
		frame, readErr := conn.ReadFrame(rCtx)
		cancelRead()
		if readErr != nil {
			if errors.Is(readErr, context.DeadlineExceeded) {
				if !awaitingPong {
					nonce := make([]byte, 8)
					if _, err := rand.Read(nonce); err == nil {
						if writeErr := w.write(0, protocol.TypePing, nonce); writeErr == nil {
							awaitingPong = true
							pingNonce = nonce
							continue
						}
					}
				}
				_ = w.writeError(0, atperr.CodeTimeout, "idle timeout")
			}
			if isNetClosed(readErr) || errors.Is(readErr, io.EOF) {
				return
			}
			return
		}
		if awaitingPong {
			if frame.Header.Type == protocol.TypePing && bytes.Equal(frame.Payload, pingNonce) {
				awaitingPong = false
				pingNonce = nil
				continue
			}
			awaitingPong = false
			pingNonce = nil
		}
		if frame.Header.SessionID != sessionID {
			_ = w.writeError(frame.Header.Seq, atperr.CodeInvalidFrame, "session mismatch")
			return
		}

		switch frame.Header.Type {
		case protocol.TypeOpenStream:
			mu.Lock()
			openStreams := len(bridges)
			mu.Unlock()
			if openStreams >= s.cfg.MaxOpenStreams {
				_ = w.writeError(frame.Header.Seq, atperr.CodeFlowControl, "too many open streams")
				continue
			}
			req, decodeErr := protocol.DecodeOpenStreamPayload(frame.Payload)
			if decodeErr != nil {
				_ = w.writeError(frame.Header.Seq, atperr.CodeInvalidFrame, decodeErr.Error())
				return
			}
			if s.cfg.AuditEngine != nil {
				protoName := "tcp"
				if req.Network == protocol.NetworkUDP {
					protoName = "udp"
				}
				auditHost := req.Host
				if req.Domain != "" {
					auditHost = req.Domain
				}
				hits := s.cfg.AuditEngine.Match(audit.Target{Host: auditHost, Protocol: protoName})
				if len(hits) > 0 {
					if s.cfg.Reporter != nil {
						for _, listID := range hits {
							s.cfg.Reporter.AddDetectLog(user.UserID, listID)
						}
					}
					if s.cfg.EnableAuditBlock {
						_ = w.writeError(frame.Header.Seq, atperr.CodeBadRequest, "blocked by detect rules")
						continue
					}
				}
			}
			var bridge streamBridge
			switch req.Network {
			case protocol.NetworkTCP:
				target, dialErr := net.DialTimeout("tcp", net.JoinHostPort(req.Host, strconv.Itoa(int(req.Port))), 10*time.Second)
				if dialErr != nil {
					_ = w.writeError(frame.Header.Seq, atperr.CodeInternal, "dial target failed")
					continue
				}
				bridge = newTCPBridge(frame.Header.StreamID, user.UserID, target, w, s.logger, s.cfg.Reporter, s.cfg.Limiter)
				go bridge.copyToClient()
			case protocol.NetworkUDP:
				udpBridge, bridgeErr := newUDPBridge(frame.Header.StreamID, user.UserID, req.Host, req.Port, w, s.logger, s.cfg.Reporter, s.cfg.Limiter)
				if bridgeErr != nil {
					_ = w.writeError(frame.Header.Seq, atperr.CodeInternal, "dial udp target failed")
					continue
				}
				bridge = udpBridge
				go udpBridge.copyToClient()
			default:
				_ = w.writeError(frame.Header.Seq, atperr.CodeBadRequest, "unsupported network")
				continue
			}
			mu.Lock()
			bridges[frame.Header.StreamID] = bridge
			mu.Unlock()
		case protocol.TypeData, protocol.TypeDatagram:
			mu.Lock()
			bridge := bridges[frame.Header.StreamID]
			mu.Unlock()
			if bridge == nil {
				continue
			}
			if writeErr := bridge.writeFromClient(frame.Header.Type, frame.Payload); writeErr != nil {
				bridge.close()
				mu.Lock()
				delete(bridges, frame.Header.StreamID)
				mu.Unlock()
				continue
			}
			if s.cfg.Limiter != nil {
				lctx, lcancel := limiter.ClampCtx(parent, len(frame.Payload))
				if limErr := s.cfg.Limiter.WaitN(lctx, user.UserID, len(frame.Payload)); limErr != nil {
					lcancel()
					bridge.close()
					mu.Lock()
					delete(bridges, frame.Header.StreamID)
					mu.Unlock()
					continue
				}
				lcancel()
			}
			if s.cfg.Reporter != nil {
				s.cfg.Reporter.AddUpload(user.UserID, len(frame.Payload))
			}
		case protocol.TypeCloseStream:
			mu.Lock()
			bridge := bridges[frame.Header.StreamID]
			delete(bridges, frame.Header.StreamID)
			mu.Unlock()
			if bridge != nil {
				bridge.close()
			}
		case protocol.TypePing:
			_ = w.write(frame.Header.StreamID, protocol.TypePing, frame.Payload)
		case protocol.TypeCloseSess:
			return
		case protocol.TypeError:
			return
		}
	}
}

func (s *Server) serverHandshake(ctx context.Context, conn transport.Conn) (uint64, state.UserPolicy, error) {
	hello, err := conn.ReadFrame(ctx)
	if err != nil {
		return 0, state.UserPolicy{}, err
	}
	if hello.Header.Type != protocol.TypeHello {
		return 0, state.UserPolicy{}, fmt.Errorf("expected hello")
	}
	helloMap, err := parseTLVMap(hello.Payload)
	if err != nil {
		return 0, state.UserPolicy{}, err
	}
	if len(helloMap[tlvCoverToken]) == 0 {
		return 0, state.UserPolicy{}, fmt.Errorf("missing cover envelope")
	}
	helloMap = normalizeHelloCoverTLVMap(helloMap)
	if len(helloMap[tlvClientNonce]) == 0 {
		return 0, state.UserPolicy{}, fmt.Errorf("missing client nonce")
	}

	binding := resumeBinding(conn.RemoteAddr())
	resumed := false
	var (
		sid  uint64
		user state.UserPolicy
	)
	if raw := helloMap[tlvResumeReq]; len(raw) > 0 {
		s.logResumeEvent("attempt", binding, 0, "")
		resumedSID, resumeErr := s.recovery.Consume(string(raw), binding)
		if resumeErr == nil {
			if resumeUser, ok := s.loadResumeUser(resumedSID); ok {
				sid = resumedSID
				user = resumeUser
				resumed = true
				s.logResumeEvent("accepted", binding, resumedSID, "")
			} else {
				s.logResumeEvent("rejected", binding, resumedSID, "session_user_not_found")
			}
		} else {
			s.logResumeEvent("rejected", binding, resumedSID, resumeErr.Error())
		}
	}
	if sid == 0 {
		sid, err = randomSessionID()
		if err != nil {
			return 0, state.UserPolicy{}, err
		}
	}
	sidBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(sidBytes, sid)
	resumeFlag := byte(0)
	if resumed {
		resumeFlag = 1
	}
	coverHello, err := protocol.EncodeTLVs([]protocol.TLV{
		{Type: tlvSessionID, Value: sidBytes},
		{Type: tlvServerNonce, Value: []byte("atp-server")},
		{Type: tlvResumeAccept, Value: []byte{resumeFlag}},
	})
	if err != nil {
		return 0, state.UserPolicy{}, err
	}
	helloPayload, err := buildCoverEnvelopeTLV(coverModeByTransport(s.cfg.Transport), coverHello)
	if err != nil {
		return 0, state.UserPolicy{}, err
	}
	if err = (&sessionWriter{conn: conn, sessionID: sid}).write(0, protocol.TypeHello, helloPayload); err != nil {
		return 0, state.UserPolicy{}, err
	}

	if !resumed {
		auth, authErr := conn.ReadFrame(ctx)
		if authErr != nil {
			return 0, state.UserPolicy{}, authErr
		}
		if auth.Header.Type != protocol.TypeAuth || auth.Header.SessionID != sid {
			return 0, state.UserPolicy{}, fmt.Errorf("expected auth")
		}
		authMap, authErr := parseTLVMap(auth.Payload)
		if authErr != nil {
			return 0, state.UserPolicy{}, authErr
		}
		if len(authMap[tlvCoverToken]) == 0 {
			return 0, state.UserPolicy{}, fmt.Errorf("missing auth cover envelope")
		}
		authMap = normalizeAuthCoverTLVMap(authMap)
		token := string(authMap[tlvAuthToken])
		password := string(authMap[tlvAuthPassword])
		if password == "" {
			_ = (&sessionWriter{conn: conn, sessionID: sid}).writeError(auth.Header.Seq, atperr.CodeAuthFailed, "missing auth password")
			return 0, state.UserPolicy{}, fmt.Errorf("missing auth password")
		}
		if token == "" {
			return 0, state.UserPolicy{}, fmt.Errorf("missing auth token")
		}

		snap := s.store.Load()
		nodeToken := extractNodeToken(snap.Node.Server)
		if nodeToken == "" {
			_ = (&sessionWriter{conn: conn, sessionID: sid}).writeError(auth.Header.Seq, atperr.CodeAuthFailed, "node token not configured")
			return 0, state.UserPolicy{}, fmt.Errorf("node token not configured")
		}
		if token != nodeToken {
			_ = (&sessionWriter{conn: conn, sessionID: sid}).writeError(auth.Header.Seq, atperr.CodeAuthFailed, "invalid node token")
			return 0, state.UserPolicy{}, fmt.Errorf("invalid node token")
		}

		users, ok := snap.UsersByPwd[password]
		if !ok || len(users) == 0 {
			_ = (&sessionWriter{conn: conn, sessionID: sid}).writeError(auth.Header.Seq, atperr.CodeAuthFailed, "auth failed")
			return 0, state.UserPolicy{}, fmt.Errorf("auth failed")
		}
		if len(users) > 1 {
			_ = (&sessionWriter{conn: conn, sessionID: sid}).writeError(auth.Header.Seq, atperr.CodeAuthFailed, "ambiguous auth password")
			return 0, state.UserPolicy{}, fmt.Errorf("ambiguous auth password")
		}
		user = users[0]
	}

	ticket, err := s.recovery.Issue(sid, binding)
	if err != nil {
		return 0, state.UserPolicy{}, err
	}
	coverStatus, err := protocol.EncodeTLVs([]protocol.TLV{{Type: tlvStatus, Value: []byte("ok")}, {Type: tlvResumeTicket, Value: []byte(ticket)}})
	if err != nil {
		return 0, state.UserPolicy{}, err
	}
	statusPayload, err := buildCoverEnvelopeTLV(coverModeByTransport(s.cfg.Transport), coverStatus)
	if err != nil {
		return 0, state.UserPolicy{}, err
	}
	if err = (&sessionWriter{conn: conn, sessionID: sid}).write(0, protocol.TypeAuth, statusPayload); err != nil {
		return 0, state.UserPolicy{}, err
	}
	s.rememberResumeUser(sid, user)
	s.logResumeEvent("issued", binding, sid, "")
	return sid, user, nil
}

func normalizeHelloCoverTLVMap(in map[uint16][]byte) map[uint16][]byte {
	raw := in[tlvCoverToken]
	if len(raw) == 0 {
		return in
	}
	decoded, err := parseTLVMap(raw)
	if err != nil {
		return in
	}
	if token := decoded[tlvAuthToken]; len(token) > 0 {
		in[tlvAuthToken] = token
	}
	if pass := decoded[tlvAuthPassword]; len(pass) > 0 {
		in[tlvAuthPassword] = pass
	}
	if resume := decoded[tlvResumeReq]; len(resume) > 0 {
		in[tlvResumeReq] = resume
	}
	return in
}

func normalizeAuthCoverTLVMap(in map[uint16][]byte) map[uint16][]byte {
	raw := in[tlvCoverToken]
	if len(raw) == 0 {
		return in
	}
	decoded, err := parseTLVMap(raw)
	if err != nil {
		return in
	}
	if token := decoded[tlvAuthToken]; len(token) > 0 {
		in[tlvAuthToken] = token
	}
	if pass := decoded[tlvAuthPassword]; len(pass) > 0 {
		in[tlvAuthPassword] = pass
	}
	return in
}

func buildCoverEnvelopeTLV(mode string, payload []byte) ([]byte, error) {
	ts := make([]byte, 8)
	binary.BigEndian.PutUint64(ts, uint64(time.Now().Unix()))
	randBytes := make([]byte, 16)
	if _, err := rand.Read(randBytes); err != nil {
		return nil, err
	}
	padding := make([]byte, 32)
	if _, err := rand.Read(padding); err != nil {
		return nil, err
	}
	return protocol.EncodeTLVs([]protocol.TLV{
		{Type: tlvCoverMode, Value: []byte(mode)},
		{Type: tlvCoverTS, Value: ts},
		{Type: tlvCoverRandom, Value: randBytes},
		{Type: tlvCoverPadding, Value: padding},
		{Type: tlvCoverToken, Value: payload},
	})
}

func coverModeByTransport(transport string) string {
	if strings.EqualFold(strings.TrimSpace(transport), "quic") {
		return "h3"
	}
	return "h2"
}

func (s *Server) rememberResumeUser(sessionID uint64, user state.UserPolicy) {
	if sessionID == 0 || user.UserID <= 0 {
		return
	}
	now := time.Now()
	s.resumeMu.Lock()
	for sid, entry := range s.resumeUser {
		if now.After(entry.expiresAt) {
			delete(s.resumeUser, sid)
		}
	}
	s.resumeUser[sessionID] = resumeUserEntry{user: user, expiresAt: now.Add(s.resumeTTL)}
	s.resumeMu.Unlock()
}

func (s *Server) loadResumeUser(sessionID uint64) (state.UserPolicy, bool) {
	if sessionID == 0 {
		return state.UserPolicy{}, false
	}
	now := time.Now()
	s.resumeMu.Lock()
	defer s.resumeMu.Unlock()
	entry, ok := s.resumeUser[sessionID]
	if !ok {
		return state.UserPolicy{}, false
	}
	if now.After(entry.expiresAt) {
		delete(s.resumeUser, sessionID)
		return state.UserPolicy{}, false
	}
	return entry.user, true
}

type sessionWriter struct {
	mu        sync.Mutex
	conn      transport.Conn
	sessionID uint64
	seq       atomic.Uint32
}

func (w *sessionWriter) write(streamID uint32, frameType uint8, payload []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	frame := &protocol.Frame{
		Header: protocol.Header{
			Magic:     protocol.Magic,
			Version:   protocol.VersionV1,
			Type:      frameType,
			SessionID: w.sessionID,
			StreamID:  streamID,
			Seq:       w.seq.Add(1),
		},
		Payload: payload,
	}
	return w.conn.WriteFrame(context.Background(), frame)
}

func (w *sessionWriter) writeError(seq uint32, code atperr.Code, reason string) error {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, uint16(code))
	payload, err := protocol.EncodeTLVs([]protocol.TLV{{Type: tlvErrorCode, Value: b}, {Type: tlvErrorReason, Value: []byte(reason)}})
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.conn.WriteFrame(context.Background(), &protocol.Frame{Header: protocol.Header{Magic: protocol.Magic, Version: protocol.VersionV1, Type: protocol.TypeError, SessionID: w.sessionID, Seq: seq}, Payload: payload})
}

type tcpBridge struct {
	streamID uint32
	userID   int32
	target   net.Conn
	writer   *sessionWriter
	logger   *zap.Logger
	reporter UsageReporter
	limiter  *limiter.Manager
	closed   atomic.Bool
}

type streamBridge interface {
	copyToClient()
	writeFromClient(frameType uint8, payload []byte) error
	close()
}

func newTCPBridge(streamID uint32, userID int32, target net.Conn, writer *sessionWriter, logger *zap.Logger, reporter UsageReporter, lm *limiter.Manager) *tcpBridge {
	return &tcpBridge{streamID: streamID, userID: userID, target: target, writer: writer, logger: logger, reporter: reporter, limiter: lm}
}

func (b *tcpBridge) copyToClient() {
	buf := make([]byte, 16*1024)
	for {
		n, err := b.target.Read(buf)
		if n > 0 {
			if b.limiter != nil {
				ctx, cancel := limiter.ClampCtx(context.Background(), n)
				if err := b.limiter.WaitN(ctx, b.userID, n); err != nil {
					cancel()
					b.close()
					return
				}
				cancel()
			}
			cp := append([]byte(nil), buf[:n]...)
			if writeErr := b.writer.write(b.streamID, protocol.TypeData, cp); writeErr != nil {
				b.close()
				return
			}
			if b.reporter != nil {
				b.reporter.AddDownload(b.userID, n)
			}
		}
		if err != nil {
			if !isNetClosed(err) && !errors.Is(err, io.EOF) {
				b.logger.Debug("bridge read error", zap.Error(err), zap.Uint32("stream_id", b.streamID))
			}
			_ = b.writer.write(b.streamID, protocol.TypeCloseStream, nil)
			b.close()
			return
		}
	}
}

func (b *tcpBridge) writeFromClient(frameType uint8, payload []byte) error {
	if frameType != protocol.TypeData {
		return fmt.Errorf("invalid frame type for tcp stream: %d", frameType)
	}
	_, err := b.target.Write(payload)
	return err
}

func (b *tcpBridge) close() {
	if b.closed.Swap(true) {
		return
	}
	_ = b.target.Close()
}

type udpBridge struct {
	streamID uint32
	userID   int32
	target   net.PacketConn
	remote   net.Addr
	writer   *sessionWriter
	logger   *zap.Logger
	reporter UsageReporter
	limiter  *limiter.Manager
	closed   atomic.Bool
}

func newUDPBridge(streamID uint32, userID int32, host string, port uint16, writer *sessionWriter, logger *zap.Logger, reporter UsageReporter, lm *limiter.Manager) (*udpBridge, error) {
	remote, err := net.ResolveUDPAddr("udp", net.JoinHostPort(host, strconv.Itoa(int(port))))
	if err != nil {
		return nil, err
	}
	pc, err := net.ListenPacket("udp", "")
	if err != nil {
		return nil, err
	}
	return &udpBridge{streamID: streamID, userID: userID, target: pc, remote: remote, writer: writer, logger: logger, reporter: reporter, limiter: lm}, nil
}

func (b *udpBridge) copyToClient() {
	buf := make([]byte, 64*1024)
	for {
		n, _, err := b.target.ReadFrom(buf)
		if n > 0 {
			if b.limiter != nil {
				ctx, cancel := limiter.ClampCtx(context.Background(), n)
				if err := b.limiter.WaitN(ctx, b.userID, n); err != nil {
					cancel()
					b.close()
					return
				}
				cancel()
			}
			cp := append([]byte(nil), buf[:n]...)
			if writeErr := b.writer.write(b.streamID, protocol.TypeDatagram, cp); writeErr != nil {
				b.close()
				return
			}
			if b.reporter != nil {
				b.reporter.AddDownload(b.userID, n)
			}
		}
		if err != nil {
			if !isNetClosed(err) && !errors.Is(err, io.EOF) {
				b.logger.Debug("udp bridge read error", zap.Error(err), zap.Uint32("stream_id", b.streamID))
			}
			_ = b.writer.write(b.streamID, protocol.TypeCloseStream, nil)
			b.close()
			return
		}
	}
}

func (b *udpBridge) writeFromClient(frameType uint8, payload []byte) error {
	if frameType != protocol.TypeDatagram && frameType != protocol.TypeData {
		return fmt.Errorf("invalid frame type for udp stream: %d", frameType)
	}
	_, err := b.target.WriteTo(payload, b.remote)
	return err
}

func (b *udpBridge) close() {
	if b.closed.Swap(true) {
		return
	}
	_ = b.target.Close()
}

func parseTLVMap(payload []byte) (map[uint16][]byte, error) {
	items, err := protocol.DecodeTLVs(payload)
	if err != nil {
		return nil, err
	}
	out := make(map[uint16][]byte, len(items))
	for _, item := range items {
		out[item.Type] = item.Value
	}
	return out, nil
}

func transportNetwork(t string) string {
	if strings.EqualFold(t, "quic") {
		return "quic"
	}
	return "tcp"
}

func randomSessionID() (uint64, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(b), nil
}

func isNetClosed(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, net.ErrClosed) {
		return true
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "use of closed network connection")
}

func isRetriableAcceptErr(err error) bool {
	if err == nil {
		return false
	}
	if ne, ok := err.(net.Error); ok {
		if ne.Timeout() {
			return true
		}
		type temporary interface{ Temporary() bool }
		if te, ok := any(ne).(temporary); ok && te.Temporary() {
			return true
		}
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "temporary") || strings.Contains(msg, "resource temporarily unavailable")
}

func extractNodeToken(server string) string {
	raw := strings.TrimSpace(server)
	if raw == "" {
		return ""
	}
	parts := strings.SplitN(raw, ";", 2)
	if len(parts) < 2 {
		return ""
	}
	for _, pair := range strings.Split(parts[1], "|") {
		kv := strings.SplitN(strings.TrimSpace(pair), "=", 2)
		if len(kv) != 2 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(kv[0]), "token") {
			return strings.TrimSpace(kv[1])
		}
	}
	return ""
}

func resumeBinding(remote string) string {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return strings.TrimSpace(remote)
	}
	return strings.TrimSpace(host)
}

func (s *Server) logResumeEvent(outcome string, binding string, sessionID uint64, reason string) {
	fields := []zap.Field{
		zap.String("event", "atp_session_resume"),
		zap.String("outcome", outcome),
		zap.String("binding", binding),
	}
	if sessionID != 0 {
		fields = append(fields, zap.Uint64("session_id", sessionID))
	}
	if reason != "" {
		fields = append(fields, zap.String("reason", reason))
	}
	s.logger.Info("atp session resume", fields...)
}
