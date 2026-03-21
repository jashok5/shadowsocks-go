package runtime

import (
	"fmt"
	"strings"

	"go.uber.org/zap"
)

func NewDriver(name string, log *zap.Logger) (Driver, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "mock":
		return NewMockDriver(), nil
	case "ss":
		return NewSSDriver(log), nil
	case "ssr":
		return NewSSRDriver(log), nil
	default:
		return nil, fmt.Errorf("unsupported runtime driver: %s", name)
	}
}
