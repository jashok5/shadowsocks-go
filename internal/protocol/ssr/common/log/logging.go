package log

import (
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	mu     sync.RWMutex
	logger = zap.NewNop()

	sampleMu   sync.Mutex
	sampleLast = make(map[string]time.Time)
)

func SetLogger(l *zap.Logger) {
	mu.Lock()
	defer mu.Unlock()
	if l == nil {
		logger = zap.NewNop()
		return
	}
	logger = l
}

func getLogger() *zap.Logger {
	mu.RLock()
	defer mu.RUnlock()
	return logger
}

func Debug(message string, params ...any) {
	if len(params) > 0 {
		getLogger().Debug(fmt.Sprintf(message, params...))
		return
	}
	getLogger().Debug(message)
}

func Info(message string, params ...any) {
	if len(params) > 0 {
		getLogger().Info(fmt.Sprintf(message, params...))
		return
	}
	getLogger().Info(message)
}

func Warn(message string, params ...any) {
	if len(params) > 0 {
		getLogger().Warn(fmt.Sprintf(message, params...))
		return
	}
	getLogger().Warn(message)
}

func Error(message string, params ...any) {
	if len(params) > 0 {
		getLogger().Error(fmt.Sprintf(message, params...))
		return
	}
	getLogger().Error(message)
}

func DebugEnabled() bool {
	return getLogger().Core().Enabled(zapcore.DebugLevel)
}

func Debugw(message string, keysAndValues ...any) {
	getLogger().Sugar().Debugw(message, keysAndValues...)
}

func Infow(message string, keysAndValues ...any) {
	getLogger().Sugar().Infow(message, keysAndValues...)
}

func Warnw(message string, keysAndValues ...any) {
	getLogger().Sugar().Warnw(message, keysAndValues...)
}

func Errorw(message string, keysAndValues ...any) {
	getLogger().Sugar().Errorw(message, keysAndValues...)
}

func DebugwSampled(sampleKey string, interval time.Duration, message string, keysAndValues ...any) {
	if !shouldSample(sampleKey, interval) {
		return
	}
	Debugw(message, keysAndValues...)
}

func InfowSampled(sampleKey string, interval time.Duration, message string, keysAndValues ...any) {
	if !shouldSample(sampleKey, interval) {
		return
	}
	Infow(message, keysAndValues...)
}

func WarnwSampled(sampleKey string, interval time.Duration, message string, keysAndValues ...any) {
	if !shouldSample(sampleKey, interval) {
		return
	}
	Warnw(message, keysAndValues...)
}

func shouldSample(sampleKey string, interval time.Duration) bool {
	if interval <= 0 {
		return true
	}
	now := time.Now()
	sampleMu.Lock()
	defer sampleMu.Unlock()
	last, ok := sampleLast[sampleKey]
	if ok && now.Sub(last) < interval {
		return false
	}
	sampleLast[sampleKey] = now
	return true
}
