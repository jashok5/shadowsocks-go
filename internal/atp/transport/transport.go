package transport

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"go.uber.org/zap"
	"golang.org/x/net/http2"

	"github.com/jashok5/shadowsocks-go/internal/atp/protocol"
)

type Conn interface {
	ReadFrame(ctx context.Context) (*protocol.Frame, error)
	WriteFrame(ctx context.Context, f *protocol.Frame) error
	Close() error
	RemoteAddr() string
	LocalAddr() string
	NegotiatedALPN() string
	Network() string
}

type Listener interface {
	Accept(ctx context.Context) (Conn, error)
	Close() error
	Addr() string
}

var camouflageLogger = zap.NewNop()

func SetLogger(logger *zap.Logger) {
	if logger == nil {
		camouflageLogger = zap.NewNop()
		return
	}
	camouflageLogger = logger
}

type tcpConn struct {
	conn              net.Conn
	reader            *bufio.Reader
	camouflageChecked bool
	camouflageDone    bool
	alpnHint          string
}

func (c *tcpConn) ReadFrame(ctx context.Context) (*protocol.Frame, error) {
	if c.camouflageDone {
		return nil, io.EOF
	}
	if err := applyConnDeadline(ctx, c.conn); err != nil {
		return nil, err
	}
	if err := c.ensureATPOrCamouflage(); err != nil {
		return nil, err
	}
	if c.reader != nil {
		return protocol.ReadFrame(c.reader)
	}
	return protocol.ReadFrame(c.conn)
}

func (c *tcpConn) WriteFrame(ctx context.Context, f *protocol.Frame) error {
	if err := applyConnDeadline(ctx, c.conn); err != nil {
		return err
	}
	return protocol.WriteFrame(c.conn, f)
}

func (c *tcpConn) Close() error { return c.conn.Close() }

func (c *tcpConn) RemoteAddr() string { return c.conn.RemoteAddr().String() }

func (c *tcpConn) LocalAddr() string { return c.conn.LocalAddr().String() }

func (c *tcpConn) NegotiatedALPN() string {
	alpn := strings.TrimSpace(strings.ToLower(negotiatedALPN(c.conn)))
	if alpn != "" {
		return alpn
	}
	return strings.TrimSpace(strings.ToLower(c.alpnHint))
}

func (c *tcpConn) Network() string { return "tcp" }

type tcpListener struct {
	ln net.Listener
}

func ListenTCP(addr string, tlsCfg *tls.Config) (Listener, error) {
	ln, err := tls.Listen("tcp", addr, tlsCfg)
	if err != nil {
		return nil, err
	}
	return &tcpListener{ln: ln}, nil
}

func DialTCP(ctx context.Context, addr string, tlsCfg *tls.Config) (Conn, error) {
	d := tls.Dialer{Config: tlsCfg}
	raw, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	return &tcpConn{conn: raw}, nil
}

func (l *tcpListener) Accept(ctx context.Context) (Conn, error) {
	type acceptResult struct {
		conn net.Conn
		err  error
	}
	ch := make(chan acceptResult, 1)
	go func() {
		c, err := l.ln.Accept()
		ch <- acceptResult{conn: c, err: err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		if r.err != nil {
			return nil, r.err
		}
		return &tcpConn{conn: r.conn, reader: bufio.NewReaderSize(r.conn, 4096)}, nil
	}
}

func (l *tcpListener) Close() error { return l.ln.Close() }

func (l *tcpListener) Addr() string { return l.ln.Addr().String() }

type quicConn struct {
	conn              *quic.Conn
	stream            *quic.Stream
	reader            *bufio.Reader
	alpn              string
	camouflageChecked bool
}

func (c *quicConn) ReadFrame(ctx context.Context) (*protocol.Frame, error) {
	if err := applyStreamDeadline(ctx, c.stream); err != nil {
		return nil, err
	}
	if err := c.ensureATPOrCamouflage(); err != nil {
		return nil, err
	}
	if c.reader != nil {
		return protocol.ReadFrame(c.reader)
	}
	return protocol.ReadFrame(c.stream)
}

func (c *quicConn) WriteFrame(ctx context.Context, f *protocol.Frame) error {
	if err := applyStreamDeadline(ctx, c.stream); err != nil {
		return err
	}
	return protocol.WriteFrame(c.stream, f)
}

func (c *quicConn) Close() error {
	_ = c.stream.Close()
	c.conn.CloseWithError(0, "closed")
	return nil
}

func (c *quicConn) RemoteAddr() string { return c.conn.RemoteAddr().String() }

func (c *quicConn) LocalAddr() string { return c.conn.LocalAddr().String() }

func (c *quicConn) NegotiatedALPN() string {
	if c.alpn != "" {
		return strings.TrimSpace(strings.ToLower(c.alpn))
	}
	return strings.TrimSpace(strings.ToLower(c.conn.ConnectionState().TLS.NegotiatedProtocol))
}

func (c *quicConn) Network() string { return "quic" }

type quicListener struct {
	ln *quic.Listener
}

func ListenQUIC(addr string, tlsCfg *tls.Config) (Listener, error) {
	packetConn, err := net.ListenPacket("udp", addr)
	if err != nil {
		return nil, err
	}
	if udpConn, ok := packetConn.(*net.UDPConn); ok {
		_ = udpConn.SetReadBuffer(8 * 1024 * 1024)
		_ = udpConn.SetWriteBuffer(8 * 1024 * 1024)
	}
	ln, err := quic.Listen(packetConn, tlsCfg, &quic.Config{})
	if err != nil {
		_ = packetConn.Close()
		return nil, err
	}
	return &quicListener{ln: ln}, nil
}

func DialQUIC(ctx context.Context, addr string, tlsCfg *tls.Config) (Conn, error) {
	qc, err := quic.DialAddr(ctx, addr, tlsCfg, &quic.Config{})
	if err != nil {
		return nil, err
	}
	st, err := qc.OpenStreamSync(ctx)
	if err != nil {
		qc.CloseWithError(1, "stream_open_failed")
		return nil, err
	}
	return &quicConn{conn: qc, stream: st}, nil
}

func (l *quicListener) Accept(ctx context.Context) (Conn, error) {
	for {
		qc, err := l.ln.Accept(ctx)
		if err != nil {
			return nil, err
		}
		alpn := strings.TrimSpace(strings.ToLower(qc.ConnectionState().TLS.NegotiatedProtocol))
		if strings.HasPrefix(alpn, "h3") {
			camouflageLogger.Info("quic camouflage accepted",
				zap.String("remote", qc.RemoteAddr().String()),
				zap.String("local", qc.LocalAddr().String()),
				zap.String("alpn", alpn),
			)
			go serveHTTP3ErrorJSONConn(qc)
			continue
		}
		st, err := qc.AcceptStream(ctx)
		if err != nil {
			qc.CloseWithError(1, "stream_accept_failed")
			return nil, err
		}
		return &quicConn{conn: qc, stream: st, reader: bufio.NewReaderSize(st, 4096), alpn: alpn}, nil
	}
}

func (l *quicListener) Close() error { return l.ln.Close() }

func (l *quicListener) Addr() string { return l.ln.Addr().String() }

func (c *tcpConn) ensureATPOrCamouflage() error {
	if c.camouflageChecked {
		return nil
	}
	c.camouflageChecked = true
	if c.reader == nil {
		return nil
	}
	isATP, proto, err := sniffATPClientData(c.reader, negotiatedALPN(c.conn))
	if err != nil {
		return err
	}
	if strings.TrimSpace(proto) != "" {
		c.alpnHint = strings.TrimSpace(strings.ToLower(proto))
	}
	if isATP {
		return nil
	}
	if err := writeHTTPErrorPage(c.conn, c.reader, proto); err != nil {
		return err
	}
	c.camouflageDone = true
	return io.EOF
}

func (c *quicConn) ensureATPOrCamouflage() error {
	if c.camouflageChecked {
		return nil
	}
	c.camouflageChecked = true
	if c.alpn == "h3" {
		_ = c.stream.Close()
		c.conn.CloseWithError(0, "http3_error_page")
		return io.EOF
	}
	if c.reader == nil {
		return nil
	}
	isATP, _, err := sniffATPClientData(c.reader, "")
	if err != nil {
		return err
	}
	if isATP {
		return nil
	}
	_ = writeQUICHTTPErrorPage(c.stream)
	_ = c.stream.Close()
	c.conn.CloseWithError(0, "http_error_page")
	return io.EOF
}

func sniffATPClientData(r *bufio.Reader, alpn string) (bool, string, error) {
	b, err := r.Peek(2)
	if err != nil {
		return false, "", err
	}
	if len(b) == 2 && bytes.Equal(b, []byte{'A', 'T'}) {
		return true, "", nil
	}

	alpn = strings.TrimSpace(strings.ToLower(alpn))
	if alpn == "h2" {
		return false, "h2", nil
	}
	if alpn == "http/1.1" {
		return false, "http/1.1", nil
	}
	prefix, pErr := r.Peek(24)
	if pErr != nil && !errors.Is(pErr, bufio.ErrBufferFull) {
		if !errors.Is(pErr, io.EOF) {
			return false, "", pErr
		}
	}
	s := string(prefix)
	if strings.HasPrefix(s, "PRI * HTTP/2.0") {
		return false, "h2", nil
	}
	if looksLikeHTTP1(prefix) {
		return false, "http/1.1", nil
	}
	return false, "http/1.1", nil
}

func negotiatedALPN(conn net.Conn) string {
	if conn == nil {
		return ""
	}
	if tlsConn, ok := conn.(*tls.Conn); ok {
		if err := tlsConn.Handshake(); err != nil {
			return ""
		}
		state := tlsConn.ConnectionState()
		return state.NegotiatedProtocol
	}
	return ""
}

func looksLikeHTTP1(b []byte) bool {
	s := string(b)
	methods := []string{"GET ", "POST ", "HEAD ", "PUT ", "DELETE ", "OPTIONS ", "PATCH ", "CONNECT ", "TRACE "}
	for _, method := range methods {
		if strings.HasPrefix(s, method) {
			return true
		}
	}
	return false
}

func writeHTTPErrorPage(raw net.Conn, reader *bufio.Reader, proto string) error {
	if raw == nil {
		return nil
	}
	if strings.EqualFold(proto, "h2") {
		return writeHTTP2ErrorJSON(raw, reader)
	}
	body := `{"code":403,"message":"\u672a\u6388\u6743\u8bbf\u95ee"}`
	server := &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       5 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Server", "nginx")
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Connection", "close")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(body))
		}),
	}
	server.SetKeepAlivesEnabled(false)
	conn := raw
	if reader != nil {
		conn = &bufferedNetConn{Conn: raw, reader: reader}
	}
	listener := &singleConnListener{conn: conn}
	err := server.Serve(listener)
	if err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.EOF) && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func writeHTTP2ErrorJSON(raw net.Conn, reader *bufio.Reader) error {
	conn := raw
	if reader != nil {
		conn = &bufferedNetConn{Conn: raw, reader: reader}
	}
	body := []byte(`{"code":403,"message":"\u672a\u6388\u6743\u8bbf\u95ee"}`)
	h2Server := &http2.Server{IdleTimeout: 5 * time.Second}
	closeOnce := sync.Once{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "nginx")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write(body)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		closeOnce.Do(func() {
			go func() {
				time.Sleep(80 * time.Millisecond)
				_ = conn.Close()
			}()
		})
	})

	done := make(chan struct{})
	go func() {
		h2Server.ServeConn(conn, &http2.ServeConnOpts{Handler: handler})
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-time.After(7 * time.Second):
		_ = conn.Close()
		<-done
		return nil
	}
}

func writeQUICHTTPErrorPage(stream *quic.Stream) error {
	if stream == nil {
		return nil
	}
	_ = stream.SetWriteDeadline(time.Now().Add(800 * time.Millisecond))
	_, err := stream.Write([]byte("atp rejected\n"))
	return err
}

func serveHTTP3ErrorJSONConn(conn *quic.Conn) {
	if conn == nil {
		return
	}
	camouflageLogger.Info("http3 camouflage connection handling",
		zap.String("remote", conn.RemoteAddr().String()),
		zap.String("local", conn.LocalAddr().String()),
	)
	body := []byte(`{"code":403,"message":"\u672a\u6388\u6743\u8bbf\u95ee"}`)
	h3Server := &http3.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			camouflageLogger.Info("http3 camouflage request served",
				zap.String("remote", r.RemoteAddr),
				zap.String("host", r.Host),
				zap.String("method", r.Method),
				zap.String("uri", r.URL.RequestURI()),
				zap.String("proto", r.Proto),
			)
			w.Header().Set("Server", "nginx")
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write(body)
		}),
		IdleTimeout: 3 * time.Second,
	}
	if err := h3Server.ServeQUICConn(conn); err != nil {
		camouflageLogger.Warn("http3 camouflage connection ended", zap.Error(err))
	}
}

type bufferedNetConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedNetConn) Read(p []byte) (int, error) {
	if c.reader == nil {
		return c.Conn.Read(p)
	}
	return c.reader.Read(p)
}

type singleConnListener struct {
	conn net.Conn
	once sync.Once
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	var out net.Conn
	l.once.Do(func() {
		out = l.conn
	})
	if out == nil {
		return nil, io.EOF
	}
	return out, nil
}

func (l *singleConnListener) Close() error {
	if l.conn != nil {
		return l.conn.Close()
	}
	return nil
}

func (l *singleConnListener) Addr() net.Addr {
	if l.conn != nil {
		return l.conn.LocalAddr()
	}
	return &net.TCPAddr{}
}

func applyConnDeadline(ctx context.Context, conn net.Conn) error {
	if ctx == nil {
		return conn.SetDeadline(time.Time{})
	}
	if d, ok := ctx.Deadline(); ok {
		return conn.SetDeadline(d)
	}
	return conn.SetDeadline(time.Time{})
}

func applyStreamDeadline(ctx context.Context, s *quic.Stream) error {
	if ctx == nil {
		if err := s.SetReadDeadline(time.Time{}); err != nil {
			return err
		}
		return s.SetWriteDeadline(time.Time{})
	}
	if d, ok := ctx.Deadline(); ok {
		if err := s.SetReadDeadline(d); err != nil {
			return err
		}
		return s.SetWriteDeadline(d)
	}
	if err := s.SetReadDeadline(time.Time{}); err != nil {
		return err
	}
	return s.SetWriteDeadline(time.Time{})
}

func Dial(ctx context.Context, network, addr string, tlsCfg *tls.Config) (Conn, error) {
	switch network {
	case "tcp":
		return DialTCP(ctx, addr, tlsCfg)
	case "quic":
		return DialQUIC(ctx, addr, tlsCfg)
	default:
		return nil, fmt.Errorf("unsupported transport: %s", network)
	}
}

func Listen(network, addr string, tlsCfg *tls.Config) (Listener, error) {
	switch network {
	case "tcp":
		return ListenTCP(addr, tlsCfg)
	case "quic":
		return ListenQUIC(addr, tlsCfg)
	default:
		return nil, fmt.Errorf("unsupported transport: %s", network)
	}
}
