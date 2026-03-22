package pool

import "sync"

const BufferSize = 4096

var (
	poolMap    map[int]*sync.Pool
	getBufLock *sync.Mutex
)

func init() {
	poolMap = make(map[int]*sync.Pool)
	getBufLock = new(sync.Mutex)
}

func GetBuf() []byte {
	pool := poolMap[BufferSize]
	if pool == nil {
		getBufLock.Lock()
		poolMap[BufferSize] = &sync.Pool{
			New: createAllocFunc(BufferSize),
		}
		getBufLock.Unlock()
	}
	buf := poolMap[BufferSize].Get().([]byte)
	buf = buf[:cap(buf)]
	return buf
}

func PutBuf(buf []byte) {
	poolMap[cap(buf)].Put(buf)
}

func createAllocFunc(size int) func() any {
	return func() any {
		return make([]byte, size)
	}
}
