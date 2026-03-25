package runtime

import (
	"fmt"
	"net"

	vnetNetwork "github.com/jashok5/shadowsocks-go/internal/protocol/ssr/common/network"
	"github.com/jashok5/shadowsocks-go/internal/protocol/ssr/utils/socksproxy"
)

type udpResponseWriter interface {
	WriteResponse(payload []byte, source net.Addr) error
}

type ssUDPResponseWriter struct {
	packetConn net.PacketConn
	clientAddr net.Addr
	srcAddr    []byte
}

func newSSUDPResponseWriter(packetConn net.PacketConn, clientAddr net.Addr, srcAddr []byte) udpResponseWriter {
	return &ssUDPResponseWriter{packetConn: packetConn, clientAddr: clientAddr, srcAddr: append([]byte(nil), srcAddr...)}
}

func (w *ssUDPResponseWriter) WriteResponse(payload []byte, _ net.Addr) error {
	if w == nil || w.packetConn == nil || w.clientAddr == nil || len(w.srcAddr) == 0 {
		return fmt.Errorf("invalid ss udp response writer")
	}
	out := acquireUDPPacketBuf(len(w.srcAddr) + len(payload))
	copy(out, w.srcAddr)
	copy(out[len(w.srcAddr):], payload)
	_, err := w.packetConn.WriteTo(out[:len(w.srcAddr)+len(payload)], w.clientAddr)
	releaseUDPPacketBuf(out)
	return err
}

type ssrUDPResponseWriter struct {
	decorated  *vnetNetwork.ShadowsocksRDecorate
	clientAddr net.Addr
	uid        []byte
}

func newSSRUDPResponseWriter(decorated *vnetNetwork.ShadowsocksRDecorate, clientAddr net.Addr, uid []byte) udpResponseWriter {
	return &ssrUDPResponseWriter{decorated: decorated, clientAddr: clientAddr, uid: append([]byte(nil), uid...)}
}

func (w *ssrUDPResponseWriter) WriteResponse(payload []byte, source net.Addr) error {
	if w == nil || w.decorated == nil || w.clientAddr == nil || source == nil {
		return fmt.Errorf("invalid ssr udp response writer")
	}
	src := socksproxy.ParseAddr(source.String())
	if src == nil {
		return fmt.Errorf("invalid ssr source addr")
	}
	out := acquireUDPPacketBuf(len(src.Raw) + len(payload))
	copy(out, src.Raw)
	copy(out[len(src.Raw):], payload)
	err := w.decorated.WriteTo(out[:len(src.Raw)+len(payload)], append([]byte(nil), w.uid...), w.clientAddr)
	releaseUDPPacketBuf(out)
	return err
}
