package runtime

import (
	"context"

	"github.com/jashok5/shadowsocks-go/internal/model"

	"go.uber.org/zap"
)

type NoopManager struct {
	log *zap.Logger
}

func NewNoopManager(log *zap.Logger) *NoopManager {
	return &NoopManager{log: log}
}

func (m *NoopManager) Sync(_ context.Context, in SyncInput) error {
	m.log.Info("runtime sync applied", zap.Int("users", len(in.Users)), zap.Int("rules", len(in.Rules)))
	return nil
}

func (m *NoopManager) Snapshot(_ context.Context) (Snapshot, error) {
	return Snapshot{
		Transfer:     map[int]model.PortTransfer{},
		UserTransfer: map[int]model.PortTransfer{},
		PortUser:     map[int]int{},
		OnlineIP:     map[int][]string{},
		UserOnlineIP: map[int][]string{},
		Detect:       map[int][]int{},
		UserDetect:   map[int][]int{},
	}, nil
}

func (m *NoopManager) Stop(_ context.Context) error {
	m.log.Info("runtime manager stopped")
	return nil
}
