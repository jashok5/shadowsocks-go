package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/jashok5/shadowsocks-go/internal/model"

	"go.uber.org/zap"
)

type serverState struct {
	Port   int
	UserID int
	Cfg    PortConfig
}

type MemoryManager struct {
	log     *zap.Logger
	drv     Driver
	workers int

	mu       sync.RWMutex
	nodeInfo model.NodeInfo
	rules    []model.DetectRule
	servers  map[int]serverState
}

func NewMemoryManager(log *zap.Logger) *MemoryManager {
	return NewMemoryManagerWithDriver(log, NewMockDriver(), 8)
}

func NewMemoryManagerWithDriver(log *zap.Logger, drv Driver, workers int) *MemoryManager {
	if workers <= 0 {
		workers = 1
	}
	return &MemoryManager{
		log:     log,
		drv:     drv,
		workers: workers,
		servers: make(map[int]serverState),
	}
}

type opKind string

const (
	opStart  opKind = "start"
	opReload opKind = "reload"
	opStop   opKind = "stop"
)

type reconcileOp struct {
	kind opKind
	port int
	cfg  PortConfig
}

func (m *MemoryManager) Sync(ctx context.Context, in SyncInput) error {
	onUnsupported := strings.ToLower(strings.TrimSpace(in.Runtime.OnUnsupportedCipher))
	if onUnsupported == "" {
		onUnsupported = "skip"
	}
	muUsers := make(map[int]string)
	muUserSpeed := make(map[int]float64)
	muHostsByUserID := buildMUHostMap(in.Users, in.MUHost)
	for _, u := range in.Users {
		if u.IsMultiUser == 0 {
			muUsers[u.ID] = u.Passwd
			muUserSpeed[u.ID] = maxSpeed(in.NodeInfo.NodeSpeedLimit, u.NodeSpeed)
		}
	}

	users := normalizeUsers(in.Users, in.NodeInfo)
	buckets := classifyRules(in.Rules)
	ruleHash := hashRuleBuckets(buckets)

	next := make(map[int]serverState, len(users))
	unsupportedSkipped := 0
	for _, u := range users {
		if !isCipherSupported(u.Method) {
			err := fmt.Errorf("unsupported cipher for user_id=%d port=%d method=%s", u.ID, u.Port, u.Method)
			if onUnsupported == "fail" {
				return err
			}
			unsupportedSkipped++
			m.log.Warn("skip user due to unsupported cipher", zap.Int("user_id", u.ID), zap.Int("port", u.Port), zap.String("method", u.Method))
			continue
		}
		port := effectivePort(u, in.NodeInfo)
		cfg := buildPortConfig(u, in.NodeInfo, buckets, ruleHash, in.Runtime)
		if u.IsMultiUser != 0 {
			cfg.Users = cloneIntStringMap(muUsers)
			cfg.UserSpeed = cloneIntFloatMap(muUserSpeed)
			cfg.MUHosts = collectMUHosts(muHostsByUserID)
			cfg.Fingerprint = buildFingerprintWithUsers(u, in.NodeInfo, ruleHash, cfg.Users, cfg.MUHosts)
		}
		next[port] = serverState{
			Port:   port,
			UserID: u.ID,
			Cfg:    cfg,
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	added := 0
	updated := 0
	removed := 0
	ops := make([]reconcileOp, 0, len(next)+len(m.servers))

	for port, old := range m.servers {
		nv, ok := next[port]
		if !ok {
			ops = append(ops, reconcileOp{kind: opStop, port: port})
			removed++
			continue
		}
		if old.Cfg.Fingerprint != nv.Cfg.Fingerprint || old.UserID != nv.UserID {
			ops = append(ops, reconcileOp{kind: opReload, port: port, cfg: nv.Cfg})
			updated++
		}
	}

	for port, nv := range next {
		if _, ok := m.servers[port]; !ok {
			ops = append(ops, reconcileOp{kind: opStart, port: port, cfg: nv.Cfg})
			added++
		}
	}

	if err := m.applyOps(ctx, ops); err != nil {
		return err
	}

	m.servers = next

	m.nodeInfo = in.NodeInfo
	m.rules = append(m.rules[:0], in.Rules...)

	m.log.Info("runtime sync reconciled",
		zap.Int("input_users", len(in.Users)),
		zap.Int("effective_users", len(users)),
		zap.Int("desired", len(next)),
		zap.Int("active", len(next)),
		zap.Int("added", added),
		zap.Int("updated", updated),
		zap.Int("removed", removed),
		zap.Int("unsupported_skipped", unsupportedSkipped),
	)
	return nil
}

func (m *MemoryManager) applyOps(ctx context.Context, ops []reconcileOp) error {
	if len(ops) == 0 {
		return nil
	}
	jobs := make(chan reconcileOp)
	errCh := make(chan error, 1)

	workers := m.workers
	if workers > len(ops) {
		workers = len(ops)
	}
	if workers <= 0 {
		workers = 1
	}

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for op := range jobs {
				var err error
				switch op.kind {
				case opStart:
					err = m.drv.Start(ctx, op.cfg)
				case opReload:
					err = m.drv.Reload(ctx, op.cfg)
				case opStop:
					err = m.drv.Stop(ctx, op.port)
				default:
					err = fmt.Errorf("unknown op kind: %s", op.kind)
				}
				if err != nil {
					select {
					case errCh <- fmt.Errorf("%s port %d failed: %w", op.kind, op.port, err):
					default:
					}
					return
				}
			}
		}()
	}

	for _, op := range ops {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return ctx.Err()
		case err := <-errCh:
			close(jobs)
			wg.Wait()
			return err
		case jobs <- op:
		}
	}
	close(jobs)
	wg.Wait()

	select {
	case err := <-errCh:
		return err
	default:
		return nil
	}
}

func (m *MemoryManager) Snapshot(ctx context.Context) (Snapshot, error) {
	m.mu.RLock()
	portUser := make(map[int]int, len(m.servers))
	for port, s := range m.servers {
		portUser[port] = s.UserID
	}
	defer m.mu.RUnlock()

	driverSnap, err := m.drv.Snapshot(ctx)
	if err != nil {
		return Snapshot{}, err
	}

	onlineIP := make(map[int][]string)
	userOnlineIP := make(map[int][]string)
	for port, ips := range driverSnap.OnlineIP {
		uid, ok := portUser[port]
		if !ok {
			continue
		}
		onlineIP[uid] = append(onlineIP[uid], ips...)
	}
	if len(driverSnap.UserOnlineIP) > 0 {
		userOnlineIP = driverSnap.UserOnlineIP
	}

	detect := make(map[int][]int)
	for port, ids := range driverSnap.Detect {
		uid, ok := portUser[port]
		if !ok {
			continue
		}
		detect[uid] = append(detect[uid], ids...)
	}
	if len(driverSnap.UserDetect) > 0 {
		detect = driverSnap.UserDetect
	}

	return Snapshot{
		Transfer:     driverSnap.Transfer,
		UserTransfer: driverSnap.UserTransfer,
		PortUser:     portUser,
		OnlineIP:     onlineIP,
		UserOnlineIP: userOnlineIP,
		Detect:       detect,
		UserDetect:   driverSnap.UserDetect,
		WrongIP:      driverSnap.WrongIP,
	}, nil
}

func (m *MemoryManager) Stop(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := len(m.servers)
	if err := m.drv.Close(ctx); err != nil {
		return err
	}
	m.servers = make(map[int]serverState)
	m.log.Info("runtime manager stopped", zap.Int("servers", count))
	return nil
}

func (m *MemoryManager) Driver() Driver {
	return m.drv
}

func normalizeUsers(users []model.User, node model.NodeInfo) []model.User {
	out := make([]model.User, 0, len(users))
	for _, u := range users {
		switch node.MUOnly {
		case 1:
			if u.IsMultiUser == 0 {
				continue
			}
		case -1:
			if u.IsMultiUser != 0 {
				continue
			}
		}
		out = append(out, u)
	}
	return out
}

func effectivePort(u model.User, node model.NodeInfo) int {
	if node.MUOnly == 1 {
		return u.Port + node.PortOffset
	}
	return u.Port
}

func buildFingerprint(u model.User, node model.NodeInfo, ruleHash string) string {
	cfg := struct {
		ID             int
		Port           int
		Passwd         string
		Method         string
		Protocol       string
		ProtocolParam  string
		Obfs           string
		ObfsParam      string
		ForbiddenIP    string
		ForbiddenPort  string
		NodeSpeedLimit float64
		NodeTraffic    float64
		NodeMUOnly     int
		IsMultiUser    int
		RulesHash      string
	}{
		ID:             u.ID,
		Port:           u.Port,
		Passwd:         u.Passwd,
		Method:         u.Method,
		Protocol:       u.Protocol,
		ProtocolParam:  u.ProtocolParam,
		Obfs:           u.Obfs,
		ObfsParam:      u.ObfsParam,
		ForbiddenIP:    u.ForbiddenIP,
		ForbiddenPort:  u.ForbiddenPort,
		NodeSpeedLimit: node.NodeSpeedLimit,
		NodeTraffic:    node.TrafficRate,
		NodeMUOnly:     node.MUOnly,
		IsMultiUser:    u.IsMultiUser,
		RulesHash:      ruleHash,
	}
	b, _ := json.Marshal(cfg)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hashRuleBuckets(buckets DetectBuckets) string {
	if len(buckets.Text) == 0 && len(buckets.Hex) == 0 {
		return ""
	}
	type item struct {
		ID    int
		Regex string
		Kind  string
	}
	all := make([]item, 0, len(buckets.Text)+len(buckets.Hex))
	for id, regex := range buckets.Text {
		all = append(all, item{ID: id, Regex: regex, Kind: "text"})
	}
	for id, regex := range buckets.Hex {
		all = append(all, item{ID: id, Regex: regex, Kind: "hex"})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].ID != all[j].ID {
			return all[i].ID < all[j].ID
		}
		if all[i].Kind != all[j].Kind {
			return all[i].Kind < all[j].Kind
		}
		return all[i].Regex < all[j].Regex
	})
	b, _ := json.Marshal(all)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func classifyRules(rules []model.DetectRule) DetectBuckets {
	b := DetectBuckets{Text: make(map[int]string), Hex: make(map[int]string)}
	for _, r := range rules {
		if r.Type == 1 {
			b.Text[r.ID] = r.Regex
			continue
		}
		b.Hex[r.ID] = r.Regex
	}
	return b
}

func buildPortConfig(u model.User, node model.NodeInfo, buckets DetectBuckets, ruleHash string, opts RuntimeOptions) PortConfig {
	port := effectivePort(u, node)
	users := map[int]string{}
	if u.IsMultiUser != 0 {
		users[u.ID] = u.Passwd
	}
	return PortConfig{
		Port:           port,
		SourcePort:     u.Port,
		UserID:         u.ID,
		Password:       u.Passwd,
		Users:          users,
		Method:         u.Method,
		Protocol:       u.Protocol,
		ProtocolParam:  u.ProtocolParam,
		Obfs:           u.Obfs,
		ObfsParam:      u.ObfsParam,
		ForbiddenIP:    u.ForbiddenIP,
		ForbiddenPort:  u.ForbiddenPort,
		NodeSpeedLimit: maxSpeed(node.NodeSpeedLimit, u.NodeSpeed),
		NodeTraffic:    node.TrafficRate,
		IsMultiUser:    u.IsMultiUser != 0,
		MUHosts:        nil,
		DialTimeout:    opts.DialTimeout,
		DNSResolver:    strings.TrimSpace(opts.DNSResolver),
		DNSPreferIPv4:  opts.DNSPreferIPv4,
		Detect: DetectBuckets{
			Text: cloneStringMap(buckets.Text),
			Hex:  cloneStringMap(buckets.Hex),
		},
		Fingerprint: buildFingerprintWithUsers(u, node, ruleHash, users, nil),
	}
}

func cloneIntStringMap(in map[int]string) map[int]string {
	out := make(map[int]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneIntFloatMap(in map[int]float64) map[int]float64 {
	out := make(map[int]float64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func buildFingerprintWithUsers(u model.User, node model.NodeInfo, ruleHash string, users map[int]string, muHosts []string) string {
	usersHash := hashUsers(users)
	hostHash := hashMUHosts(muHosts)
	return buildFingerprint(u, node, ruleHash+"|"+usersHash+"|"+hostHash)
}

func hashUsers(users map[int]string) string {
	if len(users) == 0 {
		return ""
	}
	type item struct {
		UID  int
		Pass string
	}
	arr := make([]item, 0, len(users))
	for uid, pass := range users {
		arr = append(arr, item{UID: uid, Pass: pass})
	}
	sort.Slice(arr, func(i, j int) bool {
		if arr[i].UID != arr[j].UID {
			return arr[i].UID < arr[j].UID
		}
		return arr[i].Pass < arr[j].Pass
	})
	b, _ := json.Marshal(arr)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hashMUHosts(hosts []string) string {
	if len(hosts) == 0 {
		return ""
	}
	cp := make([]string, 0, len(hosts))
	for _, h := range hosts {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		cp = append(cp, h)
	}
	if len(cp) == 0 {
		return ""
	}
	sort.Strings(cp)
	b, _ := json.Marshal(cp)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func cloneStringMap(in map[int]string) map[int]string {
	out := make(map[int]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func maxSpeed(node, user float64) float64 {
	if node > user {
		return node
	}
	return user
}
