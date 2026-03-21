package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
	"go.uber.org/zap"
)

type Config struct {
	Node   NodeConfig    `mapstructure:"node"`
	API    APIConfig     `mapstructure:"api"`
	Sync   SyncConfig    `mapstructure:"sync"`
	RT     RuntimeConfig `mapstructure:"runtime"`
	Update UpdateConfig  `mapstructure:"update"`
	Log    LogConfig     `mapstructure:"log"`
}

type NodeConfig struct {
	ID                      int  `mapstructure:"id"`
	GetPortOffsetByNodeName bool `mapstructure:"get_port_offset_by_node_name"`
}

type APIConfig struct {
	Interface       string        `mapstructure:"interface"`
	URL             string        `mapstructure:"url"`
	Token           string        `mapstructure:"token"`
	Timeout         time.Duration `mapstructure:"timeout"`
	RetryMax        int           `mapstructure:"retry_max"`
	RetryBackoff    time.Duration `mapstructure:"retry_backoff"`
	RetryMaxBackoff time.Duration `mapstructure:"retry_max_backoff"`
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
	Driver           string `mapstructure:"driver"`
	ReconcileWorkers int    `mapstructure:"reconcile_workers"`
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
	if c.Sync.FailureBaseWait <= 0 {
		return fmt.Errorf("sync.failure_base_wait must be > 0")
	}
	if c.Sync.FailureMaxWait < c.Sync.FailureBaseWait {
		return fmt.Errorf("sync.failure_max_wait must be >= sync.failure_base_wait")
	}
	if c.RT.ReconcileWorkers <= 0 {
		return fmt.Errorf("runtime.reconcile_workers must be > 0")
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

	v.SetDefault("api.interface", "modwebapi")
	v.SetDefault("api.url", "https://118.128.151.30")
	v.SetDefault("api.token", "")
	v.SetDefault("api.timeout", "10s")
	v.SetDefault("api.retry_max", 2)
	v.SetDefault("api.retry_backoff", "500ms")
	v.SetDefault("api.retry_max_backoff", "5s")

	v.SetDefault("sync.update_interval", "60s")
	v.SetDefault("sync.failure_base_wait", "3s")
	v.SetDefault("sync.failure_max_wait", "60s")

	v.SetDefault("runtime.driver", "mock")
	v.SetDefault("runtime.reconcile_workers", 8)

	v.SetDefault("update.enabled", false)
	v.SetDefault("update.repository", "jashok5/shadowsocks-go")
	v.SetDefault("update.check_interval", "1h")
	v.SetDefault("update.timeout", "30s")
	v.SetDefault("update.allow_prerelease", false)

	v.SetDefault("log.level", "info")
	v.SetDefault("log.format", "console")
}
