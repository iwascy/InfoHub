package api

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"unicode"

	"gopkg.in/yaml.v3"
	"infohub/internal/model"
)

func TestCodexDashboardStates(t *testing.T) {
	for _, tc := range []struct {
		name     string
		snapshot model.SourceSnapshot
		status   string
	}{
		{"empty", model.SourceSnapshot{}, "等待数据"},
		{"error", model.SourceSnapshot{Status: "error", Error: "upstream unavailable"}, "采集失败"},
		{"cached", model.SourceSnapshot{Status: "error", Error: "upstream unavailable", Items: sub2apiDashboardItems()}, "数据未更新"},
		{"ready", model.SourceSnapshot{Items: sub2apiDashboardItems()}, "关注"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := buildEInkDashboardPage(map[string]model.SourceSnapshot{"sub2api": tc.snapshot}, 600, DashboardSources{Sub2API: "sub2api"})
			if page.Device.CodexStatus != tc.status {
				t.Fatalf("status = %q, want %q", page.Device.CodexStatus, tc.status)
			}
			var out bytes.Buffer
			if err := einkDashboardTmpl.Execute(&out, page); err != nil {
				t.Fatal(err)
			}
			for _, forbidden := range []string{"告警", "关注", "upstream unavailable", "额度状态正常"} {
				if strings.Contains(out.String(), forbidden) {
					t.Fatalf("unexpected visible status: %s", forbidden)
				}
			}
			if len(tc.snapshot.Items) == 0 {
				if page.Device.CodexFocus.FiveHour.Text != "--" || page.Device.CodexFocus.Week.Text != "--" {
					t.Fatal("empty quota must be unknown")
				}
				if strings.Contains(out.String(), "额度状态正常") || strings.Contains(out.String(), "aria-valuenow=") {
					t.Fatal("empty quota must not report healthy or numeric quota")
				}
			}
		})
	}
}

func TestCodexFocusUsesOwnResetTimes(t *testing.T) {
	items := sub2apiDashboardItems()
	items = append(items,
		model.DataItem{Category: "quota", Title: "账号 priority 5H 额度", Extra: map[string]any{"window": "5H", "remaining_percent": 6, "reset_at": "2026-04-25T16:00:00+08:00"}},
		model.DataItem{Category: "quota", Title: "账号 priority Week 额度", Extra: map[string]any{"window": "Week", "remaining_percent": 70, "reset_at": "2026-04-29T08:00:00+08:00"}},
	)
	page := buildEInkDashboardPage(map[string]model.SourceSnapshot{"sub2api": {Items: items}}, 600, DashboardSources{Sub2API: "sub2api"})
	focus := page.Device.CodexFocus
	if focus.Account != "priority" || focus.FiveHour.Percent != 6 {
		t.Fatalf("wrong focus: %+v", focus)
	}
	if focus.FiveHour.ResetAt != formatResetFooter("2026-04-25T16:00:00+08:00") || focus.Week.ResetAt != formatResetFooter("2026-04-29T08:00:00+08:00") {
		t.Fatalf("reset times belong to another account: %+v", focus)
	}
	var out bytes.Buffer
	page.Device.CodexAccounts = filterCodexAccounts(page.Device.CodexRows, []string{"priority"})
	if err := einkDashboardTmpl.Execute(&out, page); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "重置 "+focus.FiveHour.ResetShort) {
		t.Fatal("focus reset not rendered")
	}
}

func TestCodexMissingWindowIsUnknown(t *testing.T) {
	items := []model.DataItem{{Category: "quota", Title: "账号 partial 5H 额度", Extra: map[string]any{"window": "5H", "remaining_percent": 80}}}
	device := buildEInkDevicePayload(map[string]model.SourceSnapshot{"sub2api": {Items: items}}, 600, DashboardSources{Sub2API: "sub2api"})
	if device.CodexFocus.FiveHour.Text != "80%" || device.CodexFocus.Week.Text != "--" || device.CodexStatus != "额度未知" {
		t.Fatalf("missing window shown as known: %+v", device.CodexFocus)
	}
}

func TestCodexDashboardEscapesAccountNames(t *testing.T) {
	page := buildMockEInkDashboardPage(600)
	page.Device.CodexAccounts[0].Account = "<script>alert(1)</script>"
	var out bytes.Buffer
	if err := einkDashboardTmpl.Execute(&out, page); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "<script>") || !strings.Contains(out.String(), "&lt;script&gt;") {
		t.Fatal("account name must be escaped")
	}
}

func TestCodexDeviceLocalEstimateWithoutCapIsUnknown(t *testing.T) {
	items := []model.DataItem{}
	for _, window := range []string{"5H", "Week"} {
		items = append(items, model.DataItem{Category: "quota", Title: "账号 Codex Local " + window + " 额度", Extra: map[string]any{"window": window, "quota_source": "estimated_cap", "cap": 0, "remaining_percent": 100}})
	}
	device := buildEInkDevicePayload(map[string]model.SourceSnapshot{"codex_local": {Items: items}}, 600, DashboardSources{Sub2API: "codex_local"})
	if device.CodexFocus.FiveHour.Text != "--" || device.CodexFocus.Week.Text != "--" || device.CodexStatus != "额度未知" {
		t.Fatalf("local quota estimate must not be presented as 100%%: %+v", device.CodexFocus)
	}
}

func TestCodexZeroUsageIsNotMissing(t *testing.T) {
	items := []model.DataItem{{Category: "token_usage_product", Value: "0", Extra: map[string]any{"product": "codex"}}}
	device := buildEInkDevicePayload(map[string]model.SourceSnapshot{"sub2api": {Items: items}}, 600, DashboardSources{Sub2API: "sub2api"})
	if device.Codex.Value != "0" || device.Codex.DisplayValue != "0.0M" {
		t.Fatalf("zero usage shown as missing: %+v", device.Codex)
	}
}

func TestCodexFirmwareLayout(t *testing.T) {
	data, err := os.ReadFile("../../deploy/esphome/reterminal_e1001_infohub_api.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Font []struct {
			ID     string `yaml:"id"`
			File   string `yaml:"file"`
			Size   int    `yaml:"size"`
			BPP    int    `yaml:"bpp"`
			Glyphs string `yaml:"glyphs"`
		} `yaml:"font"`
		Display []struct {
			Lambda string `yaml:"lambda"`
		} `yaml:"display"`
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if len(config.Display) != 1 || len(config.Font) == 0 {
		t.Fatal("missing display or fonts")
	}
	boldFonts := 0
	for _, font := range config.Font {
		if strings.HasPrefix(font.ID, "codex_font_") {
			boldFonts++
			if !strings.HasSuffix(font.File, "@800") || font.Size < 15 || font.BPP != 1 {
				t.Fatalf("Codex fonts must be bold, legible and monochrome: %+v", font)
			}
		}
	}
	if boldFonts != 5 {
		t.Fatalf("expected five dedicated Codex fonts, got %d", boldFonts)
	}
	lambda := config.Display[0].Lambda
	newLayout := lambda[strings.LastIndex(lambda, "struct RowData {"):]
	for _, forbidden := range []string{"DeepSeek", "deepseek", "total_value_live"} {
		if strings.Contains(newLayout, forbidden) {
			t.Fatalf("legacy display data: %s", forbidden)
		}
	}
	for _, expected := range []string{"deepseek-codex", "codex_accounts", "codex_week", "five_reset", "week_reset", "draw_progress(track, percent, false)"} {
		if !strings.Contains(lambda, expected) {
			t.Fatalf("missing %s", expected)
		}
	}
	for _, ch := range lambda {
		if unicode.Is(unicode.Han, ch) && !strings.ContainsRune(config.Font[0].Glyphs, ch) {
			t.Fatalf("missing font glyph %c", ch)
		}
	}
}
