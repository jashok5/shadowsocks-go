package runtime

import "sync"

const (
	defaultUDPPacketBufSize = 64 * 1024
	defaultRelayBufSize     = 32 * 1024
)

var (
	sharedUDPPacketBufPool = sync.Pool{
		New: func() any {
			return make([]byte, defaultUDPPacketBufSize)
		},
	}
	sharedRelayBufPool = sync.Pool{
		New: func() any {
			return make([]byte, defaultRelayBufSize)
		},
	}
)

func acquireUDPPacketBuf(size int) []byte {
	if size <= 0 {
		size = defaultUDPPacketBufSize
	}
	b := sharedUDPPacketBufPool.Get().([]byte)
	if cap(b) < size {
		return make([]byte, size)
	}
	return b[:size]
}

func releaseUDPPacketBuf(buf []byte) {
	if cap(buf) != defaultUDPPacketBufSize {
		return
	}
	sharedUDPPacketBufPool.Put(buf[:defaultUDPPacketBufSize])
}

func acquireRelayBuf() []byte {
	return sharedRelayBufPool.Get().([]byte)
}

func releaseRelayBuf(buf []byte) {
	if cap(buf) < defaultRelayBufSize {
		return
	}
	sharedRelayBufPool.Put(buf[:defaultRelayBufSize])
}
