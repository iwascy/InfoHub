package collector

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"infohub/internal/config"
	"infohub/internal/store"
)

func newRemoteTestStore(t *testing.T) *store.SQLiteStore {
	t.Helper()
	dataStore, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "remote.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = dataStore.Close() })
	return dataStore
}

func TestLocalCollectorRemoteModeAggregatesAcrossMachines(t *testing.T) {
	dataStore := newRemoteTestStore(t)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	if err := dataStore.SaveIngestedUsage("mbp", "claude_local", nil, []store.LocalUsageRecord{
		{FilePath: "/mbp/a.jsonl", ByteOffset: 0, At: now.Add(-time.Hour), Model: "claude-sonnet-4-6", Input: 100, Output: 50, Quota5hUsed: -1, QuotaWeekUsed: -1},
	}); err != nil {
		t.Fatalf("SaveIngestedUsage mbp: %v", err)
	}
	if err := dataStore.SaveIngestedUsage("desk", "claude_local", nil, []store.LocalUsageRecord{
		{FilePath: "/desk/b.jsonl", ByteOffset: 0, At: now.Add(-2 * time.Hour), Model: "claude-opus-4-7", Input: 20, Output: 5, Quota5hUsed: -1, QuotaWeekUsed: -1},
	}); err != nil {
		t.Fatalf("SaveIngestedUsage desk: %v", err)
	}

	collector := NewClaudeLocalCollector(config.LocalCollectorConfig{
		Mode:  "remote",
		Quota: config.LocalQuotaConfig{FiveHourCap: 10},
	}, nil)
	collector.now = func() time.Time { return now }
	collector.SetStore(dataStore)

	items, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("collect failed: %v", err)
	}

	tokenItem := mustFindItem(t, items, "今日 Token 用量")
	if tokenItem.Value != "175" {
		t.Fatalf("expected cross-machine total 175, got %s", tokenItem.Value)
	}
}

func TestLocalCollectorRemoteModeRequiresStore(t *testing.T) {
	collector := NewClaudeLocalCollector(config.LocalCollectorConfig{Mode: "remote"}, nil)
	if _, err := collector.Collect(context.Background()); err == nil {
		t.Fatal("expected error when remote mode has no store")
	}
}

func TestStoreQuotaFetcherFreshAndStale(t *testing.T) {
	dataStore := newRemoteTestStore(t)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	if err := dataStore.SaveAgentQuotaObservation(store.AgentQuotaObservation{
		Machine: "mbp", Source: "claude_local",
		Quota5hUsed: 35, Quota5hReset: now.Add(3 * time.Hour).Format(time.RFC3339),
		QuotaWeekUsed: 60, QuotaWeekReset: now.Add(72 * time.Hour).Format(time.RFC3339),
		ObservedAt: now.Add(-10 * time.Minute),
	}); err != nil {
		t.Fatalf("SaveAgentQuotaObservation: %v", err)
	}

	fetcher := NewStoreQuotaFetcher(dataStore, "claude_local", 30*time.Minute)
	fetcher.now = func() time.Time { return now }

	limits, ok, err := fetcher.FetchRateLimits(context.Background())
	if err != nil || !ok {
		t.Fatalf("FetchRateLimits: ok=%v err=%v", ok, err)
	}
	if limits.FiveHour.UsedPercent != 35 || limits.Week.UsedPercent != 60 {
		t.Fatalf("unexpected limits: %+v", limits)
	}
	if fetcher.LastStatus() != "ok" {
		t.Fatalf("status = %q", fetcher.LastStatus())
	}

	// Stale observation must be ignored.
	staleFetcher := NewStoreQuotaFetcher(dataStore, "claude_local", 30*time.Minute)
	staleFetcher.now = func() time.Time { return now.Add(2 * time.Hour) }
	if _, ok, _ := staleFetcher.FetchRateLimits(context.Background()); ok {
		t.Fatal("expected stale observation to be rejected")
	}
	if staleFetcher.LastStatus() != "stale" {
		t.Fatalf("status = %q", staleFetcher.LastStatus())
	}
}

func TestRemoteModeUsesAgentQuotaObservation(t *testing.T) {
	dataStore := newRemoteTestStore(t)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	if err := dataStore.SaveIngestedUsage("mbp", "claude_local", nil, []store.LocalUsageRecord{
		{FilePath: "/mbp/a.jsonl", ByteOffset: 0, At: now.Add(-time.Hour), Model: "claude-sonnet-4-6", Input: 100, Output: 50, Quota5hUsed: -1, QuotaWeekUsed: -1},
	}); err != nil {
		t.Fatalf("SaveIngestedUsage: %v", err)
	}
	if err := dataStore.SaveAgentQuotaObservation(store.AgentQuotaObservation{
		Machine: "mbp", Source: "claude_local",
		Quota5hUsed: 44, Quota5hReset: now.Add(3 * time.Hour).Format(time.RFC3339),
		QuotaWeekUsed: 78, QuotaWeekReset: now.Add(72 * time.Hour).Format(time.RFC3339),
		ObservedAt: now.Add(-5 * time.Minute),
	}); err != nil {
		t.Fatalf("SaveAgentQuotaObservation: %v", err)
	}

	collector := NewClaudeLocalCollector(config.LocalCollectorConfig{
		Mode:  "remote",
		Quota: config.LocalQuotaConfig{FiveHourCap: 10},
	}, nil)
	collector.now = func() time.Time { return now }
	collector.SetStore(dataStore)
	fetcher := NewStoreQuotaFetcher(dataStore, "claude_local", 30*time.Minute)
	fetcher.now = func() time.Time { return now }
	collector.SetClaudeQuotaFetcher(fetcher)

	items, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("collect failed: %v", err)
	}

	quotaItem := mustFindItem(t, items, "账号 Claude Local 5H 额度")
	if quotaItem.Value != "56%" {
		t.Fatalf("expected remaining 56%%, got %s", quotaItem.Value)
	}
	if got := quotaItem.Extra["quota_source"]; got != "claude_oauth_usage" {
		t.Fatalf("quota_source = %v", got)
	}
	if got := quotaItem.Extra["approx"]; got != false {
		t.Fatalf("approx = %v", got)
	}
}

func TestBuiltinModeWithStoreFallsBackToIngestedRecordsAndEmitsProductUsage(t *testing.T) {
	dataStore := newRemoteTestStore(t)
	now := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)

	if err := dataStore.SaveIngestedUsage("mbp", "claude_local", nil, []store.LocalUsageRecord{
		{FilePath: "/mbp/week.jsonl", ByteOffset: 0, At: time.Date(2026, 4, 21, 8, 0, 0, 0, time.UTC), Model: "claude-sonnet-4-6", Input: 2000000, Output: 500000, Quota5hUsed: -1, QuotaWeekUsed: -1},
		{FilePath: "/mbp/today.jsonl", ByteOffset: 0, At: now.Add(-time.Hour), Model: "claude-sonnet-4-6", Input: 1000000, Output: 200000, Quota5hUsed: -1, QuotaWeekUsed: -1},
	}); err != nil {
		t.Fatalf("SaveIngestedUsage claude_local: %v", err)
	}
	if err := dataStore.SaveIngestedUsage("mbp", "codex_local", nil, []store.LocalUsageRecord{
		{FilePath: "/mbp/codex-week.jsonl", ByteOffset: 0, At: time.Date(2026, 4, 22, 8, 0, 0, 0, time.UTC), Model: "gpt-5-codex", Input: 3000000, Output: 1000000, Quota5hUsed: -1, QuotaWeekUsed: -1},
		{FilePath: "/mbp/codex-today.jsonl", ByteOffset: 0, At: now.Add(-time.Hour), Model: "gpt-5-codex", Input: 500000, Output: 100000, Quota5hUsed: -1, QuotaWeekUsed: -1},
	}); err != nil {
		t.Fatalf("SaveIngestedUsage codex_local: %v", err)
	}

	claudeCollector := NewClaudeLocalCollector(config.LocalCollectorConfig{
		Mode:  "builtin",
		Paths: []string{filepath.Join(t.TempDir(), "missing-claude")},
	}, nil)
	claudeCollector.now = func() time.Time { return now }
	claudeCollector.SetStore(dataStore)

	claudeItems, err := claudeCollector.Collect(context.Background())
	if err != nil {
		t.Fatalf("claude collect failed: %v", err)
	}
	if got := mustFindItem(t, claudeItems, "Claude Local 今日 Token 用量").Value; got != "1200000" {
		t.Fatalf("claude today product usage = %s, want 1200000", got)
	}
	if got := mustFindItem(t, claudeItems, "Claude Local 本周 Token 用量").Value; got != "3700000" {
		t.Fatalf("claude week product usage = %s, want 3700000", got)
	}

	codexCollector := NewCodexLocalCollector(config.LocalCollectorConfig{
		Mode:  "builtin",
		Paths: []string{filepath.Join(t.TempDir(), "missing-codex")},
	}, nil)
	codexCollector.now = func() time.Time { return now }
	codexCollector.SetStore(dataStore)

	codexItems, err := codexCollector.Collect(context.Background())
	if err != nil {
		t.Fatalf("codex collect failed: %v", err)
	}
	if got := mustFindItem(t, codexItems, "Codex Local 今日 Token 用量").Value; got != "600000" {
		t.Fatalf("codex today product usage = %s, want 600000", got)
	}
	if got := mustFindItem(t, codexItems, "Codex Local 本周 Token 用量").Value; got != "4600000" {
		t.Fatalf("codex week product usage = %s, want 4600000", got)
	}
}
