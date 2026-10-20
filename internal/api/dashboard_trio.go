package api

import (
	"strings"

	"infohub/internal/model"
)

const (
	trioCPASource         = "cpa"
	trioAntigravitySource = "antigravity"
)

// einkPlatform is one block of the trio layout: a usage summary plus the
// quota rows of its accounts or model groups.
type einkPlatform struct {
	Key       string         `json:"key"`
	Name      string         `json:"name"`
	Today     string         `json:"today"`
	UsageNote string         `json:"usage_note"`
	Rows      []einkQuotaRow `json:"rows"`
}

func buildTrioPlatforms(snapshots map[string]model.SourceSnapshot, device einkDevicePayload) []einkPlatform {
	claude := snapshots[trioCPASource]
	claudeToday := buildProductDashboard(claude, "claude", "Claude 今日消耗")
	claudeWeek := buildProductWeek(claude.Items, "claude", "Claude 本周用量")
	claudeRows := trioQuotaRows(claude, trioCPASource, "Claude Pro")
	for index := range claudeRows {
		if email := strings.TrimSpace(claudeRows[index].Email); email != "" {
			claudeRows[index].Account = email
		}
	}

	antigravity := snapshots[trioAntigravitySource]
	return []einkPlatform{
		{
			Key:       "claude",
			Name:      "Claude Pro",
			Today:     trioTokenText(claudeToday),
			UsageNote: trioWeekNote(claudeWeek.DisplayValue, device.WeekRange),
			Rows:      claudeRows,
		},
		{
			Key:       "codex",
			Name:      "Codex",
			Today:     device.Codex.DisplayValue,
			UsageNote: trioWeekNote(device.CodexWeek.DisplayValue, device.WeekRange),
			Rows:      device.CodexAccounts,
		},
		{
			Key:       "antigravity",
			Name:      "Antigravity",
			Today:     "--",
			UsageNote: "Pro · 仅提供额度百分比",
			Rows:      trioQuotaRows(antigravity, trioAntigravitySource, "Gemini"),
		},
	}
}

// trioQuotaRows returns the snapshot's quota rows, or one unknown row named
// placeholder so the panel keeps its shape before the first collection.
func trioQuotaRows(snapshot model.SourceSnapshot, sourceKey, placeholder string) []einkQuotaRow {
	accounts, _, _ := buildQuotaAccounts(snapshot.Items, sourceKey)
	rows := make([]einkQuotaRow, 0, len(accounts))
	for _, account := range accounts {
		rows = append(rows, buildQuotaRow(account))
	}
	if len(rows) == 0 {
		row := emptyQuotaRow()
		row.Account = placeholder
		rows = append(rows, row)
	}
	return rows
}

func trioTokenText(overview sourceOverview) string {
	if overview.Card.Value == "--" {
		return "--"
	}
	if overview.TokenValue == 0 {
		return "0.0M"
	}
	return formatTokenMillions(overview.TokenValue)
}

func trioWeekNote(week, weekRange string) string {
	return "本周 " + week + " · " + weekRange
}

func trioUpdatedAt(snapshots map[string]model.SourceSnapshot, sources DashboardSources) int64 {
	selected := map[string]model.SourceSnapshot{}
	for _, key := range []string{sources.Sub2APIKey(), trioCPASource, trioAntigravitySource} {
		if snapshot, ok := snapshots[key]; ok && key != "" {
			selected[key] = snapshot
		}
	}
	return maxSnapshotDataUpdatedAt(selected)
}

func mockTrioPlatforms(device einkDevicePayload) []einkPlatform {
	quota := func(account string, five, week int, fiveReset, weekReset string) einkQuotaRow {
		row := einkQuotaRow{Account: account, Status: "--"}
		row.FiveHour = einkPercentCell{Percent: five, Text: formatPercentText(five), ResetShort: fiveReset}
		row.Week = einkPercentCell{Percent: week, Text: formatPercentText(week), ResetShort: weekReset}
		if five < 0 {
			row.FiveHour = einkPercentCell{Text: "--", ResetAt: "--", ResetShort: "--"}
		}
		return row
	}
	return []einkPlatform{
		{Key: "claude", Name: "Claude Pro", Today: "12.3M", UsageNote: trioWeekNote("88.1M", device.WeekRange),
			Rows: []einkQuotaRow{quota("cycyzg@gmail.com", 95, 83, "04/24 21:20", "04/26 15:00")}},
		{Key: "codex", Name: "Codex", Today: device.Codex.DisplayValue, UsageNote: trioWeekNote(device.CodexWeek.DisplayValue, device.WeekRange),
			Rows: device.CodexAccounts},
		{Key: "antigravity", Name: "Antigravity", Today: "--", UsageNote: "Pro · 仅提供额度百分比",
			Rows: []einkQuotaRow{quota("Gemini", 99, 68, "04/24 21:31", "04/26 16:58"), quota("Gemini 4 Argon", -1, 83, "--", "04/26 11:39")}},
	}
}
