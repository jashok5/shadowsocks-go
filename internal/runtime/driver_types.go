package runtime

import (
	"context"

	"github.com/jashok5/shadowsocks-go/internal/model"
)

type DetectBuckets struct {
	Text map[int]string
	Hex  map[int]string
}

type PortConfig struct {
	Port           int
	SourcePort     int
	UserID         int
	Password       string
	Users          map[int]string
	UserSpeed      map[int]float64
	Method         string
	Protocol       string
	ProtocolParam  string
	Obfs           string
	ObfsParam      string
	ForbiddenIP    string
	ForbiddenPort  string
	NodeSpeedLimit float64
	NodeTraffic    float64
	IsMultiUser    bool
	Detect         DetectBuckets
	Fingerprint    string
}

type DriverSnapshot struct {
	Transfer     map[int]model.PortTransfer
	UserTransfer map[int]model.PortTransfer
	OnlineIP     map[int][]string
	UserOnlineIP map[int][]string
	Detect       map[int][]int
	UserDetect   map[int][]int
}

type Driver interface {
	Start(context.Context, PortConfig) error
	Reload(context.Context, PortConfig) error
	Stop(context.Context, int) error
	Snapshot(context.Context) (DriverSnapshot, error)
	Close(context.Context) error
}
