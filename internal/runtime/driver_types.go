package runtime

import (
	"context"
	"regexp"
	"time"

	"github.com/jashok5/shadowsocks-go/internal/model"
)

type DetectBuckets struct {
	Text         map[int]string
	Hex          map[int]string
	TextCompiled map[int]*regexp.Regexp
	HexCompiled  map[int]*regexp.Regexp
	HexLiteral   map[int]string
	HexBytes     map[int][]byte
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
	MUHosts        []string
	Detect         DetectBuckets
	Fingerprint    string
	DialTimeout    time.Duration
	DNSResolver    string
	DNSPreferIPv4  bool
}

type ATPConfig struct {
	NodeInfo model.NodeInfo
	Users    []model.User
	Rules    []model.DetectRule

	HandshakeTimeout time.Duration
	IdleTimeout      time.Duration
	ResumeTicketTTL  time.Duration
	RestartDebounce  time.Duration
	CertReadyTimeout time.Duration
	CertRetryGap     time.Duration

	MaxConnsPerUser       int
	MaxOpenStreamsPerUser int
	EnableAuditBlock      bool
	AuditBlockDuration    time.Duration

	PullNodeInfoInterval  time.Duration
	PullUsersInterval     time.Duration
	PullDetectInterval    time.Duration
	ReportTrafficInterval time.Duration
	ReportAliveInterval   time.Duration
	ReportDetectInterval  time.Duration
	ReportNodeInterval    time.Duration
}

type DriverSnapshot struct {
	Transfer     map[int]model.PortTransfer
	UserTransfer map[int]model.PortTransfer
	OnlineIP     map[int][]string
	UserOnlineIP map[int][]string
	Detect       map[int][]int
	UserDetect   map[int][]int
	WrongIP      []string
}

type Driver interface {
	Start(context.Context, PortConfig) error
	Reload(context.Context, PortConfig) error
	Stop(context.Context, int) error
	Snapshot(context.Context) (DriverSnapshot, error)
	Close(context.Context) error
}

type ATPAwareDriver interface {
	ApplyATP(context.Context, ATPConfig) error
}
