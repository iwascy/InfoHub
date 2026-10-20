package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"infohub/internal/model"
	"infohub/internal/store"
)

func trioSnapshots() map[string]model.SourceSnapshot {
	sub := sub2apiDashboardItems()
	for _, item := range sub {
		if item.Category == "quota" {
			item.Extra["email"] = "cycyzg@gmail.com"
		}
	}
	return map[string]model.SourceSnapshot{
		"sub2api": {Items: sub},
		"cpa": {Items: []model.DataItem{
			{Category: "token_usage_product", Value: "12300000", FetchedAt: 1776766400, Extra: map[string]any{"product": "claude"}},
			{Category: "token_usage_product_week", Value: "88100000", Extra: map[string]any{"product": "claude", "start_date": "2026-04-20", "end_date": "2026-04-21"}},
			{Category: "quota", Title: "账号 Claude Pro 5H 额度", Extra: map[string]any{"window": "5H", "remaining_percent": 95, "email": "pro@example.com", "account_id": "cpa:c1", "reset_at": "2026-04-24T15:00:00+08:00"}},
			{Category: "quota", Title: "账号 Claude Pro Week 额度", Extra: map[string]any{"window": "Week", "remaining_percent": 83, "email": "pro@example.com", "account_id": "cpa:c1"}},
		}},
		"antigravity": {Items: []model.DataItem{
			{Category: "token_usage_product", Value: "287400000", FetchedAt: 1776766400, Extra: map[string]any{"product": "antigravity"}},
			{Category: "token_usage_product_week", Value: "404600000", Extra: map[string]any{"product": "antigravity", "start_date": "2026-04-20", "end_date": "2026-04-21"}},
			{Category: "quota", Title: "账号 Gemini 5H 额度", Extra: map[string]any{"window": "5H", "remaining_percent": 99, "account_id": "antigravity:Gemini Models"}},
			{Category: "quota", Title: "账号 Gemini Week 额度", Extra: map[string]any{"window": "Week", "remaining_percent": 68, "account_id": "antigravity:Gemini Models"}},
			{Category: "quota", Title: "账号 Gemini 4 Argon Week 额度", Extra: map[string]any{"window": "Week", "remaining_percent": 83, "account_id": "antigravity:Gemini 4 Argon"}},
		}},
	}
}

func TestTrioPlatforms(t *testing.T) {
	snapshots := trioSnapshots()
	page := buildEInkDashboardPage(snapshots, 600, DashboardSources{Sub2API: "sub2api"})
	page.Device.CodexAccounts = filterCodexAccounts(page.Device.CodexRows, dashboardAccounts(nil))
	platforms := buildTrioPlatforms(snapshots, page.Device)
	if len(platforms) != 3 {
		t.Fatalf("platforms = %d", len(platforms))
	}
	claude, codex, ag := platforms[0], platforms[1], platforms[2]
	if claude.Today != "12.3M" || claude.UsageNote != "本周 88.1M" || len(claude.Rows) != 1 || claude.Rows[0].Account != "pro@example.com" || claude.Rows[0].FiveHour.Text != "95%" {
		t.Fatalf("claude = %+v", claude)
	}
	if codex.Today != "54.5M" || len(codex.Rows) != 2 || codex.Rows[0].FiveHour.Text != "56%" || codex.Rows[1].FiveHour.Text != "--" {
		t.Fatalf("codex = %+v", codex)
	}
	if len(ag.Rows) != 2 || ag.Rows[0].Account != "Gemini" || ag.Rows[1].FiveHour.Text != "不限" || !ag.Rows[1].FiveHour.NotApplicable || ag.Today != "287.4M" || ag.UsageNote != "本周 404.6M" || ag.Rows[1].Week.Text != "83%" {
		t.Fatalf("antigravity = %+v", ag)
	}
}

func TestTrioPlaceholdersWithoutData(t *testing.T) {
	platforms := buildTrioPlatforms(map[string]model.SourceSnapshot{}, buildEInkDevicePayload(nil, 600, DashboardSources{}))
	if platforms[0].Today != "--" || platforms[0].Rows[0].Account != "Claude Pro" || platforms[0].Rows[0].Week.Text != "--" {
		t.Fatalf("claude placeholder = %+v", platforms[0])
	}
	if platforms[2].Today != "--" || platforms[2].Rows[0].Account != "Gemini" || platforms[2].Rows[0].FiveHour.Text != "--" {
		t.Fatalf("antigravity placeholder = %+v", platforms[2])
	}
}

func TestTrioIsDefaultTemplate(t *testing.T) {
	dataStore := store.NewMemoryStore()
	for source, snapshot := range trioSnapshots() {
		mustSaveSnapshot(t, dataStore, source, snapshot.Items)
	}
	h := NewHandlerWithOptions(dataStore, nil, nil, HandlerOptions{DashboardSources: DashboardSources{Sub2API: "sub2api"}})
	rec := httptest.NewRecorder()
	h.EInkDashboard(rec, httptest.NewRequest(http.MethodGet, "/dashboard/eink", nil))
	body := rec.Body.String()
	for _, want := range []string{"AI 额度", "Claude Pro", "pro@example.com", "Gemini 4 Argon", "12.3M", "54.5M", "不限", `rowspan="2"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("trio page missing %q", want)
		}
	}
	for _, forbidden := range []string{"告警", "关注", "DeepSeek", "仅额度", "重置", "刷新"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("trio page renders %q", forbidden)
		}
	}

	rec = httptest.NewRecorder()
	h.EInkDeviceData(rec, httptest.NewRequest(http.MethodGet, "/dashboard/eink/device.json", nil))
	var device einkDevicePayload
	if err := json.Unmarshal(rec.Body.Bytes(), &device); err != nil {
		t.Fatal(err)
	}
	if device.Template != "trio" || len(device.Platforms) != 3 || device.UpdatedAtUnix <= 0 {
		t.Fatalf("device = template %q platforms %d updated %d", device.Template, len(device.Platforms), device.UpdatedAtUnix)
	}

	// Legacy layouts stay selectable and do not carry trio rows.
	rec = httptest.NewRecorder()
	h.EInkDeviceData(rec, httptest.NewRequest(http.MethodGet, "/dashboard/eink/device.json?template=codex", nil))
	device = einkDevicePayload{}
	_ = json.Unmarshal(rec.Body.Bytes(), &device)
	if device.Template != "codex" || len(device.Platforms) != 0 {
		t.Fatalf("codex device = %q %d", device.Template, len(device.Platforms))
	}
}

func TestTrioMockPage(t *testing.T) {
	h := NewHandlerWithOptions(store.NewMemoryStore(), nil, nil, HandlerOptions{DashboardMockEnabled: true})
	rec := httptest.NewRecorder()
	h.EInkDashboard(rec, httptest.NewRequest(http.MethodGet, "/dashboard/eink", nil))
	var out bytes.Buffer
	out.WriteString(rec.Body.String())
	for _, want := range []string{"cycyzg@gmail.com", "qqcyan@gmail.com", "Gemini 4 Argon", "88.1M"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("mock trio missing %q", want)
		}
	}
}

func TestTrioCompactRowsShortensFiveHourReset(t *testing.T) {
	rows := []einkQuotaRow{{FiveHour: einkPercentCell{ResetShort: "04/24 21:20"}, Week: einkPercentCell{ResetShort: "04/26 15:00"}}, {FiveHour: einkPercentCell{ResetShort: "--"}}}
	compact := trioCompactRows(rows)
	if compact[0].FiveHour.ResetShort != "21:20" || compact[0].Week.ResetShort != "04/26 15:00" || compact[1].FiveHour.ResetShort != "--" {
		t.Fatalf("compact = %+v", compact)
	}
	if rows[0].FiveHour.ResetShort != "04/24 21:20" {
		t.Fatalf("input mutated: %+v", rows[0])
	}
}
