package runtime

import (
	"context"
	"maps"
	"sync"

	"github.com/jashok5/shadowsocks-go/internal/model"
)

type MockDriver struct {
	mu           sync.RWMutex
	configs      map[int]PortConfig
	transfer     map[int]model.PortTransfer
	userTransfer map[int]model.PortTransfer
	onlineIP     map[int][]string
	userOnlineIP map[int][]string
	detect       map[int][]int
	userDetect   map[int][]int
	wrongIP      []string
	starts       int
	reloads      int
	stops        int
}

func NewMockDriver() *MockDriver {
	return &MockDriver{
		configs:      make(map[int]PortConfig),
		transfer:     make(map[int]model.PortTransfer),
		userTransfer: make(map[int]model.PortTransfer),
		onlineIP:     make(map[int][]string),
		userOnlineIP: make(map[int][]string),
		detect:       make(map[int][]int),
		userDetect:   make(map[int][]int),
	}
}

func (d *MockDriver) Start(_ context.Context, cfg PortConfig) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.starts++
	d.configs[cfg.Port] = cfg
	if _, ok := d.transfer[cfg.Port]; !ok {
		d.transfer[cfg.Port] = model.PortTransfer{}
	}
	return nil
}

func (d *MockDriver) Reload(_ context.Context, cfg PortConfig) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reloads++
	d.configs[cfg.Port] = cfg
	return nil
}

func (d *MockDriver) Stop(_ context.Context, port int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stops++
	delete(d.configs, port)
	delete(d.transfer, port)
	delete(d.onlineIP, port)
	delete(d.userOnlineIP, port)
	delete(d.detect, port)
	delete(d.userDetect, port)
	return nil
}

func (d *MockDriver) Snapshot(_ context.Context) (DriverSnapshot, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	transfer := make(map[int]model.PortTransfer, len(d.transfer))
	maps.Copy(transfer, d.transfer)
	userTransfer := make(map[int]model.PortTransfer, len(d.userTransfer))
	maps.Copy(userTransfer, d.userTransfer)
	onlineIP := make(map[int][]string, len(d.onlineIP))
	for k, v := range d.onlineIP {
		cp := make([]string, len(v))
		copy(cp, v)
		onlineIP[k] = cp
	}
	userOnlineIP := make(map[int][]string, len(d.userOnlineIP))
	for k, v := range d.userOnlineIP {
		cp := make([]string, len(v))
		copy(cp, v)
		userOnlineIP[k] = cp
	}
	detect := make(map[int][]int, len(d.detect))
	for k, v := range d.detect {
		cp := make([]int, len(v))
		copy(cp, v)
		detect[k] = cp
	}
	userDetect := make(map[int][]int, len(d.userDetect))
	for k, v := range d.userDetect {
		cp := make([]int, len(v))
		copy(cp, v)
		userDetect[k] = cp
	}
	wrongIP := make([]string, len(d.wrongIP))
	copy(wrongIP, d.wrongIP)
	return DriverSnapshot{Transfer: transfer, UserTransfer: userTransfer, OnlineIP: onlineIP, UserOnlineIP: userOnlineIP, Detect: detect, UserDetect: userDetect, WrongIP: wrongIP}, nil
}

func (d *MockDriver) Close(_ context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.configs = make(map[int]PortConfig)
	d.transfer = make(map[int]model.PortTransfer)
	d.userTransfer = make(map[int]model.PortTransfer)
	d.onlineIP = make(map[int][]string)
	d.userOnlineIP = make(map[int][]string)
	d.detect = make(map[int][]int)
	d.userDetect = make(map[int][]int)
	return nil
}

func (d *MockDriver) InjectTransfer(port int, t model.PortTransfer) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.transfer[port] = t
}

func (d *MockDriver) InjectUserTransfer(userID int, t model.PortTransfer) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.userTransfer[userID] = t
}

func (d *MockDriver) InjectOnlineIP(port int, ips []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	cp := make([]string, len(ips))
	copy(cp, ips)
	d.onlineIP[port] = cp
}

func (d *MockDriver) InjectUserOnlineIP(userID int, ips []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	cp := make([]string, len(ips))
	copy(cp, ips)
	d.userOnlineIP[userID] = cp
}

func (d *MockDriver) InjectDetect(port int, ruleIDs []int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	cp := make([]int, len(ruleIDs))
	copy(cp, ruleIDs)
	d.detect[port] = cp
}

func (d *MockDriver) InjectUserDetect(userID int, ruleIDs []int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	cp := make([]int, len(ruleIDs))
	copy(cp, ruleIDs)
	d.userDetect[userID] = cp
}

func (d *MockDriver) Stats() (starts, reloads, stops int) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.starts, d.reloads, d.stops
}

func (d *MockDriver) HasPort(port int) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	_, ok := d.configs[port]
	return ok
}
