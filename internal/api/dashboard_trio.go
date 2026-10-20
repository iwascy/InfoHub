package api

import (
	"strings"

	"infohub/internal/model"
)

const (
	trioCPASource         = "cpa"
	trioAntigravitySource = "antigravity"
	trioTokensLabel       = "今日"
	trioNoFiveHour        = "不限"
)

// einkPlatform is one block of the trio layout: a usage summary plus the
// quota rows of its accounts or model groups.
type einkPlatform struct {
	Key        string         `json:"key"`
	Name       string         `json:"name"`
	Today      string         `json:"today"`
	TodayLabel string         `json:"today_label"`
	UsageNote  string         `json:"usage_note"`
	Rows       []einkQuotaRow `json:"rows"`
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
	antigravityToday := buildProductDashboard(antigravity, "antigravity", "Antigravity 今日消耗")
	antigravityWeek := buildProductWeek(antigravity.Items, "antigravity", "Antigravity 本周用量")
	return []einkPlatform{
		{
			Key:        "claude",
			Name:       "Claude Pro",
			Today:      trioTokenText(claudeToday),
			TodayLabel: trioTokensLabel,
			UsageNote:  trioWeekNote(claudeWeek.DisplayValue),
			Rows:       trioCompactRows(claudeRows),
		},
		{
			Key:        "codex",
			Name:       "Codex",
			Today:      device.Codex.DisplayValue,
			TodayLabel: trioTokensLabel,
			UsageNote:  trioWeekNote(device.CodexWeek.DisplayValue),
			Rows:       trioCompactRows(device.CodexAccounts),
		},
		{
			Key:        "antigravity",
			Name:       "Antigravity",
			Today:      trioTokenText(antigravityToday),
			TodayLabel: trioTokensLabel,
			UsageNote:  trioWeekNote(antigravityWeek.DisplayValue),
			Rows:       trioCompactRows(markMissingFiveHour(trioQuotaRows(antigravity, trioAntigravitySource, "Gemini"))),
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

// markMissingFiveHour labels groups that report a weekly limit but no 5H
// window upstream; a row with neither window stays unknown.
func markMissingFiveHour(rows []einkQuotaRow) []einkQuotaRow {
	for index := range rows {
		if rows[index].FiveHour.Text == "--" && rows[index].Week.Text != "--" {
			rows[index].FiveHour = einkPercentCell{Text: trioNoFiveHour, ResetAt: "--", ResetShort: "--", NotApplicable: true}
		}
	}
	return rows
}

// trioCompactRows copies rows and shortens the 5H reset to its time of day;
// the window is at most five hours long, so the date adds nothing.
func trioCompactRows(rows []einkQuotaRow) []einkQuotaRow {
	result := append([]einkQuotaRow(nil), rows...)
	for index := range result {
		reset := result[index].FiveHour.ResetShort
		if at := strings.LastIndex(reset, " "); at >= 0 {
			result[index].FiveHour.ResetShort = reset[at+1:]
		}
	}
	return result
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

func trioWeekNote(week string) string {
	return "本周 " + week
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
		{Key: "claude", Name: "Claude Pro", Today: "12.3M", TodayLabel: trioTokensLabel, UsageNote: trioWeekNote("88.1M"),
			Rows: trioCompactRows([]einkQuotaRow{quota("cycyzg@gmail.com", 95, 83, "04/24 21:20", "04/26 15:00")})},
		{Key: "codex", Name: "Codex", Today: device.Codex.DisplayValue, TodayLabel: trioTokensLabel, UsageNote: trioWeekNote(device.CodexWeek.DisplayValue),
			Rows: trioCompactRows(device.CodexAccounts)},
		{Key: "antigravity", Name: "Antigravity", Today: "287.4M", TodayLabel: trioTokensLabel, UsageNote: trioWeekNote("404.6M"),
			Rows: trioCompactRows(markMissingFiveHour([]einkQuotaRow{quota("Gemini", 99, 68, "04/24 21:31", "04/26 16:58"), quota("Gemini 4 Argon", -1, 83, "--", "04/26 11:39")}))},
	}
}
