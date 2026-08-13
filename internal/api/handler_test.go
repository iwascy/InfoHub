package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"infohub/internal/collector"
	"infohub/internal/model"
	"infohub/internal/store"
)

type fakeCollector struct {
	name string
}

func (f fakeCollector) Name() string { return f.name }

func (f fakeCollector) Collect(_ context.Context) ([]model.DataItem, error) {
	return nil, nil
}

func newTestDashboardHandler(dataStore store.Store) *Handler {
	return NewHandlerWithOptions(dataStore, collector.NewRegistry(), nil, HandlerOptions{
		DashboardSources: DashboardSources{
			Sub2API: "sub2api",
		},
	})
}

func TestSummaryHandler(t *testing.T) {
	dataStore := store.NewMemoryStore()
	if err := dataStore.Save("claude_relay", []model.DataItem{{
		Source:    "claude_relay",
		Category:  "token_usage",
		Title:     "今日 Token 用量",
		Value:     "123",
		FetchedAt: 1713600000,
	}}); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	registry := collector.NewRegistry()
	handler := NewHandler(dataStore, registry, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/summary", nil)
	rec := httptest.NewRecorder()
	handler.Summary(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", rec.Code)
	}

	var payload model.SummaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if payload.UpdatedAt != 1713600000 {
		t.Fatalf("unexpected updated_at: %d", payload.UpdatedAt)
	}
	if got := payload.Sources["claude_relay"].Items[0].Value; got != "123" {
		t.Fatalf("unexpected source item value: %s", got)
	}
}

func TestHealthHandlerIncludesUnknownRegisteredCollector(t *testing.T) {
	dataStore := store.NewMemoryStore()
	registry := collector.NewRegistry()
	registry.Register(fakeCollector{name: "feishu"})

	handler := NewHandler(dataStore, registry, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	handler.Health(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", rec.Code)
	}

	var payload model.HealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if payload.Collectors["feishu"].Status != "unknown" {
		t.Fatalf("unexpected collector status: %s", payload.Collectors["feishu"].Status)
	}
}

func TestEInkDashboardRendersCustomLayout(t *testing.T) {
	dataStore := store.NewMemoryStore()
	mustSaveSnapshot(t, dataStore, "sub2api", sub2apiDashboardItems())

	handler := newTestDashboardHandler(dataStore)
	req := httptest.NewRequest(http.MethodGet, "/dashboard/eink?refresh=600", nil)
	rec := httptest.NewRecorder()
	handler.EInkDashboard(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", rec.Code)
	}

	body := rec.Body.String()
	for _, expected := range []string{
		"InfoHub 墨水屏面板",
		"SUB2API CONSUMPTION",
		"DeepSeek",
		"Codex 订阅额度",
		">5H<",
		">Week<",
		"6.6M",
		"54.5M",
		"61.1M",
		"刷新 600s",
		"56%",
		"92%",
		"5H 余量仅 56%",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("dashboard body missing %q", expected)
		}
	}
	if strings.Contains(body, "Claude") {
		t.Fatalf("dashboard should not render Claude: %s", body)
	}
}

func TestDashboardRouteUsesDedicatedToken(t *testing.T) {
	dataStore := store.NewMemoryStore()
	registry := collector.NewRegistry()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := NewRouter(dataStore, registry, nil, logger, "api-token", "view-token", "", false, DashboardSources{})

	t.Run("rejects missing dashboard token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/dashboard/eink", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("unexpected status code: %d", rec.Code)
		}
	})

	t.Run("accepts dedicated dashboard token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/dashboard/eink?token=view-token", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("unexpected status code: %d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("accepts dedicated dashboard token for json", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/dashboard/eink.json?token=view-token", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("unexpected status code: %d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("accepts dedicated dashboard token for device json", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/dashboard/eink/device.json?token=view-token", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("unexpected status code: %d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("keeps api auth unchanged", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/summary?token=view-token", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("unexpected status code: %d", rec.Code)
		}
	})
}

func TestEInkDashboardDataReturnsStructuredPayload(t *testing.T) {
	dataStore := store.NewMemoryStore()
	mustSaveSnapshot(t, dataStore, "sub2api", sub2apiDashboardItems())

	handler := newTestDashboardHandler(dataStore)
	req := httptest.NewRequest(http.MethodGet, "/dashboard/eink.json?refresh=300", nil)
	rec := httptest.NewRecorder()
	handler.EInkDashboardData(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", rec.Code)
	}

	var payload struct {
		UpdatedAtUnix  int64 `json:"updated_at_unix"`
		RefreshSeconds int   `json:"refresh_seconds"`
		Overview       []struct {
			Kind  string   `json:"kind"`
			Title string   `json:"title"`
			Value string   `json:"value"`
			Stats []string `json:"stats"`
		} `json:"overview"`
		Sub2APITable struct {
			HasRows bool `json:"has_rows"`
			Rows    []struct {
				Account  string `json:"account"`
				Status   string `json:"status"`
				FiveHour struct {
					Percent int    `json:"percent"`
					Text    string `json:"text"`
				} `json:"five_hour"`
				Week struct {
					Percent int    `json:"percent"`
					Text    string `json:"text"`
				} `json:"week"`
			} `json:"rows"`
		} `json:"sub2api_table"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if payload.UpdatedAtUnix != 1776766339 {
		t.Fatalf("unexpected updated_at_unix: %d", payload.UpdatedAtUnix)
	}
	if payload.RefreshSeconds != 300 {
		t.Fatalf("unexpected refresh_seconds: %d", payload.RefreshSeconds)
	}
	if len(payload.Overview) != 3 || payload.Overview[0].Kind != "deepseek" || payload.Overview[0].Value != "6,580,305" {
		t.Fatalf("unexpected overview payload: %+v", payload.Overview)
	}
	if !payload.Sub2APITable.HasRows || len(payload.Sub2APITable.Rows) != 1 {
		t.Fatalf("unexpected sub2api table rows: %+v", payload.Sub2APITable.Rows)
	}
	row := payload.Sub2APITable.Rows[0]
	if row.FiveHour.Percent != 56 || row.FiveHour.Text != "56%" {
		t.Fatalf("unexpected 5H payload: %+v", row.FiveHour)
	}
	if row.Week.Percent != 92 || row.Week.Text != "92%" {
		t.Fatalf("unexpected week payload: %+v", row.Week)
	}
	if row.Status != "关注" {
		t.Fatalf("unexpected row status: %s", row.Status)
	}
}

func TestEInkDeviceDataReturnsCompactPayload(t *testing.T) {
	dataStore := store.NewMemoryStore()
	mustSaveSnapshot(t, dataStore, "sub2api", sub2apiDashboardItems())

	handler := newTestDashboardHandler(dataStore)
	req := httptest.NewRequest(http.MethodGet, "/dashboard/eink/device.json?refresh=180", nil)
	rec := httptest.NewRecorder()
	handler.EInkDeviceData(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", rec.Code)
	}

	var payload struct {
		UpdatedAtUnix  int64 `json:"updated_at_unix"`
		RefreshSeconds int   `json:"refresh_seconds"`
		DeepSeek       struct {
			Value        string `json:"value"`
			Requests     int    `json:"requests"`
			Cost         string `json:"cost"`
			Enabled      int    `json:"enabled"`
			ValueNumeric int64  `json:"value_numeric"`
		} `json:"deepseek"`
		DeepSeekQuota struct {
			Label     string `json:"label"`
			Remaining string `json:"remaining"`
			Detail    string `json:"detail"`
			Window    string `json:"window"`
			Percent   int    `json:"percent"`
			Source    string `json:"source"`
		} `json:"deepseek_quota"`
		Total struct {
			Value    string `json:"value"`
			Requests int    `json:"requests"`
			Cost     string `json:"cost"`
			Alerts   int    `json:"alerts"`
		} `json:"total"`
		Codex struct {
			Value        string `json:"value"`
			Requests     int    `json:"requests"`
			Cost         string `json:"cost"`
			Enabled      int    `json:"enabled"`
			ValueNumeric int64  `json:"value_numeric"`
		} `json:"codex"`
		CodexRows []struct {
			Account  string `json:"account"`
			FiveHour struct {
				Percent int `json:"percent"`
			} `json:"five_hour"`
			Status string `json:"status"`
		} `json:"codex_rows"`
		Alerts []string `json:"alerts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if payload.UpdatedAtUnix != 1776766339 {
		t.Fatalf("unexpected updated_at_unix: %d", payload.UpdatedAtUnix)
	}
	if payload.RefreshSeconds != 180 {
		t.Fatalf("unexpected refresh_seconds: %d", payload.RefreshSeconds)
	}
	if payload.DeepSeek.Value != "6,580,305" || payload.DeepSeek.Requests != 86 || payload.DeepSeek.Cost != "0.07" {
		t.Fatalf("unexpected DeepSeek payload: %+v", payload.DeepSeek)
	}
	if payload.DeepSeek.ValueNumeric != 6580305 {
		t.Fatalf("unexpected DeepSeek numeric payload: %+v", payload.DeepSeek)
	}
	if payload.DeepSeekQuota.Label != "日额度" || payload.DeepSeekQuota.Remaining != "$8.75" || payload.DeepSeekQuota.Detail != "已用 $1.25 / $10.00" || payload.DeepSeekQuota.Percent != 88 || payload.DeepSeekQuota.Source != "platform_quota" {
		t.Fatalf("unexpected DeepSeek quota payload: %+v", payload.DeepSeekQuota)
	}
	if payload.Codex.Value != "54,500,630" || payload.Codex.Requests != 607 || payload.Codex.Cost != "68.85" {
		t.Fatalf("unexpected codex payload: %+v", payload.Codex)
	}
	if payload.Codex.ValueNumeric != 54500630 {
		t.Fatalf("unexpected codex numeric payload: %+v", payload.Codex)
	}
	if payload.Total.Value != "61,080,935" || payload.Total.Requests != 693 || payload.Total.Cost != "68.92" || payload.Total.Alerts != 1 {
		t.Fatalf("unexpected total payload: %+v", payload.Total)
	}
	if len(payload.CodexRows) != 1 || payload.CodexRows[0].Account != "admin10010" || payload.CodexRows[0].FiveHour.Percent != 56 || payload.CodexRows[0].Status != "关注" {
		t.Fatalf("unexpected codex rows: %+v", payload.CodexRows)
	}
	if len(payload.Alerts) != 1 || payload.Alerts[0] != "Codex admin10010：5H 余量仅 56%" {
		t.Fatalf("unexpected alerts: %+v", payload.Alerts)
	}
}

func TestEInkDashboardUsesConfiguredSub2APIOverUnrelatedFailures(t *testing.T) {
	dataStore := store.NewMemoryStore()
	mustSaveSnapshot(t, dataStore, "sub2api", sub2apiDashboardItems())
	if err := dataStore.SaveFailure("claude_local", context.DeadlineExceeded, time.Unix(1776767000, 0)); err != nil {
		t.Fatalf("save claude local failure failed: %v", err)
	}
	if err := dataStore.SaveFailure("codex_local", context.DeadlineExceeded, time.Unix(1776767000, 0)); err != nil {
		t.Fatalf("save codex local failure failed: %v", err)
	}

	handler := newTestDashboardHandler(dataStore)
	req := httptest.NewRequest(http.MethodGet, "/dashboard/eink.json", nil)
	rec := httptest.NewRecorder()
	handler.EInkDashboardData(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", rec.Code)
	}

	var dashboardPayload struct {
		UpdatedAtUnix int64 `json:"updated_at_unix"`
		Overview      []struct {
			Kind  string `json:"kind"`
			Title string `json:"title"`
			Value string `json:"value"`
		} `json:"overview"`
		Device struct {
			UpdatedAtUnix int64 `json:"updated_at_unix"`
			DeepSeek      struct {
				Title string `json:"title"`
				Value string `json:"value"`
			} `json:"deepseek"`
			Codex struct {
				Title string `json:"title"`
				Value string `json:"value"`
			} `json:"codex"`
		} `json:"device"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &dashboardPayload); err != nil {
		t.Fatalf("decode dashboard response failed: %v", err)
	}

	if dashboardPayload.UpdatedAtUnix != 1776766339 || dashboardPayload.Device.UpdatedAtUnix != 1776766339 {
		t.Fatalf("dashboard should use remote source timestamps: %+v", dashboardPayload)
	}
	if len(dashboardPayload.Overview) < 2 || dashboardPayload.Overview[0].Kind != "deepseek" || dashboardPayload.Overview[1].Kind != "codex" {
		t.Fatalf("dashboard should use product views: %+v", dashboardPayload.Overview)
	}
	if dashboardPayload.Device.DeepSeek.Title != "DeepSeek 今日消耗" || dashboardPayload.Device.DeepSeek.Value != "6,580,305" {
		t.Fatalf("device should use Sub2API DeepSeek data: %+v", dashboardPayload.Device.DeepSeek)
	}
	if dashboardPayload.Device.Codex.Title != "Codex 今日消耗" || dashboardPayload.Device.Codex.Value != "54,500,630" {
		t.Fatalf("device should use Sub2API data for Codex panel: %+v", dashboardPayload.Device.Codex)
	}
}

func TestEInkDashboardDoesNotReadSourcesWithoutConfiguration(t *testing.T) {
	dataStore := store.NewMemoryStore()
	mustSaveSnapshot(t, dataStore, "claude_relay", []model.DataItem{
		{
			Source:    "claude_relay",
			Category:  "token_usage",
			Title:     "今日 Token 用量",
			Value:     "1058870",
			FetchedAt: 1776766339,
		},
	})
	mustSaveSnapshot(t, dataStore, "sub2api", []model.DataItem{
		{
			Source:    "sub2api",
			Category:  "token_usage",
			Title:     "今日 Token 用量",
			Value:     "24854435",
			FetchedAt: 1776766339,
		},
	})

	handler := NewHandler(dataStore, collector.NewRegistry(), nil)
	req := httptest.NewRequest(http.MethodGet, "/dashboard/eink/device.json", nil)
	rec := httptest.NewRecorder()
	handler.EInkDeviceData(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", rec.Code)
	}

	var payload struct {
		UpdatedAtUnix int64 `json:"updated_at_unix"`
		DeepSeek      struct {
			Title string `json:"title"`
			Value string `json:"value"`
		} `json:"deepseek"`
		Codex struct {
			Title string `json:"title"`
			Value string `json:"value"`
		} `json:"codex"`
		Total struct {
			Value string `json:"value"`
		} `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if payload.UpdatedAtUnix != 0 {
		t.Fatalf("dashboard should not read unconfigured source timestamps: %+v", payload)
	}
	if payload.DeepSeek.Value != "--" || payload.Codex.Value != "--" || payload.Total.Value != "--" {
		t.Fatalf("dashboard should not read unconfigured source values: %+v", payload)
	}
	if payload.DeepSeek.Title != "DeepSeek 今日消耗" || payload.Codex.Title != "Codex 今日消耗" {
		t.Fatalf("dashboard should retain product titles: %+v", payload)
	}
}

func TestEInkDashboardDataPrioritizesLowestRemainingAlert(t *testing.T) {
	dataStore := store.NewMemoryStore()
	mustSaveSnapshot(t, dataStore, "sub2api", []model.DataItem{
		{Source: "sub2api", Category: "quota", Title: "账号 codex-low 5H 额度", Value: "6%", FetchedAt: 1777027500, Extra: map[string]any{"remaining_percent": 6, "window": "5H"}},
		{Source: "sub2api", Category: "quota", Title: "账号 codex-low Week 额度", Value: "83%", FetchedAt: 1777027500, Extra: map[string]any{"remaining_percent": 83, "window": "Week"}},
		{
			Source:    "sub2api",
			Category:  "quota",
			Title:     "账号 admin10010 5H 额度",
			Value:     "58%",
			FetchedAt: 1777027500,
			Extra: map[string]any{
				"remaining_percent": 58,
				"window":            "5H",
			},
		},
		{
			Source:    "sub2api",
			Category:  "quota",
			Title:     "账号 admin10010 Week 额度",
			Value:     "18%",
			FetchedAt: 1777027500,
			Extra: map[string]any{
				"remaining_percent": 18,
				"window":            "Week",
			},
		},
	})

	handler := newTestDashboardHandler(dataStore)
	req := httptest.NewRequest(http.MethodGet, "/dashboard/eink/data.json?refresh=300", nil)
	rec := httptest.NewRecorder()
	handler.EInkDashboardData(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", rec.Code)
	}

	var payload struct {
		Alerts      []string `json:"alerts"`
		AlertTitle  string   `json:"alert_title"`
		AlertDetail string   `json:"alert_detail"`
		Device      struct {
			Alerts []string `json:"alerts"`
		} `json:"device"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if len(payload.Alerts) != 2 || payload.Alerts[0] != "codex-low：5H 余量仅 6%" || payload.Alerts[1] != "admin10010：5H 余量仅 58%" {
		t.Fatalf("unexpected dashboard alerts: %+v", payload.Alerts)
	}
	if payload.AlertTitle != "codex-low" || payload.AlertDetail != "5H 余量仅 6%" {
		t.Fatalf("unexpected dashboard alert summary: %q / %q", payload.AlertTitle, payload.AlertDetail)
	}
	if len(payload.Device.Alerts) != 2 || payload.Device.Alerts[0] != "Codex codex-low：5H 余量仅 6%" || payload.Device.Alerts[1] != "Codex admin10010：5H 余量仅 58%" {
		t.Fatalf("unexpected device alerts: %+v", payload.Device.Alerts)
	}
}

func TestEInkDashboardDataPrioritizesWeekAlertWhenFiveHourTies(t *testing.T) {
	dataStore := store.NewMemoryStore()
	mustSaveSnapshot(t, dataStore, "sub2api", []model.DataItem{
		{Source: "sub2api", Category: "quota", Title: "账号 codex-week-ok 5H 额度", Value: "58%", FetchedAt: 1777027500, Extra: map[string]any{"remaining_percent": 58, "window": "5H"}},
		{Source: "sub2api", Category: "quota", Title: "账号 codex-week-ok Week 额度", Value: "83%", FetchedAt: 1777027500, Extra: map[string]any{"remaining_percent": 83, "window": "Week"}},
		{
			Source:    "sub2api",
			Category:  "quota",
			Title:     "账号 admin10010 5H 额度",
			Value:     "58%",
			FetchedAt: 1777027500,
			Extra: map[string]any{
				"remaining_percent": 58,
				"window":            "5H",
			},
		},
		{
			Source:    "sub2api",
			Category:  "quota",
			Title:     "账号 admin10010 Week 额度",
			Value:     "18%",
			FetchedAt: 1777027500,
			Extra: map[string]any{
				"remaining_percent": 18,
				"window":            "Week",
			},
		},
	})

	handler := newTestDashboardHandler(dataStore)
	req := httptest.NewRequest(http.MethodGet, "/dashboard/eink.json?refresh=300", nil)
	rec := httptest.NewRecorder()
	handler.EInkDashboardData(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", rec.Code)
	}

	var payload struct {
		Alerts      []string `json:"alerts"`
		AlertTitle  string   `json:"alert_title"`
		AlertDetail string   `json:"alert_detail"`
		Device      struct {
			Alerts []string `json:"alerts"`
		} `json:"device"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if len(payload.Alerts) != 2 || payload.Alerts[0] != "codex-week-ok：5H 余量仅 58%" || payload.Alerts[1] != "admin10010：5H 余量仅 58%" {
		t.Fatalf("unexpected dashboard alerts: %+v", payload.Alerts)
	}
	if payload.AlertTitle != "codex-week-ok" || payload.AlertDetail != "5H 余量仅 58%" {
		t.Fatalf("unexpected dashboard alert summary: %q / %q", payload.AlertTitle, payload.AlertDetail)
	}
	if len(payload.Device.Alerts) != 2 || payload.Device.Alerts[0] != "Codex codex-week-ok：5H 余量仅 58%" || payload.Device.Alerts[1] != "Codex admin10010：5H 余量仅 58%" {
		t.Fatalf("unexpected device alerts: %+v", payload.Device.Alerts)
	}
}

func TestEInkDashboardMarksCodexLocalQuotaWithoutCapUnknown(t *testing.T) {
	dataStore := store.NewMemoryStore()
	mustSaveSnapshot(t, dataStore, "codex_local", []model.DataItem{
		{
			Source:    "codex_local",
			Category:  "token_usage",
			Title:     "今日 Token 用量",
			Value:     "10120138",
			FetchedAt: 1777265700,
			Extra: map[string]any{
				"daily_cost":       0,
				"daily_requests":   160,
				"enabled_accounts": 1,
			},
		},
		{
			Source:    "codex_local",
			Category:  "quota",
			Title:     "账号 Codex Local 5H 额度",
			Value:     "100%",
			FetchedAt: 1777265700,
			Extra: map[string]any{
				"cap":               0,
				"quota_source":      "estimated_cap",
				"remaining_percent": 100,
				"window":            "5H",
			},
		},
		{
			Source:    "codex_local",
			Category:  "quota",
			Title:     "账号 Codex Local Week 额度",
			Value:     "100%",
			FetchedAt: 1777265700,
			Extra: map[string]any{
				"cap":               0,
				"quota_source":      "estimated_cap",
				"remaining_percent": 100,
				"window":            "Week",
			},
		},
	})

	handler := NewHandlerWithOptions(dataStore, collector.NewRegistry(), nil, HandlerOptions{
		DashboardSources: DashboardSources{
			Sub2API: "codex_local",
		},
	})
	req := httptest.NewRequest(http.MethodGet, "/dashboard/eink.json", nil)
	rec := httptest.NewRecorder()
	handler.EInkDashboardData(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", rec.Code)
	}

	var payload struct {
		Alerts       []string `json:"alerts"`
		Sub2APITable struct {
			Rows []struct {
				FiveHour struct {
					Text string `json:"text"`
				} `json:"five_hour"`
				Week struct {
					Text string `json:"text"`
				} `json:"week"`
				Status string `json:"status"`
			} `json:"rows"`
		} `json:"sub2api_table"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if len(payload.Sub2APITable.Rows) != 1 {
		t.Fatalf("unexpected codex rows: %+v", payload.Sub2APITable.Rows)
	}
	row := payload.Sub2APITable.Rows[0]
	if row.FiveHour.Text != "--" || row.Week.Text != "--" || row.Status != "额度未知" {
		t.Fatalf("unexpected unknown local quota row: %+v", row)
	}
	if len(payload.Alerts) != 1 || payload.Alerts[0] != "Codex Local：在线额度不可用" {
		t.Fatalf("unexpected alerts: %+v", payload.Alerts)
	}
}

func TestEInkDeviceDataReturnsMockPayloadWhenEnabled(t *testing.T) {
	dataStore := store.NewMemoryStore()
	if err := dataStore.Save("sub2api", []model.DataItem{
		{
			Source:    "sub2api",
			Category:  "token_usage",
			Title:     "今日 Token 用量",
			Value:     "1",
			FetchedAt: 1,
		},
	}); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	handler := NewHandlerWithOptions(dataStore, collector.NewRegistry(), nil, HandlerOptions{DashboardMockEnabled: true})
	req := httptest.NewRequest(http.MethodGet, "/dashboard/eink/device.json?refresh=180", nil)
	rec := httptest.NewRecorder()
	handler.EInkDeviceData(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", rec.Code)
	}

	var payload struct {
		UpdatedAtUnix  int64    `json:"updated_at_unix"`
		RefreshSeconds int      `json:"refresh_seconds"`
		Alerts         []string `json:"alerts"`
		Codex          struct {
			Value        string `json:"value"`
			ValueNumeric int64  `json:"value_numeric"`
		} `json:"codex"`
		Total struct {
			Alerts int `json:"alerts"`
		} `json:"total"`
		CodexRows []struct {
			Account string `json:"account"`
			Status  string `json:"status"`
		} `json:"codex_rows"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if payload.UpdatedAtUnix != 1776997800 || payload.RefreshSeconds != 180 {
		t.Fatalf("unexpected mock metadata: %+v", payload)
	}
	if payload.Codex.Value != "54,500,630" || payload.Codex.ValueNumeric != 54500630 {
		t.Fatalf("unexpected mock codex payload: %+v", payload.Codex)
	}
	if payload.Total.Alerts != 0 || len(payload.Alerts) != 0 {
		t.Fatalf("mock payload should not include alerts: %+v", payload)
	}
	if len(payload.CodexRows) != 2 || payload.CodexRows[1].Account != "admin10086" || payload.CodexRows[1].Status != "正常" {
		t.Fatalf("unexpected mock codex rows: %+v", payload.CodexRows)
	}
}

func TestEInkDeviceDataKeepsLastSuccessfulSnapshotOnCollectorFailure(t *testing.T) {
	dataStore := store.NewMemoryStore()
	mustSaveSnapshot(t, dataStore, "sub2api", sub2apiDashboardItems())
	if err := dataStore.SaveFailure("sub2api", context.DeadlineExceeded, time.Unix(1776767000, 0)); err != nil {
		t.Fatalf("save failure failed: %v", err)
	}

	handler := newTestDashboardHandler(dataStore)
	req := httptest.NewRequest(http.MethodGet, "/dashboard/eink/device.json?refresh=180", nil)
	rec := httptest.NewRecorder()
	handler.EInkDeviceData(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", rec.Code)
	}

	var payload struct {
		UpdatedAtUnix int64 `json:"updated_at_unix"`
		Codex         struct {
			Value    string `json:"value"`
			Requests int    `json:"requests"`
			Cost     string `json:"cost"`
			Enabled  int    `json:"enabled"`
		} `json:"codex"`
		CodexRows []struct {
			Account string `json:"account"`
			Status  string `json:"status"`
		} `json:"codex_rows"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if payload.UpdatedAtUnix != 1776766339 {
		t.Fatalf("unexpected updated_at_unix: %d", payload.UpdatedAtUnix)
	}
	if payload.Codex.Value != "54,500,630" || payload.Codex.Requests != 607 || payload.Codex.Cost != "68.85" {
		t.Fatalf("unexpected cached codex payload: %+v", payload.Codex)
	}
	if len(payload.CodexRows) != 1 || payload.CodexRows[0].Account != "admin10010" || payload.CodexRows[0].Status != "关注" {
		t.Fatalf("unexpected cached codex rows: %+v", payload.CodexRows)
	}
}

func mustSaveSnapshot(t *testing.T, dataStore store.Store, source string, items []model.DataItem) {
	t.Helper()
	if err := dataStore.Save(source, items); err != nil {
		t.Fatalf("save failed: %v", err)
	}
}

func sub2apiDashboardItems() []model.DataItem {
	return []model.DataItem{
		{
			Source: "sub2api", Category: "token_usage_product", Title: "DeepSeek 今日 Token 用量", Value: "6580305", FetchedAt: 1776766339,
			Extra: map[string]any{"product": "deepseek", "daily_requests": 86, "daily_cost": 0.07},
		},
		{
			Source: "sub2api", Category: "token_usage_product", Title: "Codex 今日 Token 用量", Value: "54500630", FetchedAt: 1776766339,
			Extra: map[string]any{"product": "codex", "daily_requests": 607, "daily_cost": 68.85},
		},
		{
			Source: "sub2api", Category: "product_quota", Title: "DeepSeek 日额度", Value: "$8.75", FetchedAt: 1776766339,
			Extra: map[string]any{"product": "deepseek", "window": "daily", "usage_usd": 1.25, "limit_usd": 10, "remaining_usd": 8.75, "remaining_percent": 87.5, "quota_source": "platform_quota"},
		},
		{
			Source: "sub2api", Category: "quota", Title: "账号 admin10010 5H 额度", Value: "56%", FetchedAt: 1776766339,
			Extra: map[string]any{"remaining_percent": 56, "window": "5H", "reset_at": "2026-04-24T15:00:00+08:00"},
		},
		{
			Source: "sub2api", Category: "quota", Title: "账号 admin10010 Week 额度", Value: "92%", FetchedAt: 1776766339,
			Extra: map[string]any{"remaining_percent": 92, "window": "Week", "reset_at": "2026-04-27T08:00:00+08:00"},
		},
	}
}
