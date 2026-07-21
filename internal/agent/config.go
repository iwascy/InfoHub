package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"infohub/internal/config"
)

const (
	defaultIntervalSeconds = 120
	defaultPushTimeout     = 15
	defaultClaudeQuotaTime = 8
)

type Config struct {
	Server          ServerConfig                  `yaml:"server"`
	MachineID       string                        `yaml:"machine_id"`
	IntervalSeconds int                           `yaml:"interval_seconds"`
	StatePath       string                        `yaml:"state_path"`
	Sources         map[string]SourceConfig       `yaml:"sources"`
	ClaudeQuota     config.LocalCodexOnlineConfig `yaml:"claude_quota"`
	CodexQuota      config.LocalCodexOnlineConfig `yaml:"codex_quota"`
	Log             config.LogConfig              `yaml:"log"`
}

type ServerConfig struct {
	BaseURL        string `yaml:"base_url"`
	IngestToken    string `yaml:"ingest_token"`
	TimeoutSeconds int    `yaml:"timeout_seconds"`
}

type SourceConfig struct {
	Enabled bool     `yaml:"enabled"`
	Paths   []string `yaml:"paths"`
}

func (c Config) Interval() time.Duration {
	return time.Duration(c.IntervalSeconds) * time.Second
}

func (s ServerConfig) Timeout() time.Duration {
	return time.Duration(s.TimeoutSeconds) * time.Second
}

func LoadConfig(path string) (Config, error) {
	cfg, err := parseConfig(path)
	if err != nil {
		return cfg, err
	}
	if err := cfg.validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// LoadPrintConfig loads configuration for local print mode, where no server
// is contacted: the config file is optional (defaults apply, with online
// quota lookups enabled) and server.base_url is not required.
func LoadPrintConfig(path string) (Config, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		var cfg Config
		cfg.ClaudeQuota.Enabled = true
		cfg.CodexQuota.Enabled = true
		cfg.applyDefaults()
		return cfg, nil
	}

	cfg, err := parseConfig(path)
	if err != nil {
		return cfg, err
	}
	if err := cfg.validateSources(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func parseConfig(path string) (Config, error) {
	var cfg Config

	if err := config.LoadDotEnv(path); err != nil {
		return cfg, err
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("read agent config: %w", err)
	}
	expanded := os.ExpandEnv(string(content))
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return cfg, fmt.Errorf("parse agent config yaml: %w", err)
	}

	cfg.applyDefaults()
	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.MachineID == "" {
		if hostname, err := os.Hostname(); err == nil {
			c.MachineID = hostname
		}
	}
	if c.IntervalSeconds == 0 {
		c.IntervalSeconds = defaultIntervalSeconds
	}
	if c.Server.TimeoutSeconds == 0 {
		c.Server.TimeoutSeconds = defaultPushTimeout
	}
	if c.StatePath == "" {
		c.StatePath = defaultStatePath()
	}
	if c.Sources == nil {
		c.Sources = map[string]SourceConfig{}
	}
	if _, ok := c.Sources["claude_local"]; !ok {
		c.Sources["claude_local"] = SourceConfig{Enabled: true}
	}
	if _, ok := c.Sources["codex_local"]; !ok {
		c.Sources["codex_local"] = SourceConfig{Enabled: true}
	}
	if source := c.Sources["claude_local"]; len(source.Paths) == 0 {
		source.Paths = []string{
			"${HOME}/.config/claude/projects",
			"${HOME}/.claude/projects",
		}
		c.Sources["claude_local"] = source
	}
	if source := c.Sources["codex_local"]; len(source.Paths) == 0 {
		source.Paths = []string{"${HOME}/.codex/sessions"}
		c.Sources["codex_local"] = source
	}
	if c.ClaudeQuota.TimeoutSeconds == 0 {
		c.ClaudeQuota.TimeoutSeconds = defaultClaudeQuotaTime
	}
	if c.CodexQuota.TimeoutSeconds == 0 {
		c.CodexQuota.TimeoutSeconds = defaultClaudeQuotaTime
	}
}

func (c Config) validate() error {
	if strings.TrimSpace(c.Server.BaseURL) == "" {
		return fmt.Errorf("server.base_url is required")
	}
	if strings.TrimSpace(c.MachineID) == "" {
		return fmt.Errorf("machine_id is required (hostname lookup failed)")
	}
	return c.validateSources()
}

func (c Config) validateSources() error {
	for name := range c.Sources {
		if name != "claude_local" && name != "codex_local" {
			return fmt.Errorf("unsupported source %q (claude_local / codex_local)", name)
		}
	}
	return nil
}

func defaultStatePath() string {
	if stateHome := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); stateHome != "" {
		return filepath.Join(stateHome, "infohub-agent", "state.json")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "state", "infohub-agent", "state.json")
	}
	return filepath.Join(".", "infohub-agent-state.json")
}
