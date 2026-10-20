package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"infohub/internal/model"
	"infohub/internal/store"
)

func TestDashboardTemplateSwitch(t *testing.T) {
	h := NewHandlerWithOptions(store.NewMemoryStore(), nil, nil, HandlerOptions{DashboardMockEnabled: true, Dashboard: DashboardOptions{Template: "deepseek-codex"}})
	for _, tc := range []struct{ query, want string }{{"", "DeepSeek"}, {"?template=codex", "cycyzg@gmail.com"}, {"?template=deepseek-codex", "DeepSeek"}} {
		rec := httptest.NewRecorder()
		h.EInkDashboard(rec, httptest.NewRequest(http.MethodGet, "/dashboard/eink"+tc.query, nil))
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), tc.want) {
			t.Fatalf("%s: %d missing %s", tc.query, rec.Code, tc.want)
		}
	}
	for _, handler := range []http.HandlerFunc{h.EInkDashboard, h.EInkDashboardData, h.EInkDeviceData} {
		rec := httptest.NewRecorder()
		handler(rec, httptest.NewRequest(http.MethodGet, "/dashboard/eink?template=../invalid", nil))
		if rec.Code != 400 {
			t.Fatalf("invalid template status = %d", rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	h.EInkDeviceData(rec, httptest.NewRequest(http.MethodGet, "/dashboard/eink/device.json?template=codex", nil))
	var device einkDevicePayload
	if err := json.Unmarshal(rec.Body.Bytes(), &device); err != nil {
		t.Fatal(err)
	}
	if device.Template != "codex" || len(device.CodexAccounts) != 2 || device.CodexAccounts[1].Account != "qqcyan@gmail.com" || device.CodexWeek.DisplayValue != "286.2M" {
		t.Fatalf("unexpected device: %+v", device)
	}
}

func TestCodexAccountFilterIsExactAndStable(t *testing.T) {
	rows := []einkQuotaRow{
		{Account: "misleading cycyzg@gmail.com", Email: "other@gmail.com", FiveHour: einkPercentCell{Text: "1%", Percent: 1}},
		{Account: "Pro 20x", Email: " CYCYZG@gmail.com ", FiveHour: einkPercentCell{Text: "56%", Percent: 56}, Week: einkPercentCell{Text: "92%", Percent: 92}},
		{Account: "qqcyan@gmail.com", FiveHour: einkPercentCell{Text: "78%", Percent: 78}, Week: einkPercentCell{Text: "64%", Percent: 64}},
	}
	filtered := filterCodexAccounts(rows, dashboardAccounts(nil))
	if len(filtered) != 2 || filtered[0].Account != "cycyzg@gmail.com" || filtered[0].FiveHour.Percent != 56 || filtered[1].FiveHour.Percent != 78 {
		t.Fatalf("bad filter: %+v", filtered)
	}
	missing := filterCodexAccounts(nil, dashboardAccounts(nil))
	if len(missing) != 2 || missing[0].FiveHour.Text != "--" || missing[1].Week.Text != "--" {
		t.Fatal("missing accounts must stay unknown")
	}
}

func TestCodexWeekAggregation(t *testing.T) {
	item := func(value, start string) model.DataItem {
		return model.DataItem{Category: "token_usage_product_week", Value: value, Extra: map[string]any{"product": "codex", "start_date": start, "end_date": "2026-04-24"}}
	}
	for _, tc := range []struct {
		name  string
		items []model.DataItem
		want  string
	}{
		{"missing", nil, "--"},
		{"zero", []model.DataItem{item("0", "2026-04-20")}, "0.0M"},
		{"sum", []model.DataItem{item("2000000", "2026-04-20"), item("1000000", "2026-04-20")}, "3.0M"},
		{"partial failure", []model.DataItem{item("2000000", "2026-04-20"), item("--", "2026-04-20")}, "--"},
		{"different weeks", []model.DataItem{item("2000000", "2026-04-20"), item("1000000", "2026-04-13")}, "--"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildCodexWeek(tc.items).DisplayValue; got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestAccountQuotaDoesNotMergeDisplayAliases(t *testing.T) {
	items := []model.DataItem{}
	for i, email := range []string{"cycyzg@gmail.com", "qqcyan@gmail.com"} {
		items = append(items, model.DataItem{Category: "quota", Title: "账号 Pro 20x 5H 额度", Extra: map[string]any{"account_id": i + 1, "email": email, "window": "5H", "remaining_percent": 50 + i}})
	}
	accounts, _, _ := buildQuotaAccounts(items, "codex")
	if len(accounts) != 2 {
		t.Fatalf("merged same-name accounts: %+v", accounts)
	}
}
