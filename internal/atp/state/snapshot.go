package state

import (
	"sync/atomic"
	"time"
)

type UserPolicy struct {
	UserID         int32
	Token          string
	Password       string
	NodeSpeedLimit float64
	NodeConnector  int
}

type NodeInfo struct {
	NodeGroup      int     `json:"node_group"`
	NodeClass      int     `json:"node_class"`
	NodeSpeedlimit float64 `json:"node_speedlimit"`
	TrafficRate    float64 `json:"traffic_rate"`
	Sort           int     `json:"sort"`
	Server         string  `json:"server"`
}

type Snapshot struct {
	Node         NodeInfo
	UsersByID    map[int32]UserPolicy
	UsersByToken map[string][]UserPolicy
	UsersByPwd   map[string][]UserPolicy
	UpdatedAt    time.Time
}

type Store struct {
	v atomic.Value
}

func NewStore() *Store {
	s := &Store{}
	s.v.Store(Snapshot{UsersByID: map[int32]UserPolicy{}, UsersByToken: map[string][]UserPolicy{}, UsersByPwd: map[string][]UserPolicy{}, UpdatedAt: time.Now()})
	return s
}

func (s *Store) Load() Snapshot {
	return s.v.Load().(Snapshot)
}

func (s *Store) Update(next Snapshot) {
	if next.UsersByID == nil {
		next.UsersByID = map[int32]UserPolicy{}
	}
	if next.UsersByToken == nil {
		next.UsersByToken = map[string][]UserPolicy{}
	}
	if next.UsersByPwd == nil {
		next.UsersByPwd = map[string][]UserPolicy{}
	}
	if next.UpdatedAt.IsZero() {
		next.UpdatedAt = time.Now()
	}
	s.v.Store(next)
}
