package panel

import (
	"crypto/subtle"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	goRuntime "runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jashok5/shadowsocks-go/internal/config"
	"github.com/jashok5/shadowsocks-go/internal/logger"
	"github.com/jashok5/shadowsocks-go/internal/runtime"
	"go.uber.org/zap"
)

type Server struct {
	cfg       config.PanelConfig
	log       *zap.Logger
	rt        runtime.Manager
	onlineMu  sync.Mutex
	online    map[int]map[string]time.Time
	onlineTTL time.Duration
	mode      string
	assets    fs.FS
	driver    string
	version   string
	startedAt time.Time
}

type UserOverview struct {
	UserID        int      `json:"user_id"`
	Upload        int64    `json:"upload"`
	Download      int64    `json:"download"`
	OnlineIPCount int      `json:"online_ip_count"`
	OnlineIPs     []string `json:"online_ips"`
	LastSeenUnix  int64    `json:"last_seen_unix"`
	DetectCount   int      `json:"detect_count"`
	Ports         []int    `json:"ports"`
}

type OverviewResponse struct {
	NowUnix       int64          `json:"now_unix"`
	Version       string         `json:"version"`
	GoVersion     string         `json:"go_version"`
	Driver        string         `json:"driver"`
	StartedAtUnix int64          `json:"started_at_unix"`
	UptimeSeconds int64          `json:"uptime_seconds"`
	Ports         int            `json:"ports"`
	Users         int            `json:"users"`
	OnlineUsers   int            `json:"online_users"`
	OnlineIPs     int            `json:"online_ips"`
	TotalUpload   int64          `json:"total_upload"`
	TotalDownload int64          `json:"total_download"`
	WrongIPs      int            `json:"wrong_ips"`
	UserList      []UserOverview `json:"user_list"`
	Mem           struct {
		Goroutines int    `json:"goroutines"`
		HeapAlloc  uint64 `json:"heap_alloc"`
		HeapInuse  uint64 `json:"heap_inuse"`
		HeapObject uint64 `json:"heap_objects"`
		NumGC      uint32 `json:"num_gc"`
		RSSBytes   uint64 `json:"rss_bytes"`
	} `json:"mem"`
	SSStats  []runtime.PortRuntimeStat  `json:"ss_stats,omitempty"`
	SSRStats []runtime.SSRPortCacheStat `json:"ssr_stats,omitempty"`
}

type UserDetailResponse struct {
	UserID        int      `json:"user_id"`
	Upload        int64    `json:"upload"`
	Download      int64    `json:"download"`
	OnlineIPCount int      `json:"online_ip_count"`
	OnlineIPs     []string `json:"online_ips"`
	DetectRules   []int    `json:"detect_rules"`
	Ports         []int    `json:"ports"`
	Active        bool     `json:"active"`
}

type LogsSnapshotResponse struct {
	LatestID int64             `json:"latest_id"`
	Items    []logger.LogEntry `json:"items"`
}

type onlineWindowEntry struct {
	IPs          []string
	LastSeenUnix int64
}

func NewServer(cfg config.PanelConfig, log *zap.Logger, rt runtime.Manager, assets fs.FS, driver string, version string, startedAt time.Time, onlineTTL time.Duration) *Server {
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	if mode == "" {
		mode = "dev"
	}
	if onlineTTL <= 0 {
		onlineTTL = 60 * time.Second
	}
	return &Server{cfg: cfg, log: log, rt: rt, online: make(map[int]map[string]time.Time), onlineTTL: onlineTTL, mode: mode, assets: assets, driver: strings.TrimSpace(driver), version: strings.TrimSpace(version), startedAt: startedAt}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/panel/api/auth/verify", s.withCORS(s.withAuth(s.handleVerify)))
	mux.HandleFunc("/panel/api/overview", s.withCORS(s.withAuth(s.handleOverview)))
	mux.HandleFunc("/panel/api/stream", s.withCORS(s.withAuth(s.handleStream)))
	mux.HandleFunc("/panel/api/users/", s.withCORS(s.withAuth(s.handleUserDetail)))
	mux.HandleFunc("/panel/api/logs", s.withCORS(s.withAuth(s.handleLogsSnapshot)))
	mux.HandleFunc("/panel/api/logs/stream", s.withCORS(s.withAuth(s.handleLogsStream)))
	if s.mode == "prod" && s.assets != nil {
		fileServer := http.FileServer(http.FS(s.assets))
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/panel/api/") {
				http.NotFound(w, r)
				return
			}
			path := strings.TrimSpace(r.URL.Path)
			if path == "" || path == "/" {
				s.serveIndex(w)
				return
			}
			target := strings.TrimPrefix(path, "/")
			if _, err := fs.Stat(s.assets, target); err == nil {
				fileServer.ServeHTTP(w, r)
				return
			}
			s.serveIndex(w)
		})
	}
	return mux
}

func (s *Server) handleLogsSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	b := logger.Buffer()
	if b == nil {
		writeJSON(w, http.StatusOK, LogsSnapshotResponse{LatestID: 0, Items: []logger.LogEntry{}})
		return
	}
	limit := parsePositiveInt(r.URL.Query().Get("limit"), 300)
	items := b.Tail(limit)
	writeJSON(w, http.StatusOK, LogsSnapshotResponse{LatestID: b.LatestID(), Items: items})
}

func (s *Server) handleLogsStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := initSSE(w, r)
	if !ok {
		return
	}

	b := logger.Buffer()
	if b == nil {
		return
	}

	afterID := parseInt64(r.URL.Query().Get("after_id"), 0)
	limit := parsePositiveInt(r.URL.Query().Get("limit"), 200)
	ticker := time.NewTicker(700 * time.Millisecond)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			items := b.Since(afterID, limit)
			if len(items) == 0 {
				continue
			}
			afterID = items[len(items)-1].ID
			writeSSE(w, "logs", map[string]any{"items": items, "latest_id": afterID})
			flusher.Flush()
		}
	}
}

func (s *Server) handleVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	snap, err := s.rt.Snapshot(r.Context())
	if err != nil {
		s.log.Warn("panel snapshot failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "snapshot failed"})
		return
	}

	resp := s.buildOverview(snap)
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := initSSE(w, r)
	if !ok {
		return
	}

	interval := s.cfg.StreamInterval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			snap, err := s.rt.Snapshot(ctx)
			if err != nil {
				writeSSE(w, "error", map[string]string{"error": "snapshot failed"})
				flusher.Flush()
				continue
			}
			writeSSE(w, "overview", s.buildOverview(snap))
			flusher.Flush()
		}
	}
}

func initSSE(w http.ResponseWriter, r *http.Request) (http.Flusher, bool) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return nil, false
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "stream unsupported"})
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	writeSSE(w, "ready", map[string]bool{"ok": true})
	flusher.Flush()
	return flusher, true
}

func (s *Server) handleUserDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	uid, err := parseUserID(r.URL.Path)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid user id"})
		return
	}
	snap, err := s.rt.Snapshot(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "snapshot failed"})
		return
	}
	realUsers := collectRealUsers(snap)
	if isCarrierUser(uid, snap, realUsers) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}
	now := time.Now()
	onlineMap := s.onlineSnapshotFromSnap(now, snap)
	detail, ok := s.buildUserDetail(snap, uid)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}
	if online, ok := onlineMap[uid]; ok {
		detail.OnlineIPs = append([]string{}, online.IPs...)
		detail.OnlineIPCount = len(online.IPs)
		detail.Active = detail.OnlineIPCount > 0
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) buildOverview(snap runtime.Snapshot) OverviewResponse {
	resp := OverviewResponse{}
	now := time.Now()
	resp.NowUnix = now.Unix()
	resp.Version = s.version
	resp.GoVersion = goRuntime.Version()
	resp.Driver = s.driver
	resp.StartedAtUnix = s.startedAt.Unix()
	resp.UptimeSeconds = int64(now.Sub(s.startedAt).Seconds())
	resp.Ports = len(snap.PortUser)
	resp.WrongIPs = len(snap.WrongIP)

	userMap := map[int]*UserOverview{}
	for uid, t := range snap.UserTransfer {
		item := ensureUser(userMap, uid)
		item.Upload = t.Upload
		item.Download = t.Download
	}
	for port, t := range snap.Transfer {
		uid, ok := snap.PortUser[port]
		if !ok {
			continue
		}
		item := ensureUser(userMap, uid)
		if item.Upload == 0 {
			item.Upload = t.Upload
		}
		if item.Download == 0 {
			item.Download = t.Download
		}
	}
	for uid, ips := range snap.UserOnlineIP {
		item := ensureUser(userMap, uid)
		item.OnlineIPs = append(item.OnlineIPs, ips...)
	}
	for port, ips := range snap.OnlineIP {
		if uid, ok := snap.PortUser[port]; ok {
			item := ensureUser(userMap, uid)
			item.OnlineIPs = append(item.OnlineIPs, ips...)
			continue
		}
		item := ensureUser(userMap, port)
		item.OnlineIPs = append(item.OnlineIPs, ips...)
	}

	cachedOnline := s.onlineSnapshotFromSnap(now, snap)
	for uid, online := range cachedOnline {
		item := ensureUser(userMap, uid)
		item.OnlineIPs = append(item.OnlineIPs, online.IPs...)
		if online.LastSeenUnix > item.LastSeenUnix {
			item.LastSeenUnix = online.LastSeenUnix
		}
	}
	for uid, rules := range snap.UserDetect {
		item := ensureUser(userMap, uid)
		item.DetectCount = len(rules)
	}
	for port, rules := range snap.Detect {
		uid, ok := snap.PortUser[port]
		if !ok {
			uid = port
		}
		item := ensureUser(userMap, uid)
		if item.DetectCount == 0 {
			item.DetectCount = len(rules)
		}
	}
	for port, uid := range snap.PortUser {
		item := ensureUser(userMap, uid)
		item.Ports = append(item.Ports, port)
	}
	if len(snap.PortUser) > 0 {
		sharedPorts := make([]int, 0, len(snap.PortUser))
		for port := range snap.PortUser {
			sharedPorts = append(sharedPorts, port)
		}
		sort.Ints(sharedPorts)
		for _, item := range userMap {
			if len(item.Ports) > 0 {
				continue
			}
			item.Ports = append(item.Ports, sharedPorts...)
		}
	}

	realUsers := collectRealUsers(snap)
	for uid := range userMap {
		if isCarrierUser(uid, snap, realUsers) {
			delete(userMap, uid)
		}
	}
	for uid, v := range userMap {
		if len(v.OnlineIPs) == 0 {
			delete(userMap, uid)
		}
	}

	resp.TotalUpload, resp.TotalDownload = aggregateTotals(snap)

	resp.UserList = make([]UserOverview, 0, len(userMap))
	for _, v := range userMap {
		v.OnlineIPs = dedupeStrings(v.OnlineIPs)
		v.OnlineIPCount = len(v.OnlineIPs)
		sort.Ints(v.Ports)
		resp.UserList = append(resp.UserList, *v)
		resp.OnlineIPs += v.OnlineIPCount
		if v.OnlineIPCount > 0 {
			resp.OnlineUsers++
		}
	}
	resp.Users = len(resp.UserList)
	sort.Slice(resp.UserList, func(i, j int) bool {
		if resp.UserList[i].OnlineIPCount != resp.UserList[j].OnlineIPCount {
			return resp.UserList[i].OnlineIPCount > resp.UserList[j].OnlineIPCount
		}
		return resp.UserList[i].UserID < resp.UserList[j].UserID
	})

	var ms goRuntime.MemStats
	goRuntime.ReadMemStats(&ms)
	resp.Mem.Goroutines = goRuntime.NumGoroutine()
	resp.Mem.HeapAlloc = ms.HeapAlloc
	resp.Mem.HeapInuse = ms.HeapInuse
	resp.Mem.HeapObject = ms.HeapObjects
	resp.Mem.NumGC = ms.NumGC
	resp.Mem.RSSBytes = processRSSBytes()

	if mm, ok := s.rt.(*runtime.MemoryManager); ok {
		switch d := mm.Driver().(type) {
		case *runtime.SSDriver:
			resp.SSStats = d.Stats()
		case *runtime.SSRDriver:
			resp.SSRStats = d.CacheStats()
			s.alignSSROnline(resp.SSRStats, resp.UserList)
		}
	}

	return resp
}

func (s *Server) updateOnlineWindow(now time.Time, incoming map[int][]string) {
	s.onlineMu.Lock()
	defer s.onlineMu.Unlock()
	for uid, ips := range incoming {
		if _, ok := s.online[uid]; !ok {
			s.online[uid] = make(map[string]time.Time)
		}
		for _, ip := range ips {
			ip = strings.TrimSpace(ip)
			if ip == "" {
				continue
			}
			s.online[uid][ip] = now
		}
	}
	cutoff := now.Add(-s.onlineTTL)
	for uid, ips := range s.online {
		for ip, ts := range ips {
			if ts.Before(cutoff) {
				delete(ips, ip)
			}
		}
		if len(ips) == 0 {
			delete(s.online, uid)
		}
	}
}

func (s *Server) onlineWindowSnapshot(now time.Time) map[int]onlineWindowEntry {
	s.onlineMu.Lock()
	defer s.onlineMu.Unlock()
	cutoff := now.Add(-s.onlineTTL)
	out := make(map[int]onlineWindowEntry, len(s.online))
	for uid, ips := range s.online {
		arr := make([]string, 0, len(ips))
		lastSeen := int64(0)
		for ip, ts := range ips {
			if ts.Before(cutoff) {
				delete(ips, ip)
				continue
			}
			arr = append(arr, ip)
			unix := ts.Unix()
			if unix > lastSeen {
				lastSeen = unix
			}
		}
		if len(arr) == 0 {
			delete(s.online, uid)
			continue
		}
		sort.Strings(arr)
		out[uid] = onlineWindowEntry{IPs: arr, LastSeenUnix: lastSeen}
	}
	return out
}

func (s *Server) onlineSnapshotFromSnap(now time.Time, snap runtime.Snapshot) map[int]onlineWindowEntry {
	s.updateOnlineWindow(now, collectIncomingOnline(snap))
	return s.onlineWindowSnapshot(now)
}

func aggregateTotals(snap runtime.Snapshot) (int64, int64) {
	var up int64
	var down int64
	if len(snap.UserTransfer) > 0 {
		for _, t := range snap.UserTransfer {
			up += t.Upload
			down += t.Download
		}
		return up, down
	}
	for _, t := range snap.Transfer {
		up += t.Upload
		down += t.Download
	}
	return up, down
}

func (s *Server) alignSSROnline(stats []runtime.SSRPortCacheStat, users []UserOverview) {
	if len(stats) == 0 {
		return
	}
	byPort := make(map[int]map[int]int)
	for _, u := range users {
		if u.OnlineIPCount <= 0 {
			continue
		}
		for _, port := range u.Ports {
			if _, ok := byPort[port]; !ok {
				byPort[port] = make(map[int]int)
			}
			byPort[port][u.UserID] = u.OnlineIPCount
		}
	}
	for i := range stats {
		if m, ok := byPort[stats[i].Port]; ok {
			stats[i].UserOnlineCount = m
		}
	}
}

func collectIncomingOnline(snap runtime.Snapshot) map[int][]string {
	out := make(map[int][]string)
	for uid, ips := range snap.UserOnlineIP {
		out[uid] = append(out[uid], ips...)
	}
	for port, ips := range snap.OnlineIP {
		uid, ok := snap.PortUser[port]
		if !ok {
			uid = port
		}
		out[uid] = append(out[uid], ips...)
	}
	for uid, ips := range out {
		out[uid] = dedupeStrings(ips)
	}
	return out
}

func collectRealUsers(snap runtime.Snapshot) map[int]struct{} {
	out := make(map[int]struct{})
	for uid := range snap.UserTransfer {
		out[uid] = struct{}{}
	}
	for uid := range snap.UserOnlineIP {
		out[uid] = struct{}{}
	}
	for uid := range snap.UserDetect {
		out[uid] = struct{}{}
	}
	return out
}

func isCarrierUser(uid int, snap runtime.Snapshot, realUsers map[int]struct{}) bool {
	if len(realUsers) == 0 {
		return false
	}
	if _, ok := realUsers[uid]; ok {
		return false
	}
	for _, portUID := range snap.PortUser {
		if portUID == uid {
			return true
		}
	}
	return false
}

func ensureUser(userMap map[int]*UserOverview, uid int) *UserOverview {
	v, ok := userMap[uid]
	if ok {
		return v
	}
	v = &UserOverview{UserID: uid, Ports: []int{}}
	userMap[uid] = v
	return v
}

func (s *Server) buildUserDetail(snap runtime.Snapshot, uid int) (UserDetailResponse, bool) {
	detail := UserDetailResponse{UserID: uid, OnlineIPs: []string{}, DetectRules: []int{}, Ports: []int{}}
	found := false
	if tr, ok := snap.UserTransfer[uid]; ok {
		detail.Upload = tr.Upload
		detail.Download = tr.Download
		found = true
	}
	if detail.Upload == 0 && detail.Download == 0 {
		for port, userID := range snap.PortUser {
			if userID != uid {
				continue
			}
			if tr, ok := snap.Transfer[port]; ok {
				detail.Upload += tr.Upload
				detail.Download += tr.Download
				found = true
			}
		}
	}
	if ips, ok := snap.UserOnlineIP[uid]; ok {
		detail.OnlineIPs = append(detail.OnlineIPs, ips...)
		detail.OnlineIPCount = len(detail.OnlineIPs)
		detail.Active = detail.OnlineIPCount > 0
		found = true
	}
	if detail.OnlineIPCount == 0 {
		for port, userID := range snap.PortUser {
			if userID != uid {
				continue
			}
			if ips, ok := snap.OnlineIP[port]; ok {
				detail.OnlineIPs = append(detail.OnlineIPs, ips...)
				found = true
			}
		}
		detail.OnlineIPs = dedupeStrings(detail.OnlineIPs)
		detail.OnlineIPCount = len(detail.OnlineIPs)
		detail.Active = detail.OnlineIPCount > 0
	}
	if detail.OnlineIPCount == 0 {
		if ips, ok := snap.OnlineIP[uid]; ok {
			detail.OnlineIPs = append(detail.OnlineIPs, ips...)
			detail.OnlineIPs = dedupeStrings(detail.OnlineIPs)
			detail.OnlineIPCount = len(detail.OnlineIPs)
			detail.Active = detail.OnlineIPCount > 0
			found = true
		}
	}
	if rules, ok := snap.UserDetect[uid]; ok {
		detail.DetectRules = append(detail.DetectRules, rules...)
		sort.Ints(detail.DetectRules)
		found = true
	}
	if len(detail.DetectRules) == 0 {
		for port, userID := range snap.PortUser {
			if userID != uid {
				continue
			}
			if rules, ok := snap.Detect[port]; ok {
				detail.DetectRules = append(detail.DetectRules, rules...)
				found = true
			}
		}
		sort.Ints(detail.DetectRules)
		detail.DetectRules = dedupeInts(detail.DetectRules)
	}
	for port, userID := range snap.PortUser {
		if userID == uid {
			detail.Ports = append(detail.Ports, port)
			found = true
		}
	}
	if len(detail.Ports) == 0 {
		for port := range snap.PortUser {
			detail.Ports = append(detail.Ports, port)
		}
	}
	sort.Ints(detail.Ports)
	return detail, found
}

func parseUserID(path string) (int, error) {
	v := strings.TrimPrefix(path, "/panel/api/users/")
	if i := strings.Index(v, "/"); i >= 0 {
		v = v[:i]
	}
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, strconv.ErrSyntax
	}
	return strconv.Atoi(v)
}

func parsePositiveInt(input string, def int) int {
	v := strings.TrimSpace(input)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func parseInt64(input string, def int64) int64 {
	v := strings.TrimSpace(input)
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return def
	}
	return n
}

func dedupeStrings(input []string) []string {
	if len(input) <= 1 {
		return input
	}
	m := make(map[string]struct{}, len(input))
	out := make([]string, 0, len(input))
	for _, v := range input {
		if _, ok := m[v]; ok {
			continue
		}
		m[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func dedupeInts(input []int) []int {
	if len(input) <= 1 {
		return input
	}
	m := make(map[int]struct{}, len(input))
	out := make([]int, 0, len(input))
	for _, v := range input {
		if _, ok := m[v]; ok {
			continue
		}
		m[v] = struct{}{}
		out = append(out, v)
	}
	sort.Ints(out)
	return out
}

func (s *Server) serveIndex(w http.ResponseWriter) {
	if s.assets == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	b, err := fs.ReadFile(s.assets, "index.html")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "index not found"})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}

func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	expected := strings.TrimSpace(s.cfg.Token)
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		t := strings.TrimSpace(extractToken(r))
		if !secureEqual(t, expected) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next(w, r)
	}
}

func (s *Server) withCORS(next http.HandlerFunc) http.HandlerFunc {
	allow := normalizeOrigins(s.cfg.AllowOrigins)
	return func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin != "" {
			if allowAllOrigins(allow) {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else if containsOrigin(allow, origin) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
			}
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Panel-Token")
		next(w, r)
	}
}

func normalizeOrigins(origins []string) []string {
	if len(origins) == 0 {
		return []string{"*"}
	}
	out := make([]string, 0, len(origins))
	for _, v := range origins {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		return []string{"*"}
	}
	return out
}

func containsOrigin(origins []string, value string) bool {
	for _, v := range origins {
		if v == value {
			return true
		}
	}
	return false
}

func allowAllOrigins(origins []string) bool {
	for _, v := range origins {
		if v == "*" {
			return true
		}
	}
	return false
}

func secureEqual(a, b string) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func extractToken(r *http.Request) string {
	v := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(v) > 7 && strings.EqualFold(v[:7], "Bearer ") {
		return strings.TrimSpace(v[7:])
	}
	v = strings.TrimSpace(r.Header.Get("X-Panel-Token"))
	if v != "" {
		return v
	}
	v = strings.TrimSpace(r.URL.Query().Get("token"))
	if v != "" {
		return v
	}
	return ""
}

func writeSSE(w http.ResponseWriter, event string, payload any) {
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_, _ = w.Write([]byte("event: " + event + "\n"))
	_, _ = w.Write([]byte("data: "))
	_, _ = w.Write(b)
	_, _ = w.Write([]byte("\n\n"))
}

func processRSSBytes() uint64 {
	b, err := os.ReadFile("/proc/self/statm")
	if err == nil {
		parts := strings.Fields(string(b))
		if len(parts) >= 2 {
			pages, perr := strconv.ParseUint(parts[1], 10, 64)
			if perr == nil {
				return pages * uint64(os.Getpagesize())
			}
		}
	}

	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		return 0
	}
	rssKB, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0
	}
	return rssKB * 1024
}

func writeJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}
