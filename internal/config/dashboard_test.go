package config

import "testing"

func TestDashboardTemplateConfig(t *testing.T) {
	cfg := Config{}
	cfg.applyDefaults()
	if cfg.Dashboard.Template != "trio" {
		t.Fatal("default template must be trio")
	}
	for _, name := range []string{"codex", "deepseek-codex", "trio"} {
		cfg.Dashboard.Template = name
		if err := cfg.validate(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	cfg.Dashboard.Template = "../invalid"
	if err := cfg.validate(); err == nil {
		t.Fatal("invalid template accepted")
	}
}

func TestCPACollectorConfig(t *testing.T) {
	cfg := Config{}
	cfg.Collectors.CPA.Enabled = true
	cfg.applyDefaults()
	if cfg.Collectors.CPA.Cron != "@every 30s" || cfg.Collectors.CPA.Provider != "claude" || cfg.Collectors.CPA.QuotaIntervalSeconds != 300 {
		t.Fatalf("unexpected defaults: %+v", cfg.Collectors.CPA)
	}
	if err := cfg.validate(); err == nil {
		t.Fatal("cpa without base_url accepted")
	}
	cfg.Collectors.CPA.BaseURL = "https://cpa.example.com"
	cfg.Collectors.CPA.ManagementKey = "key"
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	cfg.Store.Type = "memory"
	if err := cfg.validate(); err == nil {
		t.Fatal("cpa with memory store accepted")
	}
}
