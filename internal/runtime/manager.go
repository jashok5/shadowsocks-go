package runtime

import (
	"context"

	"github.com/jashok5/shadowsocks-go/internal/model"
)

type SyncInput struct {
	NodeInfo model.NodeInfo
	Users    []model.User
	Rules    []model.DetectRule
}

type Snapshot struct {
	Transfer     map[int]model.PortTransfer
	UserTransfer map[int]model.PortTransfer
	PortUser     map[int]int
	OnlineIP     map[int][]string
	UserOnlineIP map[int][]string
	Detect       map[int][]int
	UserDetect   map[int][]int
}

type Manager interface {
	Sync(context.Context, SyncInput) error
	Snapshot(context.Context) (Snapshot, error)
	Stop(context.Context) error
}
