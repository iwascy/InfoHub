package collector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"infohub/internal/config"
	"infohub/internal/model"
	"infohub/internal/store"
)

type fakeCPA struct {
	mu         sync.Mutex
	queue      []string
	quotaCalls int
	quotaFail  bool
}

func (f *fakeCPA) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.URL.Path {
		case "/v0/management/auth-files":
			_, _ = w.Write([]byte(`{"files":[
				{"auth_index":"codex1","provider":"codex","email":"x@example.com"},
				{"auth_index":"off","provider":"claude","disabled":true},
				{"auth_index":"c1","provider":"claude","email":"pro@example.com","status":"active"}]}`))
		case "/v0/management/api-call":
			var body struct {
				AuthIndex string            `json:"auth_index"`
				URL       string            `json:"url"`
				Header    map[string]string `json:"header"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.AuthIndex != "c1" || body.URL != cpaClaudeUsageURL || body.Header["Authorization"] != "Bearer $TOKEN$" {
				t.Errorf("unexpected api-call request: %+v", body)
			}
			f.quotaCalls++
			if f.quotaFail {
				_, _ = w.Write([]byte(`{"status_code":429,"body":"{}"}`))
				return
			}
			upstream := `{"five_hour":{"utilization":5,"resets_at":"2026-10-20T13:20:00+00:00"},"seven_day":{"utilization":17,"resets_at":"2026-10-26T07:00:00+00:00"},"limits":[],"seven_day_opus":null}`
			encoded, _ := json.Marshal(map[string]any{"status_code": 200, "body": upstream})
			_, _ = w.Write(encoded)
		case "/v0/management/usage-queue":
			if r.URL.Query().Get("count") == "" {
				t.Errorf("usage-queue without count")
			}
			batch := f.queue
			f.queue = nil
			_, _ = w.Write([]byte("[" + strings.Join(batch, ",") + "]"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func cpaRecord(id, provider, at string, total int) string {
	return `{"timestamp":"` + at + `","provider":"` + provider + `","model":"claude-x","auth_index":"c1","request_id":"` + id + `","tokens":{"input_tokens":1,"output_tokens":1,"total_tokens":` + strings.TrimSpace(formatFloat(float64(total))) + `}}`
}

func newTestCPACollector(t *testing.T, fake *fakeCPA) (*CPACollector, *store.SQLiteStore) {
	t.Helper()
	server := httptest.NewServer(fake.handler(t))
	t.Cleanup(server.Close)
	dataStore := newRemoteTestStore(t)
	cfg := config.CPACollectorConfig{
		BaseURL: server.URL, ManagementKey: "secret", TimeoutSeconds: 5,
		Provider: "claude", DisplayName: "Claude Pro", QuotaIntervalSeconds: 300,
	}
	return NewCPACollector(cfg, dataStore, nil), dataStore
}

func TestCPACollectorQuotaAndUsageCache(t *testing.T) {
	now := time.Date(2026, 10, 20, 10, 0, 0, 0, model.ShanghaiLocation) // Tuesday
	fake := &fakeCPA{queue: []string{
		cpaRecord("r1", "claude", "2026-10-20T01:00:00Z", 100),                                          // today 09:00 Shanghai
		cpaRecord("r2", "claude", "2026-10-19T03:00:00Z", 50),                                           // Monday
		cpaRecord("r3", "codex", "2026-10-20T01:00:00Z", 999),                                           // other provider
		`"` + strings.ReplaceAll(cpaRecord("r4", "claude", "2026-10-18T03:00:00Z", 7), `"`, `\"`) + `"`, // last week, string-encoded
	}}
	collector, _ := newTestCPACollector(t, fake)
	collector.now = func() time.Time { return now }

	items, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	values := map[string]string{}
	for _, item := range items {
		values[item.Category+"/"+stringFromExtra(item.Extra, "window")] = item.Value
	}
	if values["token_usage_product/"] != "100" || values["token_usage_product_week/"] != "150" {
		t.Fatalf("usage totals = %v", values)
	}
	if values["quota/5H"] != "95%" || values["quota/Week"] != "83%" {
		t.Fatalf("quota = %v", values)
	}

	// The queue is drained, but cached usage and throttled quota remain.
	fake.queue = []string{cpaRecord("r1", "claude", "2026-10-20T01:00:00Z", 100), cpaRecord("r5", "claude", "2026-10-20T01:30:00Z", 1)}
	items, err = collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect again: %v", err)
	}
	if fake.quotaCalls != 1 {
		t.Fatalf("quota fetched %d times inside the interval", fake.quotaCalls)
	}
	for _, item := range items {
		if item.Category == "token_usage_product" && item.Value != "101" {
			t.Fatalf("duplicate request_id counted twice: %s", item.Value)
		}
	}
	// Reuse quota until five minutes have elapsed, then refresh at the boundary.
	now = now.Add(5*time.Minute - time.Second)
	if _, err := collector.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fake.quotaCalls != 1 {
		t.Fatalf("quota refreshed before five minutes: %d", fake.quotaCalls)
	}
	now = now.Add(time.Second)
	if _, err := collector.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fake.quotaCalls != 2 {
		t.Fatalf("quota not refreshed after five minutes: %d", fake.quotaCalls)
	}
}

func TestCPACollectorKeepsLastQuotaOnFailure(t *testing.T) {
	now := time.Date(2026, 10, 20, 10, 0, 0, 0, model.ShanghaiLocation)
	fake := &fakeCPA{}
	collector, _ := newTestCPACollector(t, fake)
	collector.now = func() time.Time { return now }
	if _, err := collector.Collect(context.Background()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	fake.quotaFail = true
	now = now.Add(5 * time.Minute)
	items, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect after quota failure: %v", err)
	}
	quotaItems := 0
	for _, item := range items {
		if item.Category == "quota" {
			quotaItems++
		}
	}
	if quotaItems != 2 || fake.quotaCalls != 2 {
		t.Fatalf("quota items = %d, calls = %d", quotaItems, fake.quotaCalls)
	}
	// Failed lookups also wait five minutes before retrying.
	now = now.Add(30 * time.Second)
	if _, err := collector.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fake.quotaCalls != 2 {
		t.Fatalf("failed quota lookup retried inside interval: %d", fake.quotaCalls)
	}
	now = now.Add(270 * time.Second)
	fake.quotaFail = false
	if _, err := collector.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fake.quotaCalls != 3 {
		t.Fatalf("quota lookup not retried after five minutes: %d", fake.quotaCalls)
	}
}

func stringFromExtra(extra map[string]any, key string) string {
	value, _ := extra[key].(string)
	return value
}

func TestAntigravityCollectorGroups(t *testing.T) {
	dataStore := newRemoteTestStore(t)
	now := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	if err := dataStore.ReplaceAgentQuotaGroups("mbp", AntigravitySourcePrefix, []store.AgentQuotaObservation{
		{Source: AntigravitySourcePrefix + "Gemini 4 Argon", Quota5hUsed: -1, QuotaWeekUsed: 16.7, QuotaWeekReset: "2026-10-26T03:39:13Z", ObservedAt: now},
		{Source: AntigravitySourcePrefix + "Gemini Models", Quota5hUsed: 0, Quota5hReset: "2026-10-20T09:00:00Z", QuotaWeekUsed: 31.7, QuotaWeekReset: "2026-10-26T08:58:54Z", ObservedAt: now},
	}); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.ReplaceAgentQuotaGroups("old", AntigravitySourcePrefix, []store.AgentQuotaObservation{
		{Source: AntigravitySourcePrefix + "Stale", Quota5hUsed: 1, QuotaWeekUsed: 1, ObservedAt: now.Add(-2 * time.Hour)},
	}); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.SaveIngestedUsage("mbp", "antigravity", nil, []store.LocalUsageRecord{
		{FilePath: "agy-switch:2026-10-19", At: time.Date(2026, 10, 19, 0, 0, 0, 0, model.ShanghaiLocation), Total: 117100000, Quota5hUsed: -1, QuotaWeekUsed: -1},
		{FilePath: "agy-switch:2026-10-20", At: time.Date(2026, 10, 20, 0, 0, 0, 0, model.ShanghaiLocation), Total: 287400000, Quota5hUsed: -1, QuotaWeekUsed: -1},
	}); err != nil {
		t.Fatal(err)
	}
	collector := NewAntigravityCollector(config.AntigravityConfig{StaleSeconds: 1800}, dataStore)
	collector.now = func() time.Time { return now }
	items, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, item := range items {
		got = append(got, item.Title+"="+item.Value)
	}
	// Usage totals lead; Gemini Models comes first; passed 5H reset reads as refreshed; stale machine ignored.
	want := "Antigravity 今日 Token 用量=287400000,Antigravity 本周 Token 用量=404500000,账号 Gemini 5H 额度=100%,账号 Gemini Week 额度=68.30%,账号 Gemini 4 Argon Week 额度=83.30%"
	if strings.Join(got, ",") != want {
		t.Fatalf("items = %v", got)
	}
}
