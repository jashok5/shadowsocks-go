package goroutine

import (
	"runtime/debug"

	"github.com/jashok5/shadowsocks-go/internal/protocol/ssr/common/log"
)

func Protect(g func()) {
	defer func() {
		if err := recover(); err != nil {
			log.Errorw("run time panic", "error", err, "stack", string(debug.Stack()))
		}
	}()
	g()
}
