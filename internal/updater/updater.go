package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/jashok5/shadowsocks-go/internal/config"
	"go.yaml.in/yaml/v3"

	"go.uber.org/zap"
)

type Updater struct {
	cfg        config.UpdateConfig
	log        *zap.Logger
	httpClient *http.Client
	execPath   string
	configPath string
	owner      string
	repo       string
	goos       string
	goarch     string
}

type Result struct {
	LatestVersion  string
	DownloadedFile string
	Updated        bool
	RestartNeeded  bool
}

type githubRelease struct {
	TagName    string               `json:"tag_name"`
	Prerelease bool                 `json:"prerelease"`
	Draft      bool                 `json:"draft"`
	Assets     []githubReleaseAsset `json:"assets"`
}

type githubReleaseAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

func New(cfg config.UpdateConfig, log *zap.Logger, configPath string) (*Updater, error) {
	repo := strings.TrimSpace(cfg.Repository)
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return nil, fmt.Errorf("invalid repository format: %q", repo)
	}
	execPath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve executable path: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(execPath); err == nil {
		execPath = resolved
	}
	return &Updater{
		cfg:        cfg,
		log:        log,
		httpClient: &http.Client{Timeout: cfg.Timeout},
		execPath:   execPath,
		configPath: strings.TrimSpace(configPath),
		owner:      strings.TrimSpace(parts[0]),
		repo:       strings.TrimSpace(parts[1]),
		goos:       runtime.GOOS,
		goarch:     runtime.GOARCH,
	}, nil
}

func (u *Updater) CheckAndUpdate(ctx context.Context, currentVersion string) (Result, error) {
	current, err := parseVersion(currentVersion)
	if err != nil {
		return Result{}, fmt.Errorf("parse current version: %w", err)
	}
	rel, err := u.fetchRelease(ctx)
	if err != nil {
		return Result{}, err
	}
	latest, err := parseVersion(rel.TagName)
	if err != nil {
		return Result{}, fmt.Errorf("parse latest version %q: %w", rel.TagName, err)
	}
	if compareVersion(latest, current) <= 0 {
		return Result{}, nil
	}

	binAsset, ok := selectBinaryAsset(rel.Assets, u.goos, u.goarch)
	if !ok {
		return Result{}, fmt.Errorf("release %s has no asset for %s/%s", rel.TagName, u.goos, u.goarch)
	}
	checksumAsset, ok := findAsset(rel.Assets, "checksums.txt")
	if !ok {
		return Result{}, fmt.Errorf("release %s missing checksums.txt", rel.TagName)
	}

	stagedDir := filepath.Join(filepath.Dir(u.execPath), ".update", rel.TagName)
	if err := os.MkdirAll(stagedDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("create update dir: %w", err)
	}
	stagedBin := filepath.Join(stagedDir, binAsset.Name)
	if err := u.downloadToFile(ctx, binAsset.URL, stagedBin, 0o755); err != nil {
		return Result{}, fmt.Errorf("download binary: %w", err)
	}
	stagedChecksum := filepath.Join(stagedDir, checksumAsset.Name)
	if err := u.downloadToFile(ctx, checksumAsset.URL, stagedChecksum, 0o644); err != nil {
		return Result{}, fmt.Errorf("download checksums: %w", err)
	}
	if err := validateChecksum(stagedBin, stagedChecksum, binAsset.Name); err != nil {
		return Result{}, fmt.Errorf("validate checksum: %w", err)
	}

	if u.goos == "windows" {
		u.log.Info("update package downloaded on windows; replace is skipped", zap.String("file", stagedBin), zap.String("latest", rel.TagName))
		return Result{LatestVersion: rel.TagName, DownloadedFile: stagedBin}, nil
	}
	if u.goos != "linux" {
		return Result{}, fmt.Errorf("auto replace unsupported on %s", u.goos)
	}
	if err := u.syncConfigWithRelease(ctx, rel.TagName, stagedDir); err != nil {
		return Result{}, fmt.Errorf("sync config with release: %w", err)
	}
	if err := replaceBinaryLinux(u.execPath, stagedBin); err != nil {
		return Result{}, err
	}
	return Result{LatestVersion: rel.TagName, DownloadedFile: stagedBin, Updated: true, RestartNeeded: true}, nil
}

func (u *Updater) syncConfigWithRelease(ctx context.Context, tag string, stagedDir string) error {
	if u.configPath == "" {
		return nil
	}
	url := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/configs/config.example.yaml", u.owner, u.repo, tag)
	stagedExample := filepath.Join(stagedDir, "config.example.yaml")
	if err := u.downloadToFile(ctx, url, stagedExample, 0o644); err != nil {
		return fmt.Errorf("download config example: %w", err)
	}
	if err := mergeConfigFile(u.configPath, stagedExample); err != nil {
		return err
	}
	u.log.Info("config synced from release example", zap.String("tag", tag), zap.String("config", u.configPath))
	return nil
}

func (u *Updater) fetchRelease(ctx context.Context) (githubRelease, error) {
	path := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases", u.owner, u.repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return githubRelease{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "shadowsocks-go-updater")

	resp, err := u.httpClient.Do(req)
	if err != nil {
		return githubRelease{}, fmt.Errorf("request github release api: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return githubRelease{}, fmt.Errorf("github api status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var releases []githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return githubRelease{}, fmt.Errorf("decode github releases: %w", err)
	}
	for _, rel := range releases {
		if rel.Draft {
			continue
		}
		if rel.Prerelease && !u.cfg.AllowPrerelease {
			continue
		}
		return rel, nil
	}
	return githubRelease{}, fmt.Errorf("no eligible release found")
}

func (u *Updater) downloadToFile(ctx context.Context, url string, dst string, mode os.FileMode) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "shadowsocks-go-updater")

	resp, err := u.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("download failed status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	tmp := dst + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err = io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err = f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err = os.Chmod(tmp, mode); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err = os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func validateChecksum(binPath string, checksumPath string, assetName string) error {
	b, err := os.ReadFile(checksumPath)
	if err != nil {
		return err
	}
	checksums := parseChecksums(string(b))
	want, ok := checksums[assetName]
	if !ok {
		return fmt.Errorf("checksum not found for %s", assetName)
	}
	f, err := os.Open(binPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("sha256 mismatch for %s: want=%s got=%s", assetName, want, got)
	}
	return nil
}

func replaceBinaryLinux(currentPath string, newPath string) error {
	backupPath := currentPath + ".bak"
	_ = os.Remove(backupPath)
	if err := os.Rename(currentPath, backupPath); err != nil {
		return fmt.Errorf("backup current binary: %w", err)
	}
	if err := os.Rename(newPath, currentPath); err != nil {
		_ = os.Rename(backupPath, currentPath)
		return fmt.Errorf("activate new binary: %w", err)
	}
	if err := os.Chmod(currentPath, 0o755); err != nil {
		return fmt.Errorf("set executable mode: %w", err)
	}
	return nil
}

func parseChecksums(content string) map[string]string {
	out := make(map[string]string)
	for line := range strings.SplitSeq(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		out[name] = strings.ToLower(fields[0])
	}
	return out
}

func findAsset(assets []githubReleaseAsset, name string) (githubReleaseAsset, bool) {
	for _, a := range assets {
		if a.Name == name {
			return a, true
		}
	}
	return githubReleaseAsset{}, false
}

func selectBinaryAsset(assets []githubReleaseAsset, goos string, goarch string) (githubReleaseAsset, bool) {
	base := fmt.Sprintf("node_%s_%s", goos, goarch)
	candidates := []string{base}
	if goos == "windows" {
		candidates = append(candidates, base+".exe")
	}
	for _, candidate := range candidates {
		if asset, ok := findAsset(assets, candidate); ok {
			return asset, true
		}
	}
	return githubReleaseAsset{}, false
}

type semver struct {
	major int
	minor int
	patch int
	pre   string
}

func parseVersion(raw string) (semver, error) {
	v := strings.TrimSpace(raw)
	v = strings.TrimPrefix(v, "v")
	main := v
	pre := ""
	if before, after, ok := strings.Cut(v, "-"); ok {
		main = before
		pre = after
	}
	parts := strings.Split(main, ".")
	if len(parts) != 3 {
		return semver{}, fmt.Errorf("invalid version: %q", raw)
	}
	maj, err := strconv.Atoi(parts[0])
	if err != nil {
		return semver{}, fmt.Errorf("invalid major: %w", err)
	}
	atoi, err := strconv.Atoi(parts[1])
	if err != nil {
		return semver{}, fmt.Errorf("invalid minor: %w", err)
	}
	pat, err := strconv.Atoi(parts[2])
	if err != nil {
		return semver{}, fmt.Errorf("invalid patch: %w", err)
	}
	return semver{major: maj, minor: atoi, patch: pat, pre: pre}, nil
}

func compareVersion(a semver, b semver) int {
	if a.major != b.major {
		if a.major > b.major {
			return 1
		}
		return -1
	}
	if a.minor != b.minor {
		if a.minor > b.minor {
			return 1
		}
		return -1
	}
	if a.patch != b.patch {
		if a.patch > b.patch {
			return 1
		}
		return -1
	}
	if a.pre == b.pre {
		return 0
	}
	if a.pre == "" {
		return 1
	}
	if b.pre == "" {
		return -1
	}
	if a.pre > b.pre {
		return 1
	}
	return -1
}

func mergeConfigFile(currentPath string, newExamplePath string) error {
	baseContent, err := os.ReadFile(newExamplePath)
	if err != nil {
		return fmt.Errorf("read new config example: %w", err)
	}

	mode := os.FileMode(0o644)
	var overrideContent []byte
	if st, statErr := os.Stat(currentPath); statErr == nil {
		mode = st.Mode().Perm()
		b, readErr := os.ReadFile(currentPath)
		if readErr != nil {
			return fmt.Errorf("read current config: %w", readErr)
		}
		overrideContent = b
	} else if !os.IsNotExist(statErr) {
		return fmt.Errorf("stat current config: %w", statErr)
	}

	merged, err := mergeYAML(baseContent, overrideContent)
	if err != nil {
		return err
	}

	tmp := currentPath + ".tmp"
	if err := os.WriteFile(tmp, merged, mode); err != nil {
		return fmt.Errorf("write merged config tmp: %w", err)
	}
	if _, err := config.Load(tmp); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("validate merged config: %w", err)
	}

	if _, err := os.Stat(currentPath); err == nil {
		backup := currentPath + ".bak"
		_ = os.Remove(backup)
		if err := copyFile(currentPath, backup, mode); err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("backup current config: %w", err)
		}
	}

	if err := os.Rename(tmp, currentPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("activate merged config: %w", err)
	}
	return nil
}

func mergeYAML(baseContent []byte, overrideContent []byte) ([]byte, error) {
	base, err := decodeYAMLMap(baseContent)
	if err != nil {
		return nil, fmt.Errorf("decode new config example: %w", err)
	}
	override := make(map[string]any)
	if len(strings.TrimSpace(string(overrideContent))) > 0 {
		override, err = decodeYAMLMap(overrideContent)
		if err != nil {
			return nil, fmt.Errorf("decode current config: %w", err)
		}
	}
	merged := base
	preservePaths := [][]string{
		{"node", "id"},
		{"api", "url"},
		{"api", "token"},
		{"panel", "token"},
	}
	for _, p := range preservePaths {
		if v, ok := getMapPath(override, p); ok {
			setMapPath(merged, p, v)
		}
	}
	out, err := yaml.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("marshal merged config: %w", err)
	}
	return out, nil
}

func decodeYAMLMap(content []byte) (map[string]any, error) {
	out := make(map[string]any)
	if len(strings.TrimSpace(string(content))) == 0 {
		return out, nil
	}
	if err := yaml.Unmarshal(content, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func getMapPath(root map[string]any, path []string) (any, bool) {
	if len(path) == 0 {
		return nil, false
	}
	cur := root
	for i := 0; i < len(path)-1; i++ {
		next, ok := cur[path[i]]
		if !ok {
			return nil, false
		}
		nextMap, ok := next.(map[string]any)
		if !ok {
			return nil, false
		}
		cur = nextMap
	}
	v, ok := cur[path[len(path)-1]]
	return v, ok
}

func setMapPath(root map[string]any, path []string, value any) {
	if len(path) == 0 {
		return
	}
	cur := root
	for i := 0; i < len(path)-1; i++ {
		next, ok := cur[path[i]]
		if !ok {
			nm := make(map[string]any)
			cur[path[i]] = nm
			cur = nm
			continue
		}
		nextMap, ok := next.(map[string]any)
		if !ok {
			nm := make(map[string]any)
			cur[path[i]] = nm
			cur = nm
			continue
		}
		cur = nextMap
	}
	cur[path[len(path)-1]] = value
}

func copyFile(src string, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	return nil
}
