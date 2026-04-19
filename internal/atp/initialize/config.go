package initialize

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/viper"
)

const DefaultWatchInterval = 2 * time.Second

type Config struct {
	Server  ServerConfig  `mapstructure:"server"`
	Panel   PanelConfig   `mapstructure:"panel"`
	Policy  PolicyConfig  `mapstructure:"policy"`
	Runtime RuntimeConfig `mapstructure:"runtime"`
}

type ServerConfig struct {
	NodeID           int32         `mapstructure:"node_id"`
	Listen           string        `mapstructure:"listen"`
	Port             int           `mapstructure:"port"`
	Transport        string        `mapstructure:"transport"`
	HandshakeTimeout time.Duration `mapstructure:"handshake_timeout"`
	IdleTimeout      time.Duration `mapstructure:"idle_timeout"`
	ResumeTicketTTL  time.Duration `mapstructure:"resume_ticket_ttl"`
}

type PanelConfig struct {
	BaseURL      string          `mapstructure:"base_url"`
	MuKey        string          `mapstructure:"mu_key"`
	Timeout      time.Duration   `mapstructure:"timeout"`
	PullInterval PullInterval    `mapstructure:"pull_interval"`
	ReportEvery  ReportIntervals `mapstructure:"report_interval"`
}

type PullInterval struct {
	NodeInfo    time.Duration `mapstructure:"node_info"`
	Users       time.Duration `mapstructure:"users"`
	DetectRules time.Duration `mapstructure:"detect_rules"`
}

type ReportIntervals struct {
	Traffic    time.Duration `mapstructure:"traffic"`
	AliveIP    time.Duration `mapstructure:"alive_ip"`
	DetectLog  time.Duration `mapstructure:"detect_log"`
	NodeStatus time.Duration `mapstructure:"node_status"`
}

type PolicyConfig struct {
	MaxConnsPerUser       int           `mapstructure:"max_conns_per_user"`
	MaxOpenStreamsPerUser int           `mapstructure:"max_open_streams_per_user"`
	EnableAuditBlock      bool          `mapstructure:"enable_audit_block"`
	AuditBlockDuration    time.Duration `mapstructure:"audit_block_duration"`
}

type RuntimeConfig struct {
	Workers     int    `mapstructure:"workers"`
	PprofAddr   string `mapstructure:"pprof_addr"`
	MetricsAddr string `mapstructure:"metrics_addr"`
	LogLevel    string `mapstructure:"log_level"`
	LogJSON     bool   `mapstructure:"log_json"`
}

type ReloadEvent struct {
	Path      string
	Config    *Config
	Triggered time.Time
}

func LoadConfig(configPath string) (*Config, error) {
	v := viper.New()
	v.SetConfigType("yaml")
	v.SetEnvPrefix("ATP")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	setDefaults(v)

	if configPath != "" {
		v.SetConfigFile(configPath)
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("read config: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	if err := validateConfig(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("server.listen", "0.0.0.0")
	v.SetDefault("server.handshake_timeout", "10s")
	v.SetDefault("server.idle_timeout", "120s")
	v.SetDefault("server.resume_ticket_ttl", "15m")

	v.SetDefault("panel.timeout", "5s")
	v.SetDefault("panel.pull_interval.node_info", "30s")
	v.SetDefault("panel.pull_interval.users", "15s")
	v.SetDefault("panel.pull_interval.detect_rules", "30s")
	v.SetDefault("panel.report_interval.traffic", "10s")
	v.SetDefault("panel.report_interval.alive_ip", "20s")
	v.SetDefault("panel.report_interval.detect_log", "5s")
	v.SetDefault("panel.report_interval.node_status", "60s")

	v.SetDefault("policy.max_open_streams_per_user", 256)
	v.SetDefault("policy.enable_audit_block", true)
	v.SetDefault("policy.audit_block_duration", "10m")

	v.SetDefault("runtime.log_level", "info")
	v.SetDefault("runtime.log_json", true)
}

func validateConfig(cfg *Config) error {
	if cfg.Server.NodeID <= 0 {
		return fmt.Errorf("server.node_id must be > 0")
	}
	if cfg.Panel.BaseURL == "" {
		return fmt.Errorf("panel.base_url is required")
	}
	if cfg.Panel.MuKey == "" {
		return fmt.Errorf("panel.mu_key is required")
	}
	return nil
}

func WatchConfig(ctx context.Context, configPath string, interval time.Duration, onReload func(event *ReloadEvent)) error {
	if onReload == nil {
		return errors.New("onReload callback is required")
	}
	if configPath == "" {
		return nil
	}
	if interval <= 0 {
		interval = DefaultWatchInterval
	}

	lastMod, err := fileModTime(configPath)
	if err != nil {
		return fmt.Errorf("watch config stat failed: %w", err)
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				modTime, statErr := fileModTime(configPath)
				if statErr != nil {
					continue
				}
				if !modTime.After(lastMod) {
					continue
				}
				cfg, loadErr := LoadConfig(configPath)
				if loadErr != nil {
					continue
				}
				lastMod = modTime
				onReload(&ReloadEvent{Path: configPath, Config: cfg, Triggered: time.Now()})
			}
		}
	}()

	return nil
}

func fileModTime(path string) (time.Time, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	return fi.ModTime(), nil
}
