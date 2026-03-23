package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
	"go.uber.org/zap"
)

type Config struct {
	Node     NodeConfig     `mapstructure:"node"`
	API      APIConfig      `mapstructure:"api"`
	Sync     SyncConfig     `mapstructure:"sync"`
	RT       RuntimeConfig  `mapstructure:"runtime"`
	Debug    DebugConfig    `mapstructure:"debug"`
	Security SecurityConfig `mapstructure:"security"`
	Update   UpdateConfig   `mapstructure:"update"`
	Log      LogConfig      `mapstructure:"log"`
}

type DebugConfig struct {
	PPROFEnabled bool   `mapstructure:"pprof_enabled"`
	PPROFListen  string `mapstructure:"pprof_listen"`
}

type SecurityConfig struct {
	AutoBlock AutoBlockConfig `mapstructure:"auto_block"`
}

type AutoBlockConfig struct {
	Enabled         bool          `mapstructure:"enabled"`
	Backend         string        `mapstructure:"backend"`
	SyncInterval    time.Duration `mapstructure:"sync_interval"`
	ProtectNodeIP   bool          `mapstructure:"protect_node_ip"`
	StaticWhitelist []string      `mapstructure:"static_whitelist"`
}

type NodeConfig struct {
	ID                      int    `mapstructure:"id"`
	GetPortOffsetByNodeName bool   `mapstructure:"get_port_offset_by_node_name"`
	EnableMUHostRule        bool   `mapstructure:"enable_mu_host_rule"`
	MURegex                 string `mapstructure:"mu_regex"`
	MUSuffix                string `mapstructure:"mu_suffix"`
}

type APIConfig struct {
	Interface                string        `mapstructure:"interface"`
	URL                      string        `mapstructure:"url"`
	Token                    string        `mapstructure:"token"`
	Timeout                  time.Duration `mapstructure:"timeout"`
	RetryMax                 int           `mapstructure:"retry_max"`
	RetryBackoff             time.Duration `mapstructure:"retry_backoff"`
	RetryMaxBackoff          time.Duration `mapstructure:"retry_max_backoff"`
	TransportDialTimeout     time.Duration `mapstructure:"transport_dial_timeout"`
	TransportKeepAlive       time.Duration `mapstructure:"transport_keep_alive"`
	TransportMaxIdleConns    int           `mapstructure:"transport_max_idle_conns"`
	TransportMaxIdlePerHost  int           `mapstructure:"transport_max_idle_per_host"`
	TransportIdleConnTimeout time.Duration `mapstructure:"transport_idle_conn_timeout"`
	TransportTLSHandshake    time.Duration `mapstructure:"transport_tls_handshake_timeout"`
	TransportExpectContinue  time.Duration `mapstructure:"transport_expect_continue_timeout"`
}

type SyncConfig struct {
	UpdateInterval  time.Duration `mapstructure:"update_interval"`
	FailureBaseWait time.Duration `mapstructure:"failure_base_wait"`
	FailureMaxWait  time.Duration `mapstructure:"failure_max_wait"`
}

type LogConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
}

type RuntimeConfig struct {
	Driver                    string           `mapstructure:"driver"`
	ReconcileWorkers          int              `mapstructure:"reconcile_workers"`
	HandshakeMaxConcurrent    int              `mapstructure:"handshake_max_concurrent"`
	PerIPHandshakeMax         int              `mapstructure:"per_ip_handshake_max"`
	OnUnsupportedCipher       string           `mapstructure:"on_unsupported_cipher"`
	DialTimeout               time.Duration    `mapstructure:"dial_timeout"`
	DNSPreferIPv4             bool             `mapstructure:"dns_prefer_ipv4"`
	DNSResolver               string           `mapstructure:"dns_resolver"`
	MaxUDPSessionPerPort      int              `mapstructure:"max_udp_session_per_port"`
	MaxUDPResolveCacheEntries int              `mapstructure:"max_udp_resolve_cache_entries"`
	SwitchRule                SwitchRuleConfig `mapstructure:"switchrule"`
}

type SwitchRuleConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Mode    string `mapstructure:"mode"`
	Expr    string `mapstructure:"expr"`
}

type UpdateConfig struct {
	Enabled         bool          `mapstructure:"enabled"`
	Repository      string        `mapstructure:"repository"`
	CheckInterval   time.Duration `mapstructure:"check_interval"`
	Timeout         time.Duration `mapstructure:"timeout"`
	AllowPrerelease bool          `mapstructure:"allow_prerelease"`
}

func Load(path string) (Config, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")
	setDefaults(v)

	if err := v.ReadInConfig(); err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return Config{}, fmt.Errorf("unmarshal config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.Node.ID <= 0 {
		return fmt.Errorf("node.id must be > 0")
	}
	if strings.TrimSpace(c.API.URL) == "" {
		return fmt.Errorf("api.url is required")
	}
	if strings.TrimSpace(c.API.Token) == "" {
		return fmt.Errorf("api.token is required")
	}
	if c.Sync.UpdateInterval <= 0 {
		return fmt.Errorf("sync.update_interval must be > 0")
	}
	if c.API.Timeout <= 0 {
		return fmt.Errorf("api.timeout must be > 0")
	}
	if c.API.RetryMax < 0 {
		return fmt.Errorf("api.retry_max must be >= 0")
	}
	if c.API.RetryBackoff <= 0 {
		return fmt.Errorf("api.retry_backoff must be > 0")
	}
	if c.API.RetryMaxBackoff < c.API.RetryBackoff {
		return fmt.Errorf("api.retry_max_backoff must be >= api.retry_backoff")
	}
	if c.API.TransportDialTimeout <= 0 {
		return fmt.Errorf("api.transport_dial_timeout must be > 0")
	}
	if c.API.TransportKeepAlive <= 0 {
		return fmt.Errorf("api.transport_keep_alive must be > 0")
	}
	if c.API.TransportMaxIdleConns <= 0 {
		return fmt.Errorf("api.transport_max_idle_conns must be > 0")
	}
	if c.API.TransportMaxIdlePerHost <= 0 {
		return fmt.Errorf("api.transport_max_idle_per_host must be > 0")
	}
	if c.API.TransportIdleConnTimeout <= 0 {
		return fmt.Errorf("api.transport_idle_conn_timeout must be > 0")
	}
	if c.API.TransportTLSHandshake <= 0 {
		return fmt.Errorf("api.transport_tls_handshake_timeout must be > 0")
	}
	if c.API.TransportExpectContinue <= 0 {
		return fmt.Errorf("api.transport_expect_continue_timeout must be > 0")
	}
	if c.Sync.FailureBaseWait <= 0 {
		return fmt.Errorf("sync.failure_base_wait must be > 0")
	}
	if c.Sync.FailureMaxWait < c.Sync.FailureBaseWait {
		return fmt.Errorf("sync.failure_max_wait must be >= sync.failure_base_wait")
	}
	if c.RT.ReconcileWorkers < 0 {
		return fmt.Errorf("runtime.reconcile_workers must be >= 0")
	}
	if c.RT.HandshakeMaxConcurrent < 0 {
		return fmt.Errorf("runtime.handshake_max_concurrent must be >= 0")
	}
	if c.RT.PerIPHandshakeMax < 0 {
		return fmt.Errorf("runtime.per_ip_handshake_max must be >= 0")
	}
	switch strings.ToLower(strings.TrimSpace(c.RT.OnUnsupportedCipher)) {
	case "", "skip", "fail":
	default:
		return fmt.Errorf("runtime.on_unsupported_cipher must be skip or fail")
	}
	if c.RT.DialTimeout <= 0 {
		return fmt.Errorf("runtime.dial_timeout must be > 0")
	}
	if c.RT.MaxUDPSessionPerPort <= 0 {
		return fmt.Errorf("runtime.max_udp_session_per_port must be > 0")
	}
	if c.RT.MaxUDPResolveCacheEntries <= 0 {
		return fmt.Errorf("runtime.max_udp_resolve_cache_entries must be > 0")
	}
	switch strings.ToLower(strings.TrimSpace(c.RT.SwitchRule.Mode)) {
	case "", "none", "expr":
	default:
		return fmt.Errorf("runtime.switchrule.mode must be none or expr")
	}
	if strings.ToLower(strings.TrimSpace(c.RT.SwitchRule.Mode)) == "expr" && strings.TrimSpace(c.RT.SwitchRule.Expr) == "" {
		return fmt.Errorf("runtime.switchrule.expr is required when runtime.switchrule.mode=expr")
	}
	if c.Security.AutoBlock.Enabled {
		if c.Security.AutoBlock.SyncInterval <= 0 {
			return fmt.Errorf("security.auto_block.sync_interval must be > 0")
		}
	}
	if c.Update.Enabled {
		if strings.TrimSpace(c.Update.Repository) == "" {
			return fmt.Errorf("update.repository is required when update.enabled=true")
		}
		if !strings.Contains(c.Update.Repository, "/") {
			return fmt.Errorf("update.repository must be in owner/repo format")
		}
		if c.Update.CheckInterval <= 0 {
			return fmt.Errorf("update.check_interval must be > 0")
		}
		if c.Update.Timeout <= 0 {
			return fmt.Errorf("update.timeout must be > 0")
		}
	}
	return nil
}

func Fields(c Config) []zap.Field {
	return []zap.Field{
		zap.Int("node_id", c.Node.ID),
		zap.String("api_interface", c.API.Interface),
		zap.String("api_url", c.API.URL),
		zap.Duration("update_interval", c.Sync.UpdateInterval),
		zap.Int("api_retry_max", c.API.RetryMax),
		zap.String("runtime_driver", c.RT.Driver),
		zap.Int("runtime_workers", c.RT.ReconcileWorkers),
	}
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("node.id", 119)
	v.SetDefault("node.get_port_offset_by_node_name", true)
	v.SetDefault("node.enable_mu_host_rule", true)
	v.SetDefault("node.mu_regex", "%5m%id.%suffix")
	v.SetDefault("node.mu_suffix", "example.com")

	v.SetDefault("api.interface", "modwebapi")
	v.SetDefault("api.url", "https://118.128.151.30")
	v.SetDefault("api.token", "")
	v.SetDefault("api.timeout", "10s")
	v.SetDefault("api.retry_max", 2)
	v.SetDefault("api.retry_backoff", "500ms")
	v.SetDefault("api.retry_max_backoff", "5s")
	v.SetDefault("api.transport_dial_timeout", "5s")
	v.SetDefault("api.transport_keep_alive", "30s")
	v.SetDefault("api.transport_max_idle_conns", 200)
	v.SetDefault("api.transport_max_idle_per_host", 64)
	v.SetDefault("api.transport_idle_conn_timeout", "90s")
	v.SetDefault("api.transport_tls_handshake_timeout", "5s")
	v.SetDefault("api.transport_expect_continue_timeout", "1s")

	v.SetDefault("sync.update_interval", "60s")
	v.SetDefault("sync.failure_base_wait", "3s")
	v.SetDefault("sync.failure_max_wait", "60s")

	v.SetDefault("runtime.driver", "mock")
	v.SetDefault("runtime.reconcile_workers", 0)
	v.SetDefault("runtime.handshake_max_concurrent", 0)
	v.SetDefault("runtime.per_ip_handshake_max", 0)
	v.SetDefault("runtime.on_unsupported_cipher", "skip")
	v.SetDefault("runtime.dial_timeout", "8s")
	v.SetDefault("runtime.dns_prefer_ipv4", false)
	v.SetDefault("runtime.dns_resolver", "")
	v.SetDefault("runtime.max_udp_session_per_port", 2048)
	v.SetDefault("runtime.max_udp_resolve_cache_entries", 4096)
	v.SetDefault("runtime.switchrule.enabled", false)
	v.SetDefault("runtime.switchrule.mode", "none")
	v.SetDefault("runtime.switchrule.expr", "")

	v.SetDefault("security.auto_block.enabled", false)
	v.SetDefault("security.auto_block.backend", "noop")
	v.SetDefault("security.auto_block.sync_interval", "60s")
	v.SetDefault("security.auto_block.protect_node_ip", true)
	v.SetDefault("security.auto_block.static_whitelist", []string{"127.0.0.1", "::1"})

	v.SetDefault("debug.pprof_enabled", false)
	v.SetDefault("debug.pprof_listen", "127.0.0.1:6060")

	v.SetDefault("update.enabled", false)
	v.SetDefault("update.repository", "jashok5/shadowsocks-go")
	v.SetDefault("update.check_interval", "1h")
	v.SetDefault("update.timeout", "30s")
	v.SetDefault("update.allow_prerelease", false)

	v.SetDefault("log.level", "info")
	v.SetDefault("log.format", "console")
}
