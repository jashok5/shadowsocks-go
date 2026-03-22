package goroutine

import (
	"runtime/debug"

	"github.com/sirupsen/logrus"
)

func Protect(g func()) {
	defer func() {
		if err := recover(); err != nil {
			logrus.Errorf("run time panic: %s stack: %s", err, string(debug.Stack()))
		}
	}()
	g()
}
