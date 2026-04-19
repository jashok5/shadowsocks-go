package runtime

import (
	"fmt"
	"strings"

	"go.uber.org/zap"
)

type DriverTuning struct {
	MaxUDPSessionPerPort      int
	MaxUDPResolveCacheEntries int
	HandshakeMaxConcurrent    int
	PerIPHandshakeMax         int
	UDPAssocErrorDeltaWarn    int
}

func (t DriverTuning) maxUDPSessionPerPortOr(def int) int {
	if t.MaxUDPSessionPerPort > 0 {
		return t.MaxUDPSessionPerPort
	}
	return def
}

func (t DriverTuning) maxUDPResolveCacheEntriesOr(def int) int {
	if t.MaxUDPResolveCacheEntries > 0 {
		return t.MaxUDPResolveCacheEntries
	}
	return def
}

func (t DriverTuning) handshakeMaxConcurrentOr(def int) int {
	if t.HandshakeMaxConcurrent > 0 {
		return t.HandshakeMaxConcurrent
	}
	return def
}

func (t DriverTuning) udpAssocErrorDeltaWarnOr(def int) int {
	if t.UDPAssocErrorDeltaWarn > 0 {
		return t.UDPAssocErrorDeltaWarn
	}
	return def
}

func NewDriver(name string, log *zap.Logger, tuning DriverTuning) (Driver, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "mock":
		return NewMockDriver(), nil
	case "ss":
		return NewSSDriverWithTuning(log, tuning), nil
	case "ssr":
		return NewSSRDriverWithTuning(log, tuning), nil
	case "atp":
		return NewATPDriver(log), nil
	default:
		return nil, fmt.Errorf("unsupported runtime driver: %s", name)
	}
}
