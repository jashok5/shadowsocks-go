package runtime

import (
	"context"
	"time"

	"github.com/jashok5/shadowsocks-go/internal/model"
)

type SyncInput struct {
	NodeInfo   model.NodeInfo
	Users      []model.User
	Rules      []model.DetectRule
	MUHost     MUHostRule
	SwitchRule UserSwitchRule
	Runtime    RuntimeOptions
}

type MUHostRule struct {
	Enabled bool
	Regex   string
	Suffix  string
}

type RuntimeOptions struct {
	OnUnsupportedCipher string
	DialTimeout         time.Duration
	DNSResolver         string
	DNSPreferIPv4       bool
}

type UserSwitchRule struct {
	Enabled bool
	Mode    string
	Expr    string
}

type Snapshot struct {
	Transfer     map[int]model.PortTransfer
	UserTransfer map[int]model.PortTransfer
	PortUser     map[int]int
	OnlineIP     map[int][]string
	UserOnlineIP map[int][]string
	Detect       map[int][]int
	UserDetect   map[int][]int
	WrongIP      []string
}

type Manager interface {
	Sync(context.Context, SyncInput) error
	Snapshot(context.Context) (Snapshot, error)
	Stop(context.Context) error
}
