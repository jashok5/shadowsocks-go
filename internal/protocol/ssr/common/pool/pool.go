package pool

import "sync"

const BufferSize = 4096

var (
	bufPool = sync.Pool{
		New: createAllocFunc(BufferSize),
	}
)

func GetBuf() []byte {
	buf := *(bufPool.Get().(*[]byte))
	buf = buf[:cap(buf)]
	return buf
}

func PutBuf(buf []byte) {
	if cap(buf) < BufferSize {
		return
	}
	b := buf[:BufferSize]
	bufPool.Put(&b)
}

func createAllocFunc(size int) func() any {
	return func() any {
		b := make([]byte, size)
		return &b
	}
}
