package netx

import (
	"io"
	"net"
	"sync"
	"time"

	"github.com/jashok5/shadowsocks-go/internal/protocol/ssr/common/log"
	"github.com/jashok5/shadowsocks-go/internal/protocol/ssr/common/network"
	"github.com/jashok5/shadowsocks-go/internal/protocol/ssr/common/pool"
	"github.com/jashok5/shadowsocks-go/internal/protocol/ssr/utils/goroutine"
	"github.com/jashok5/shadowsocks-go/internal/protocol/ssr/utils/socksproxy"
	"github.com/pkg/errors"
)

func Copy(dst, src network.IRequest) (written int64, err error) {
	buf := pool.GetBuf()
	for {
		nr, er := src.Read(buf)
		//if nr == 0{
		//	log.Debug("%s n is zero ---------------------",dst.GetRequestId())
		//}
		if nr > 0 {
			nw, ew := dst.Write(buf[0:nr])
			if nw > 0 {
				written += int64(nw)
			}
			if ew != nil {
				err = ew
				break
			}
			if nr != nw {
				err = io.ErrShortWrite
				break
			}
		}
		if er != nil {
			err = er
			break
		}
	}
	pool.PutBuf(buf)
	return written, err
}

func DuplexCopyTcp(left, right network.IRequest) (up, down int64, err error) {
	type res struct {
		N   int64
		Err error
	}
	ch := make(chan res)
	defer func() {
		if e := recover(); e != nil {
			log.Error("panic in timedCopy: %v", e)
		}
	}()

	go goroutine.Protect(func() {
		n, err := Copy(right, left)
		_ = right.SetDeadline(time.Now()) // wake up the other goroutine blocking on right
		_ = left.SetDeadline(time.Now())  // wake up the other goroutine blocking on left
		ch <- res{n, err}
	})

	up, err = Copy(left, right)
	_ = right.SetDeadline(time.Now()) // wake up the other goroutine blocking on right
	_ = left.SetDeadline(time.Now())  // wake up the other goroutine blocking on left
	rs := <-ch

	if rs.Err != nil {
		log.Error("netx copy %s <- %s : %s", right.RemoteAddr(), left.RemoteAddr(), rs.Err.Error())
	}
	if err != nil {
		log.Error("netx copy %s -> %s : %s", right.RemoteAddr(), left.RemoteAddr(), err.Error())
	}
	return up, rs.N, errors.Cause(err)
}

type NatMap struct {
	sync.RWMutex
	m       map[string]net.PacketConn
	timeout time.Duration
}

func (m *NatMap) Get(key string) net.PacketConn {
	m.RLock()
	defer m.RUnlock()
	return m.m[key]
}

func (m *NatMap) Set(key string, pc net.PacketConn) {
	m.Lock()
	defer m.Unlock()
	m.m[key] = pc
}

func (m *NatMap) Del(key string) net.PacketConn {
	m.Lock()
	defer m.Unlock()

	pc, ok := m.m[key]
	if ok {
		delete(m.m, key)
		return pc
	}
	return nil
}

func (m *NatMap) Add(peer net.Addr, dst, src net.PacketConn) {
	m.Set(peer.String(), src)
	go goroutine.Protect(func() {
		_ = timedCopy(dst, peer, src, m.timeout)
		if pc := m.Del(peer.String()); pc != nil {
			_ = pc.Close()
		}
	})
}

// copy from src to dst at target with read timeout
func timedCopy(dst net.PacketConn, target net.Addr, src net.PacketConn, timeout time.Duration) error {
	buf := pool.GetBuf()
	defer pool.PutBuf(buf)
	defer func() {
		if e := recover(); e != nil {
			log.Error("panic in timedCopy: %v", e)
		}
	}()

	for {
		_ = src.SetReadDeadline(time.Now().Add(timeout))
		n, raddr, err := src.ReadFrom(buf)
		if err != nil {
			return errors.Cause(err)
		}

		srcAddr := socksproxy.ParseAddr(raddr.String())
		srcAddrByte := srcAddr.Raw
		copy(buf[len(srcAddrByte):], buf[:n])
		copy(buf, srcAddrByte)
		_, err = dst.WriteTo(buf[:len(srcAddrByte)+n], target)

		if err != nil {
			return errors.Cause(err)
		}
	}
}
