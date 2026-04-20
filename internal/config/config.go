package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultPort                   = 8080
	defaultReadTimeoutSeconds     = 10
	defaultWriteTimeoutSeconds    = 10
	defaultShutdownTimeoutSeconds = 10
	defaultStoreType              = "memory"
	defaultSQLitePath             = "./data/infohub.db"
	defaultLogLevel               = "info"
	defaultCollectorTimeout       = 15
)

type Config struct {
	Server     ServerConfig     `yaml:"server"`
	Collectors CollectorsConfig `yaml:"collectors"`
	Store      StoreConfig      `yaml:"store"`
	Log        LogConfig        `yaml:"log"`
}

type ServerConfig struct {
	Port                   int    `yaml:"port"`
	AuthToken              string `yaml:"auth_token"`
	ReadTimeoutSeconds     int    `yaml:"read_timeout_seconds"`
	WriteTimeoutSeconds    int    `yaml:"write_timeout_seconds"`
	ShutdownTimeoutSeconds int    `yaml:"shutdown_timeout_seconds"`
}

type CollectorsConfig struct {
	ClaudeRelay HTTPCollectorConfig   `yaml:"claude_relay"`
	Sub2API     HTTPCollectorConfig   `yaml:"sub2api"`
	Feishu      FeishuCollectorConfig `yaml:"feishu"`
}

type HTTPCollectorConfig struct {
	Enabled        bool              `yaml:"enabled"`
	Cron           string            `yaml:"cron"`
	BaseURL        string            `yaml:"base_url"`
	Endpoint       string            `yaml:"endpoint"`
	APIKey         string            `yaml:"api_key"`
	TimeoutSeconds int               `yaml:"timeout_seconds"`
	Headers        map[string]string `yaml:"headers"`
}

type FeishuCollectorConfig struct {
	Enabled        bool              `yaml:"enabled"`
	Cron           string            `yaml:"cron"`
	BaseURL        string            `yaml:"base_url"`
	Endpoint       string            `yaml:"endpoint"`
	AppID          string            `yaml:"app_id"`
	AppSecret      string            `yaml:"app_secret"`
	ProjectKey     string            `yaml:"project_key"`
	TimeoutSeconds int               `yaml:"timeout_seconds"`
	Headers        map[string]string `yaml:"headers"`
}

type StoreConfig struct {
	Type       string `yaml:"type"`
	SQLitePath string `yaml:"sqlite_path"`
}

type LogConfig struct {
	Level string `yaml:"level"`
}

type CollectorSchedule struct {
	Enabled bool
	Cron    string
	Timeout time.Duration
}

type ScheduleConfig map[string]CollectorSchedule

func Load(path string) (Config, error) {
	var cfg Config

	content, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("read config file: %w", err)
	}
	if err := yaml.Unmarshal(content, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config yaml: %w", err)
	}

	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return cfg, err
	}

	return cfg, nil
}

func (c Config) ScheduleConfig() ScheduleConfig {
	return ScheduleConfig{
		"claude_relay": {
			Enabled: c.Collectors.ClaudeRelay.Enabled,
			Cron:    c.Collectors.ClaudeRelay.Cron,
			Timeout: c.Collectors.ClaudeRelay.Timeout(),
		},
		"sub2api": {
			Enabled: c.Collectors.Sub2API.Enabled,
			Cron:    c.Collectors.Sub2API.Cron,
			Timeout: c.Collectors.Sub2API.Timeout(),
		},
		"feishu": {
			Enabled: c.Collectors.Feishu.Enabled,
			Cron:    c.Collectors.Feishu.Cron,
			Timeout: c.Collectors.Feishu.Timeout(),
		},
	}
}

func (s ServerConfig) Address() string {
	return fmt.Sprintf(":%d", s.Port)
}

func (s ServerConfig) ReadTimeout() time.Duration {
	return time.Duration(s.ReadTimeoutSeconds) * time.Second
}

func (s ServerConfig) WriteTimeout() time.Duration {
	return time.Duration(s.WriteTimeoutSeconds) * time.Second
}

func (s ServerConfig) ShutdownTimeout() time.Duration {
	return time.Duration(s.ShutdownTimeoutSeconds) * time.Second
}

func (c HTTPCollectorConfig) Timeout() time.Duration {
	return time.Duration(c.TimeoutSeconds) * time.Second
}

func (c FeishuCollectorConfig) Timeout() time.Duration {
	return time.Duration(c.TimeoutSeconds) * time.Second
}

func (c *Config) applyDefaults() {
	if c.Server.Port == 0 {
		c.Server.Port = defaultPort
	}
	if c.Server.ReadTimeoutSeconds == 0 {
		c.Server.ReadTimeoutSeconds = defaultReadTimeoutSeconds
	}
	if c.Server.WriteTimeoutSeconds == 0 {
		c.Server.WriteTimeoutSeconds = defaultWriteTimeoutSeconds
	}
	if c.Server.ShutdownTimeoutSeconds == 0 {
		c.Server.ShutdownTimeoutSeconds = defaultShutdownTimeoutSeconds
	}
	if c.Store.Type == "" {
		c.Store.Type = defaultStoreType
	}
	if c.Store.SQLitePath == "" {
		c.Store.SQLitePath = defaultSQLitePath
	}
	if c.Log.Level == "" {
		c.Log.Level = defaultLogLevel
	}

	c.Collectors.ClaudeRelay.applyDefaults()
	c.Collectors.Sub2API.applyDefaults()
	c.Collectors.Feishu.applyDefaults()
}

func (c Config) validate() error {
	switch strings.ToLower(c.Store.Type) {
	case "memory", "sqlite":
	default:
		return fmt.Errorf("unsupported store type %q", c.Store.Type)
	}

	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid server port %d", c.Server.Port)
	}

	return nil
}

func (c *HTTPCollectorConfig) applyDefaults() {
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = defaultCollectorTimeout
	}
	if c.Headers == nil {
		c.Headers = map[string]string{}
	}
}

func (c *FeishuCollectorConfig) applyDefaults() {
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = defaultCollectorTimeout
	}
	if c.BaseURL == "" {
		c.BaseURL = "https://open.feishu.cn"
	}
	if c.Headers == nil {
		c.Headers = map[string]string{}
	}
}

func ParseLogLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
