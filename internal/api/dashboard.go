package api

import (
	_ "embed"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"infohub/internal/model"
)

const (
	einkDashboardDefaultRefreshSeconds = 600
	einkDashboardMinRefreshSeconds     = 60
	einkDashboardMaxRefreshSeconds     = 3600
)

//go:embed dashboard_codex.html
var einkDashboardHTML string

var einkDashboardTmpl = template.Must(template.New("eink-dashboard").Parse(einkDashboardHTML))

//go:embed dashboard_trio.html
var einkTrioDashboardHTML string

var einkTrioDashboardTmpl = template.Must(template.New("trio").Parse(einkTrioDashboardHTML))

//go:embed dashboard_deepseek_codex.html
var einkLegacyDashboardHTML string

var einkLegacyDashboardTmpl = template.Must(template.New("deepseek-codex").Parse(einkLegacyDashboardHTML))

type einkDashboardPage struct {
	UpdatedAt      string             `json:"updated_at"`
	UpdatedAtUnix  int64              `json:"updated_at_unix"`
	RefreshSeconds int                `json:"refresh_seconds"`
	Overview       []einkOverviewCard `json:"overview"`
	Sub2APITable   einkQuotaTable     `json:"sub2api_table"`
	Alerts         []string           `json:"alerts"`
	AlertTitle     string             `json:"alert_title"`
	AlertDetail    string             `json:"alert_detail"`
	Device         einkDevicePayload  `json:"device"`
}

type einkOverviewCard struct {
	Kind  string        `json:"kind"`
	Title string        `json:"title"`
	Value string        `json:"value"`
	Label string        `json:"label"`
	Stats []string      `json:"stats"`
	Icon  template.HTML `json:"-"`
}

type einkQuotaTable struct {
	Title         string         `json:"title"`
	Rows          []einkQuotaRow `json:"rows"`
	FocusRow      einkQuotaRow   `json:"focus_row"`
	HasRows       bool           `json:"has_rows"`
	ErrorText     string         `json:"error_text,omitempty"`
	FiveHourReset string         `json:"five_hour_reset"`
	WeekReset     string         `json:"week_reset"`
}

type einkQuotaRow struct {
	Account     string          `json:"account"`
	Email       string          `json:"email,omitempty"`
	FiveHour    einkPercentCell `json:"five_hour"`
	Week        einkPercentCell `json:"week"`
	Status      string          `json:"status"`
	StatusClass string          `json:"status_class"`
}

type einkPercentCell struct {
	Percent    int    `json:"percent"`
	Text       string `json:"text"`
	ResetAt    string `json:"reset_at"`
	ResetShort string `json:"reset_short"`
}

type einkDeepSeekQuota struct {
	Label     string `json:"label"`
	Remaining string `json:"remaining"`
	Detail    string `json:"detail"`
	Window    string `json:"window"`
	Percent   int    `json:"percent"`
	ResetAt   string `json:"reset_at,omitempty"`
	Source    string `json:"source"`
}

type einkDeviceOverview struct {
	Title        string `json:"title"`
	Value        string `json:"value"`
	DisplayValue string `json:"display_value"`
	Label        string `json:"label"`
	Requests     int    `json:"requests"`
	Cost         string `json:"cost"`
	Enabled      int    `json:"enabled,omitempty"`
	Alerts       int    `json:"alerts,omitempty"`
	ValueNumeric int64  `json:"value_numeric"`
}

type einkDevicePayload struct {
	Template       string             `json:"template"`
	UpdatedAt      string             `json:"updated_at"`
	UpdatedAtUnix  int64              `json:"updated_at_unix"`
	RefreshSeconds int                `json:"refresh_seconds"`
	DeepSeek       einkDeviceOverview `json:"deepseek"`
	DeepSeekQuota  einkDeepSeekQuota  `json:"deepseek_quota"`
	Codex          einkDeviceOverview `json:"codex"`
	CodexWeek      einkDeviceOverview `json:"codex_week"`
	DailyRange     string             `json:"daily_range"`
	WeekRange      string             `json:"week_range"`
	CodexAccounts  []einkQuotaRow     `json:"codex_accounts"`
	Total          einkDeviceOverview `json:"total"`
	CodexRows      []einkQuotaRow     `json:"codex_rows"`
	CodexFocus     einkQuotaRow       `json:"codex_focus"`
	CodexStatus    string             `json:"codex_status"`
	Platforms      []einkPlatform     `json:"platforms"`
	CodexError     string             `json:"codex_error,omitempty"`
	Alerts         []string           `json:"alerts"`
	ResetHints     map[string]string  `json:"reset_hints"`
}

type quotaAccount struct {
	Name      string
	Email     string
	FiveHour  quotaMetric
	Week      quotaMetric
	SourceKey string
}

type quotaMetric struct {
	UsedPercent      int
	RemainingPercent int
	ResetAt          string
	QuotaSource      string
	OnlineStatus     string
	Unknown          bool
}

func (h *Handler) EInkDashboard(w http.ResponseWriter, r *http.Request) {
	page, err := h.dashboardPage(r)
	if err != nil {
		writeDashboardError(w, dashboardErrorStatus(err), err)
		return
	}
	tmpl := einkDashboardTmpl
	switch page.Device.Template {
	case "deepseek-codex":
		tmpl = einkLegacyDashboardTmpl
	case "trio":
		tmpl = einkTrioDashboardTmpl
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := tmpl.Execute(w, page); err != nil {
		writeDashboardError(w, http.StatusInternalServerError, err)
	}
}

func (h *Handler) EInkDashboardData(w http.ResponseWriter, r *http.Request) {
	page, err := h.dashboardPage(r)
	if err != nil {
		writeJSON(w, dashboardErrorStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *Handler) EInkDeviceData(w http.ResponseWriter, r *http.Request) {
	page, err := h.dashboardPage(r)
	if err != nil {
		writeJSON(w, dashboardErrorStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, page.Device)
}

func buildEInkDashboardPage(snapshots map[string]model.SourceSnapshot, refreshSeconds int, sources DashboardSources) einkDashboardPage {
	sub2apiKey, snapshot := configuredSnapshot(snapshots, sources.Sub2APIKey())
	updatedAtUnix := maxSnapshotDataUpdatedAt(selectedSnapshots(sub2apiKey, snapshot, "", model.SourceSnapshot{}))
	deepSeekOverview := buildProductDashboard(snapshot, "deepseek", "DeepSeek 今日消耗")
	codexOverview := buildProductDashboard(snapshot, "codex", "Codex 今日消耗")
	_, subTable, codexCritical, codexAlerts := buildSourceDashboard(sub2apiKey, dashboardOverviewTitle(sub2apiKey), snapshot)
	totalOverview := buildTotalOverview(deepSeekOverview, codexOverview, codexCritical)
	alerts := mergeDashboardAlerts(codexAlerts)
	alertTitle, alertDetail := splitDashboardAlert(alerts)
	device := buildEInkDevicePayload(snapshots, refreshSeconds, sources)

	return einkDashboardPage{
		UpdatedAt:      formatHeaderTime(updatedAtUnix),
		UpdatedAtUnix:  updatedAtUnix,
		RefreshSeconds: refreshSeconds,
		Overview: []einkOverviewCard{
			deepSeekOverview.Card,
			codexOverview.Card,
			totalOverview,
		},
		Sub2APITable: subTable,
		Alerts:       alerts,
		AlertTitle:   alertTitle,
		AlertDetail:  alertDetail,
		Device:       device,
	}
}

func buildEInkDevicePayload(snapshots map[string]model.SourceSnapshot, refreshSeconds int, sources DashboardSources) einkDevicePayload {
	sub2apiKey, snapshot := configuredSnapshot(snapshots, sources.Sub2APIKey())
	updatedAtUnix := maxSnapshotDataUpdatedAt(selectedSnapshots(sub2apiKey, snapshot, "", model.SourceSnapshot{}))
	deepSeekOverview := buildProductDashboard(snapshot, "deepseek", "DeepSeek 今日消耗")
	deepSeekQuota := buildDeepSeekQuota(snapshot.Items)
	codexOverview := buildProductDashboard(snapshot, "codex", "Codex 今日消耗")
	quotaSource := "codex"
	if sub2apiKey == "codex_local" {
		quotaSource = sub2apiKey
	}
	_, codexTable, _, codexAlerts := buildSourceDashboard(quotaSource, dashboardOverviewTitle(quotaSource), snapshot)
	codexDisplayValue := formatTokenMillions(codexOverview.TokenValue)
	if codexOverview.Card.Value != "--" && codexOverview.TokenValue == 0 {
		codexDisplayValue = "0.0M"
	}

	totalToken := deepSeekOverview.TokenValue + codexOverview.TokenValue
	totalRequests := int(math.Round(deepSeekOverview.Requests + codexOverview.Requests))
	totalCost := deepSeekOverview.Cost + codexOverview.Cost
	alerts := mergeDashboardAlerts(codexAlerts)
	dailyRange, weekRange := dashboardUsageRanges(snapshot.Items, updatedAtUnix)

	return einkDevicePayload{
		Template:       "codex",
		CodexWeek:      buildCodexWeek(snapshot.Items),
		DailyRange:     dailyRange,
		WeekRange:      weekRange,
		CodexAccounts:  filterCodexAccounts(codexTable.Rows, dashboardAccounts(nil)),
		UpdatedAt:      formatHeaderTime(updatedAtUnix),
		UpdatedAtUnix:  updatedAtUnix,
		RefreshSeconds: refreshSeconds,
		DeepSeek: einkDeviceOverview{
			Title:        deepSeekOverview.Card.Title,
			Value:        deepSeekOverview.Card.Value,
			DisplayValue: formatTokenMillions(deepSeekOverview.TokenValue),
			Label:        deepSeekOverview.Card.Label,
			Requests:     int(math.Round(deepSeekOverview.Requests)),
			Cost:         fmt.Sprintf("%.2f", deepSeekOverview.Cost),
			ValueNumeric: int64(math.Round(deepSeekOverview.TokenValue)),
		},
		DeepSeekQuota: deepSeekQuota,
		Codex: einkDeviceOverview{
			Title:        codexOverview.Card.Title,
			Value:        codexOverview.Card.Value,
			DisplayValue: codexDisplayValue,
			Label:        codexOverview.Card.Label,
			Requests:     int(math.Round(codexOverview.Requests)),
			Cost:         fmt.Sprintf("%.2f", codexOverview.Cost),
			Enabled:      int(math.Round(codexOverview.EnabledAccount)),
			ValueNumeric: int64(math.Round(codexOverview.TokenValue)),
		},
		Total: einkDeviceOverview{
			Title:        "今日合计",
			Value:        formatPrimaryNumber(totalToken),
			DisplayValue: formatTokenMillions(totalToken),
			Label:        "总 Token",
			Requests:     totalRequests,
			Cost:         fmt.Sprintf("%.2f", totalCost),
			Alerts:       len(alerts),
			ValueNumeric: int64(math.Round(totalToken)),
		},
		CodexRows:   codexTable.Rows,
		CodexFocus:  codexTable.FocusRow,
		CodexStatus: codexDashboardStatus(snapshot, codexTable),
		CodexError:  codexTable.ErrorText,
		Alerts:      alerts,
		ResetHints: map[string]string{
			"codex_five_hour": codexTable.FiveHourReset,
			"codex_week":      codexTable.WeekReset,
		},
	}
}

func codexDashboardStatus(snapshot model.SourceSnapshot, table einkQuotaTable) string {
	if strings.EqualFold(snapshot.Status, "error") {
		if table.HasRows {
			return "数据未更新"
		}
		return "采集失败"
	}
	if !table.HasRows {
		return "等待数据"
	}
	if table.FocusRow.FiveHour.Text == "--" || table.FocusRow.Week.Text == "--" {
		return "额度未知"
	}
	return table.FocusRow.Status
}

func buildMockEInkDashboardPage(refreshSeconds int) einkDashboardPage {
	device := buildMockEInkDevicePayload(refreshSeconds)
	alertTitle, alertDetail := splitDashboardAlert(device.Alerts)
	codexTable := einkQuotaTable{
		Title:         "Sub2API 账号额度",
		Rows:          device.CodexRows,
		FocusRow:      device.CodexFocus,
		HasRows:       true,
		FiveHourReset: device.ResetHints["codex_five_hour"],
		WeekReset:     device.ResetHints["codex_week"],
	}

	return einkDashboardPage{
		UpdatedAt:      device.UpdatedAt,
		UpdatedAtUnix:  device.UpdatedAtUnix,
		RefreshSeconds: refreshSeconds,
		Overview: []einkOverviewCard{
			{
				Kind:  "deepseek",
				Title: device.DeepSeek.Title,
				Value: device.DeepSeek.Value,
				Label: device.DeepSeek.Label,
				Stats: []string{"请求 86", "成本 $0.07"},
				Icon:  dashboardCardIcon("deepseek"),
			},
			{
				Kind:  "sub2api",
				Title: "Sub2API 今日概览",
				Value: device.Codex.Value,
				Label: device.Codex.Label,
				Stats: []string{"请求 607", "成本 $68.85"},
				Icon:  dashboardCardIcon("sub2api"),
			},
			{
				Kind:  "total",
				Title: device.Total.Title,
				Value: device.Total.Value,
				Label: device.Total.Label,
				Stats: []string{"总请求 693", "总成本 $68.92", fmt.Sprintf("告警 %d", len(device.Alerts))},
				Icon:  dashboardCardIcon("total"),
			},
		},
		Sub2APITable: codexTable,
		Alerts:       device.Alerts,
		AlertTitle:   alertTitle,
		AlertDetail:  alertDetail,
		Device:       device,
	}
}

func buildMockEInkDevicePayload(refreshSeconds int) einkDevicePayload {
	device := einkDevicePayload{
		UpdatedAt:      "2026-04-24 10:30",
		UpdatedAtUnix:  1776997800,
		RefreshSeconds: refreshSeconds,
		DeepSeek: einkDeviceOverview{
			Title:        "DeepSeek 今日消耗",
			Value:        "6,580,305",
			DisplayValue: "6.6M",
			Label:        "Token 用量",
			Requests:     86,
			Cost:         "0.07",
			ValueNumeric: 6580305,
		},
		DeepSeekQuota: einkDeepSeekQuota{
			Label: "日额度", Remaining: "$8.70", Detail: "已用 $1.30 / $10.00", Window: "daily", Percent: 87, Source: "platform_quota",
		},
		Codex: einkDeviceOverview{
			Title:        "Codex 今日消耗",
			Value:        "54,500,630",
			DisplayValue: "54.5M",
			Label:        "Token 用量",
			Requests:     607,
			Cost:         "68.85",
			Enabled:      5,
			ValueNumeric: 54500630,
		},
		Total: einkDeviceOverview{
			Title:        "今日合计",
			Value:        "61,080,935",
			DisplayValue: "61.1M",
			Label:        "总 Token",
			Requests:     693,
			Cost:         "68.92",
			Alerts:       0,
			ValueNumeric: 61080935,
		},
		CodexRows: []einkQuotaRow{
			{
				Account:     "admin10010",
				FiveHour:    einkPercentCell{Percent: 56, Text: "56%", ResetAt: "2026-04-24 15:00"},
				Week:        einkPercentCell{Percent: 92, Text: "92%", ResetAt: "2026-04-27 08:00"},
				Status:      "关注",
				StatusClass: "",
			},
			{
				Account:     "admin10086",
				FiveHour:    einkPercentCell{Percent: 78, Text: "78%", ResetAt: "2026-04-24 16:00"},
				Week:        einkPercentCell{Percent: 64, Text: "64%", ResetAt: "2026-04-28 08:00"},
				Status:      "正常",
				StatusClass: "",
			},
		},
		Alerts: []string{},
		ResetHints: map[string]string{
			"codex_five_hour": "5H 重置: 2026-04-24 15:00",
			"codex_week":      "Week 重置: 2026-04-27 08:00",
		},
	}
	device.CodexFocus = pickFocusQuotaRow(device.CodexRows)
	device.Template = "codex"
	device.CodexWeek = einkDeviceOverview{Title: "Codex 本周用量", Value: "286,200,000", ValueNumeric: 286200000, DisplayValue: "286.2M", Label: "Tokens"}
	device.DailyRange, device.WeekRange = "04/24 00:00 至今", "04/20 周一至今"
	device.CodexRows[0].Email = "cycyzg@gmail.com"
	device.CodexRows[1].Email = "qqcyan@gmail.com"
	for i := range device.CodexRows {
		for _, cell := range []*einkPercentCell{&device.CodexRows[i].FiveHour, &device.CodexRows[i].Week} {
			if date, err := time.Parse("2006-01-02 15:04", cell.ResetAt); err == nil {
				cell.ResetShort = date.Format("01/02 15:04")
			}
		}
	}
	device.CodexAccounts = filterCodexAccounts(device.CodexRows, dashboardAccounts(nil))
	device.CodexStatus = device.CodexFocus.Status
	device.Alerts = []string{"Codex admin10010：5H 余量仅 56%"}
	device.Total.Alerts = len(device.Alerts)
	return device
}

func configuredSnapshot(snapshots map[string]model.SourceSnapshot, key string) (string, model.SourceSnapshot) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", model.SourceSnapshot{}
	}
	if snapshot, ok := snapshots[key]; ok {
		return key, snapshot
	}
	return key, model.SourceSnapshot{}
}

func selectedSnapshots(leftKey string, left model.SourceSnapshot, rightKey string, right model.SourceSnapshot) map[string]model.SourceSnapshot {
	selected := make(map[string]model.SourceSnapshot, 2)
	if leftKey != "" {
		selected[leftKey] = left
	}
	if rightKey != "" {
		selected[rightKey] = right
	}
	return selected
}

func dashboardOverviewTitle(sourceKey string) string {
	switch sourceKey {
	case "claude_local":
		return "Claude Local 今日概览"
	case "claude_relay":
		return "Claude Relay 今日概览"
	case "codex_local":
		return "Codex Local 今日概览"
	case "codex":
		return "Codex 今日概览"
	case "sub2api":
		return "Sub2API 今日概览"
	case "":
		return "未配置数据源"
	default:
		return sourceKey + " 今日概览"
	}
}

type sourceOverview struct {
	Card           einkOverviewCard
	TokenValue     float64
	Requests       float64
	Cost           float64
	EnabledAccount float64
}

func buildProductDashboard(snapshot model.SourceSnapshot, product string, title string) sourceOverview {
	hasUsage := false
	overview := sourceOverview{
		Card: einkOverviewCard{
			Kind:  product,
			Title: title,
			Value: "--",
			Label: "Token 用量",
			Stats: []string{"请求 0", "成本 $0.00"},
			Icon:  dashboardCardIcon(product),
		},
	}

	for _, item := range snapshot.Items {
		if item.Category != "token_usage_product" || !strings.EqualFold(stringExtra(item.Extra, "product"), product) {
			continue
		}
		value, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(item.Value), ",", ""), 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			continue
		}
		hasUsage = true
		overview.TokenValue += value
		overview.Requests += numericExtra(item.Extra, "daily_requests")
		overview.Cost += numericExtra(item.Extra, "daily_cost")
	}
	if overview.TokenValue > 0 {
		overview.Card.Value = formatPrimaryNumber(overview.TokenValue)
	} else if hasUsage {
		overview.Card.Value = "0"
	}
	overview.Card.Stats = []string{
		"请求 " + formatCompactWhole(overview.Requests),
		fmt.Sprintf("成本 $%.2f", overview.Cost),
	}
	return overview
}

func buildDeepSeekQuota(items []model.DataItem) einkDeepSeekQuota {
	result := einkDeepSeekQuota{
		Label: "额度", Remaining: "--", Detail: "未设置平台额度", Percent: -1,
	}
	priority := map[string]int{"daily": 1, "weekly": 2, "monthly": 3, "balance": 4}
	selectedPriority := 100
	for _, item := range items {
		if item.Category != "product_quota" || !strings.EqualFold(stringExtra(item.Extra, "product"), "deepseek") {
			continue
		}
		window := strings.ToLower(strings.TrimSpace(stringExtra(item.Extra, "window")))
		currentPriority, ok := priority[window]
		if !ok || currentPriority >= selectedPriority {
			continue
		}
		selectedPriority = currentPriority
		remaining := numericExtra(item.Extra, "remaining_usd")
		result = einkDeepSeekQuota{
			Remaining: fmt.Sprintf("$%.2f", remaining),
			Window:    window,
			Percent:   -1,
			ResetAt:   stringExtra(item.Extra, "reset_at"),
			Source:    stringExtra(item.Extra, "quota_source"),
		}
		if window == "balance" {
			result.Label = "余额"
			result.Detail = "账户可用余额"
			continue
		}
		result.Label = map[string]string{"daily": "日额度", "weekly": "周额度", "monthly": "月额度"}[window]
		usage := numericExtra(item.Extra, "usage_usd")
		limit := numericExtra(item.Extra, "limit_usd")
		result.Detail = fmt.Sprintf("已用 $%.2f / $%.2f", usage, limit)
		result.Percent = clampPercent(numericExtra(item.Extra, "remaining_percent"))
	}
	return result
}

func buildSourceDashboard(sourceKey string, title string, snapshot model.SourceSnapshot) (sourceOverview, einkQuotaTable, int, []string) {
	overview := sourceOverview{
		Card: einkOverviewCard{
			Kind:  sourceKey,
			Title: title,
			Value: "--",
			Label: "Token 用量",
			Stats: []string{"请求 0", "成本 $0.00", "启用 0"},
			Icon:  dashboardCardIcon(sourceKey),
		},
	}
	table := einkQuotaTable{
		Title:    dashboardTableTitle(sourceKey),
		FocusRow: emptyQuotaRow(),
	}

	if strings.EqualFold(snapshot.Status, "error") {
		message := strings.TrimSpace(snapshot.Error)
		if message == "" {
			message = "最近一次采集失败"
		}
		table.ErrorText = message
		if len(snapshot.Items) == 0 {
			return overview, table, 0, nil
		}
	}

	tokenItem, ok := findTokenUsageItem(snapshot.Items)
	if ok {
		overview.TokenValue = numericOrZero(tokenItem.Value)
		overview.Requests = numericExtra(tokenItem.Extra, "daily_requests")
		overview.Cost = numericExtra(tokenItem.Extra, "daily_cost")
		overview.EnabledAccount = numericExtra(tokenItem.Extra, "enabled_accounts")
		overview.Card.Value = formatPrimaryNumber(overview.TokenValue)
		overview.Card.Stats = []string{
			"请求 " + formatCompactWhole(overview.Requests),
			fmt.Sprintf("成本 $%.2f", overview.Cost),
			"启用 " + formatCompactWhole(overview.EnabledAccount),
		}
	}

	accounts, fiveReset, weekReset := buildQuotaAccounts(snapshot.Items, sourceKey)
	table.Rows = make([]einkQuotaRow, 0, len(accounts))
	table.HasRows = len(accounts) > 0
	table.FiveHourReset = "5H 重置: " + nonEmpty(formatResetFooter(fiveReset), "--")
	table.WeekReset = "Week 重置: " + nonEmpty(formatResetFooter(weekReset), "--")

	var (
		criticalCount int
		alerts        []string
	)
	for _, account := range accounts {
		row := buildQuotaRow(account)
		table.Rows = append(table.Rows, row)

		if alert, critical, ok := buildAlert(account, sourceKey); ok {
			alerts = append(alerts, alert)
			if critical {
				criticalCount++
			}
		}
	}
	table.FocusRow = pickFocusQuotaRow(table.Rows)

	return overview, table, criticalCount, alerts
}

func maxSnapshotDataUpdatedAt(snapshots map[string]model.SourceSnapshot) int64 {
	var updatedAt int64
	for _, snapshot := range snapshots {
		candidate := snapshotDataUpdatedAt(snapshot)
		if candidate > updatedAt {
			updatedAt = candidate
		}
	}
	return updatedAt
}

func snapshotDataUpdatedAt(snapshot model.SourceSnapshot) int64 {
	var updatedAt int64
	for _, item := range snapshot.Items {
		if item.FetchedAt > updatedAt {
			updatedAt = item.FetchedAt
		}
	}
	if updatedAt > 0 {
		return updatedAt
	}
	return snapshot.LastFetch
}

func buildTotalOverview(claude sourceOverview, sub sourceOverview, criticalCount int) einkOverviewCard {
	return einkOverviewCard{
		Kind:  "total",
		Title: "今日合计",
		Value: formatPrimaryNumber(claude.TokenValue + sub.TokenValue),
		Label: "总 Token",
		Stats: []string{
			"总请求 " + formatCompactWhole(claude.Requests+sub.Requests),
			fmt.Sprintf("总成本 $%.2f", claude.Cost+sub.Cost),
			"告警 " + formatCompactWhole(float64(criticalCount)),
		},
		Icon: dashboardCardIcon("total"),
	}
}

func dashboardTableTitle(sourceKey string) string {
	switch sourceKey {
	case "claude_local":
		return "Claude Local 配额"
	case "claude_relay":
		return "Claude Relay 配额"
	case "codex_local":
		return "Codex Local 配额"
	case "codex":
		return "Codex 账号额度"
	case "sub2api":
		return "Sub2API 账号额度"
	default:
		return sourceKey + " 配额"
	}
}

func buildQuotaAccounts(items []model.DataItem, sourceKey string) ([]quotaAccount, string, string) {
	order := make([]string, 0)
	byName := make(map[string]*quotaAccount)
	var (
		fiveReset string
		weekReset string
	)

	for _, item := range items {
		if item.Category != "quota" {
			continue
		}

		name, window := extractQuotaNameAndWindow(item)
		if name == "" || window == "" {
			continue
		}
		if window != "5H" && window != "Week" {
			continue
		}

		key := nonEmpty(stringExtra(item.Extra, "account_id"), name)
		account, ok := byName[key]
		if !ok {
			account = &quotaAccount{Name: name, Email: stringExtra(item.Extra, "email"), SourceKey: sourceKey, FiveHour: quotaMetric{Unknown: true}, Week: quotaMetric{Unknown: true}}
			byName[key] = account
			order = append(order, key)
		}

		metric := quotaMetric{
			UsedPercent:      clampPercent(quotaUsedPercent(item)),
			RemainingPercent: clampPercent(quotaRemainingPercent(item)),
			ResetAt:          stringExtra(item.Extra, "reset_at"),
			QuotaSource:      stringExtra(item.Extra, "quota_source"),
			OnlineStatus:     stringExtra(item.Extra, "online_quota_status"),
		}
		if isLocalQuotaSource(sourceKey) && metric.QuotaSource == "estimated_cap" && numericExtra(item.Extra, "cap") <= 0 {
			metric.Unknown = true
			metric.ResetAt = ""
		}

		switch window {
		case "5H":
			account.FiveHour = metric
			fiveReset = firstResetAt(fiveReset, metric.ResetAt)
		case "Week":
			account.Week = metric
			weekReset = firstResetAt(weekReset, metric.ResetAt)
		}
	}

	result := make([]quotaAccount, 0, len(order))
	for _, name := range order {
		result = append(result, *byName[name])
	}
	return result, fiveReset, weekReset
}

func buildQuotaRow(account quotaAccount) einkQuotaRow {
	status, className := quotaStatus(account)
	return einkQuotaRow{
		Account:     account.Name,
		Email:       account.Email,
		FiveHour:    quotaPercentCell(account.FiveHour),
		Week:        quotaPercentCell(account.Week),
		Status:      status,
		StatusClass: className,
	}
}

func quotaPercentCell(metric quotaMetric) einkPercentCell {
	if metric.Unknown {
		return einkPercentCell{Percent: 0, Text: "--", ResetAt: "--", ResetShort: "--"}
	}
	return einkPercentCell{
		Percent:    metric.RemainingPercent,
		Text:       formatPercentText(metric.RemainingPercent),
		ResetAt:    nonEmpty(formatResetFooter(metric.ResetAt), "--"),
		ResetShort: shortQuotaReset(metric.ResetAt),
	}
}

func pickFocusQuotaRow(rows []einkQuotaRow) einkQuotaRow {
	if len(rows) == 0 {
		return emptyQuotaRow()
	}

	selected := rows[0]
	for _, row := range rows[1:] {
		currentSeverity := quotaRowSeverity(row)
		selectedSeverity := quotaRowSeverity(selected)
		if currentSeverity > selectedSeverity || currentSeverity == selectedSeverity && quotaRowFloor(row) < quotaRowFloor(selected) {
			selected = row
		}
	}
	return selected
}

func emptyQuotaRow() einkQuotaRow {
	return einkQuotaRow{
		Account:  "--",
		FiveHour: einkPercentCell{Percent: 0, Text: "--", ResetAt: "--", ResetShort: "--"},
		Week:     einkPercentCell{Percent: 0, Text: "--", ResetAt: "--", ResetShort: "--"},
		Status:   "--",
	}
}

func quotaRowSeverity(row einkQuotaRow) int {
	switch {
	case row.FiveHour.Text == "--" || row.Week.Text == "--":
		return 4
	case row.Week.Percent == 0:
		return 5
	case row.FiveHour.Percent <= 20 || row.Week.Percent <= 10:
		return 4
	case row.FiveHour.Percent <= 50 || row.Week.Percent <= 50 || strings.Contains(row.Status, "关注"):
		return 3
	default:
		return 1
	}
}

func quotaRowFloor(row einkQuotaRow) int {
	if row.FiveHour.Percent < row.Week.Percent {
		return row.FiveHour.Percent
	}
	return row.Week.Percent
}

func quotaStatus(account quotaAccount) (string, string) {
	switch {
	case account.FiveHour.Unknown || account.Week.Unknown:
		return codexOnlineQuotaStatusLabel(account.FiveHour.OnlineStatus, account.Week.OnlineStatus), "solid"
	case account.Week.RemainingPercent <= 0:
		return "Week 耗尽", "solid"
	case account.FiveHour.RemainingPercent >= 95 && account.Week.RemainingPercent >= 95:
		return "最佳", "solid"
	case account.FiveHour.RemainingPercent >= 75 && account.Week.RemainingPercent >= 75:
		return "充足", ""
	case account.FiveHour.RemainingPercent >= 60 && account.Week.RemainingPercent >= 60:
		return "正常", ""
	case account.FiveHour.RemainingPercent <= 20 || account.Week.RemainingPercent <= 10:
		return "告警", "solid"
	default:
		return "关注", ""
	}
}

func buildAlert(account quotaAccount, sourceKey string) (string, bool, bool) {
	displayName := account.Name
	if (sourceKey == "claude_relay" || sourceKey == "claude_local") && !strings.HasPrefix(displayName, "Claude") {
		displayName = "Claude " + displayName
	} else if (sourceKey == "codex" || sourceKey == "codex_local") && !strings.HasPrefix(displayName, "Codex") {
		displayName = "Codex " + displayName
	}

	switch {
	case account.FiveHour.Unknown || account.Week.Unknown:
		if sourceKey == "claude_local" {
			return fmt.Sprintf("%s：额度未知", displayName), false, true
		}
		return fmt.Sprintf("%s：在线额度%s", displayName, codexOnlineQuotaAlertLabel(account.FiveHour.OnlineStatus, account.Week.OnlineStatus)), false, true
	case account.Week.RemainingPercent <= 0:
		return fmt.Sprintf("%s：Week 余量 %d%%", displayName, account.Week.RemainingPercent), true, true
	case account.Week.RemainingPercent <= 10:
		return fmt.Sprintf("%s：Week 余量仅 %d%%", displayName, account.Week.RemainingPercent), true, true
	case account.FiveHour.RemainingPercent < 60:
		return fmt.Sprintf("%s：5H 余量仅 %d%%", displayName, account.FiveHour.RemainingPercent), false, true
	default:
		return "", false, false
	}
}

func isLocalQuotaSource(sourceKey string) bool {
	return sourceKey == "claude_local" || sourceKey == "codex_local"
}

func codexOnlineQuotaStatusLabel(statuses ...string) string {
	switch firstNonEmptyStatus(statuses...) {
	case "token_missing":
		return "待登录"
	case "unauthorized":
		return "需刷新"
	case "rate_limited":
		return "限流"
	case "endpoint_404":
		return "端点异常"
	case "transport_error":
		return "查询失败"
	default:
		return "额度未知"
	}
}

func codexOnlineQuotaAlertLabel(statuses ...string) string {
	switch firstNonEmptyStatus(statuses...) {
	case "token_missing":
		return "缺少 ChatGPT token"
	case "unauthorized":
		return "登录已失效"
	case "rate_limited":
		return "被上游限流"
	case "endpoint_404":
		return "端点不可用"
	case "transport_error":
		return "查询失败"
	default:
		return "不可用"
	}
}

func firstNonEmptyStatus(statuses ...string) string {
	for _, status := range statuses {
		status = strings.TrimSpace(status)
		if status != "" && status != "disabled" && status != "ok" {
			return status
		}
	}
	for _, status := range statuses {
		status = strings.TrimSpace(status)
		if status != "" {
			return status
		}
	}
	return ""
}

func findTokenUsageItem(items []model.DataItem) (model.DataItem, bool) {
	for _, item := range items {
		if item.Category == "token_usage" {
			return item, true
		}
	}
	return model.DataItem{}, false
}

func extractQuotaNameAndWindow(item model.DataItem) (string, string) {
	window := strings.TrimSpace(stringExtra(item.Extra, "window"))
	title := strings.TrimSpace(item.Title)
	title = strings.TrimPrefix(title, "账号 ")
	title = strings.TrimSuffix(title, " 额度")
	if title == "" {
		return "", window
	}
	if window == "" {
		for _, candidate := range []string{"5H", "Week"} {
			if strings.HasSuffix(title, " "+candidate) {
				window = candidate
				title = strings.TrimSuffix(title, " "+candidate)
				break
			}
		}
	} else {
		title = strings.TrimSuffix(title, " "+window)
	}
	return strings.TrimSpace(title), window
}

func formatHeaderTime(unixSeconds int64) string {
	if unixSeconds <= 0 {
		return time.Now().Format("2006-01-02 15:04")
	}
	return time.Unix(unixSeconds, 0).In(time.Local).Format("2006-01-02 15:04")
}

func formatResetFooter(resetAt string) string {
	resetAt = strings.TrimSpace(resetAt)
	if resetAt == "" {
		return ""
	}
	parsed, err := time.Parse(time.RFC3339Nano, resetAt)
	if err != nil {
		return resetAt
	}
	return parsed.In(time.Local).Format("2006-01-02 15:04")
}

func firstResetAt(current string, candidate string) string {
	if strings.TrimSpace(candidate) == "" {
		return current
	}
	if strings.TrimSpace(current) == "" {
		return candidate
	}

	currentTime, currentErr := time.Parse(time.RFC3339Nano, current)
	candidateTime, candidateErr := time.Parse(time.RFC3339Nano, candidate)
	if currentErr != nil || candidateErr != nil {
		return current
	}
	if candidateTime.Before(currentTime) {
		return candidate
	}
	return current
}

func dashboardRefreshSeconds(r *http.Request) int {
	refreshSeconds := einkDashboardDefaultRefreshSeconds
	if raw := strings.TrimSpace(r.URL.Query().Get("refresh")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			refreshSeconds = parsed
		}
	}
	if refreshSeconds < einkDashboardMinRefreshSeconds {
		return einkDashboardMinRefreshSeconds
	}
	if refreshSeconds > einkDashboardMaxRefreshSeconds {
		return einkDashboardMaxRefreshSeconds
	}
	return refreshSeconds
}

func formatPrimaryNumber(value float64) string {
	if value <= 0 {
		return "--"
	}
	if math.Abs(value-math.Round(value)) < 0.000001 {
		return formatInt64(int64(math.Round(value)))
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", value), "0"), ".")
}

func formatTokenMillions(value float64) string {
	if value <= 0 {
		return "--"
	}
	tenths := int64(math.Round(value / 100000))
	return fmt.Sprintf("%d.%dM", tenths/10, tenths%10)
}

func splitDashboardAlert(alerts []string) (string, string) {
	if len(alerts) == 0 {
		return "", ""
	}
	alert := alerts[0]
	for _, separator := range []string{"：", ":"} {
		if index := strings.Index(alert, separator); index >= 0 {
			return strings.TrimSpace(alert[:index]), strings.TrimSpace(alert[index+len(separator):])
		}
	}
	return alert, ""
}

func mergeDashboardAlerts(groups ...[]string) []string {
	alerts := make([]string, 0)
	for _, group := range groups {
		alerts = append(alerts, group...)
	}
	sort.SliceStable(alerts, func(i, j int) bool {
		leftPercent := alertRemainingPercent(alerts[i])
		rightPercent := alertRemainingPercent(alerts[j])
		if leftPercent != rightPercent {
			return leftPercent < rightPercent
		}
		return alertSourcePriority(alerts[i]) < alertSourcePriority(alerts[j])
	})
	return alerts
}

func alertSourcePriority(alert string) int {
	if strings.HasPrefix(alert, "Claude ") {
		return 0
	}
	if strings.HasPrefix(alert, "Codex ") {
		return 1
	}
	return 2
}

func alertRemainingPercent(alert string) int {
	percentIndex := strings.LastIndex(alert, "%")
	if percentIndex < 0 {
		return 101
	}

	start := percentIndex
	for start > 0 {
		char := alert[start-1]
		if char < '0' || char > '9' {
			break
		}
		start--
	}
	if start == percentIndex {
		return 101
	}

	value, err := strconv.Atoi(alert[start:percentIndex])
	if err != nil {
		return 101
	}
	return value
}

func formatCompactWhole(value float64) string {
	return formatInt64(int64(math.Round(value)))
}

func formatPercentText(value int) string {
	return fmt.Sprintf("%d%%", value)
}

func clampPercent(value float64) int {
	switch {
	case value < 0:
		return 0
	case value > 100:
		return 100
	default:
		return int(math.Round(value))
	}
}

func quotaUsedPercent(item model.DataItem) float64 {
	if value, ok := floatValue(item.Extra["used_percent"]); ok {
		return value
	}
	if value, ok := floatValue(item.Extra["remaining_percent"]); ok {
		return 100 - value
	}
	if number, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(item.Value), "%"), 64); err == nil {
		return 100 - number
	}
	return 0
}

func quotaRemainingPercent(item model.DataItem) float64 {
	if value, ok := floatValue(item.Extra["remaining_percent"]); ok {
		return value
	}
	if value, ok := floatValue(item.Extra["used_percent"]); ok {
		return 100 - value
	}
	if number, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(item.Value), "%"), 64); err == nil {
		return number
	}
	return 0
}

func numericOrZero(value string) float64 {
	value = strings.TrimSpace(strings.ReplaceAll(value, ",", ""))
	if value == "" {
		return 0
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0
	}
	return number
}

func numericExtra(extra map[string]any, key string) float64 {
	if extra == nil {
		return 0
	}
	value, ok := floatValue(extra[key])
	if !ok {
		return 0
	}
	return value
}

func stringExtra(extra map[string]any, key string) string {
	if extra == nil {
		return ""
	}
	return stringValue(extra[key])
}

func floatValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case int:
		return float64(typed), true
	case int8:
		return float64(typed), true
	case int16:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case uint:
		return float64(typed), true
	case uint8:
		return float64(typed), true
	case uint16:
		return float64(typed), true
	case uint32:
		return float64(typed), true
	case uint64:
		return float64(typed), true
	case float32:
		return float64(typed), true
	case float64:
		return typed, true
	case string:
		number, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return 0, false
		}
		return number, true
	default:
		return 0, false
	}
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return fmt.Sprintf("%v", typed)
	}
}

func formatInt64(value int64) string {
	negative := value < 0
	if negative {
		value = -value
	}

	text := strconv.FormatInt(value, 10)
	if len(text) <= 3 {
		if negative {
			return "-" + text
		}
		return text
	}

	var groups []string
	for len(text) > 3 {
		groups = append([]string{text[len(text)-3:]}, groups...)
		text = text[:len(text)-3]
	}
	groups = append([]string{text}, groups...)

	joined := strings.Join(groups, ",")
	if negative {
		return "-" + joined
	}
	return joined
}

func nonEmpty(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func dashboardCardIcon(kind string) template.HTML {
	switch kind {
	case "claude_relay", "claude_local":
		return template.HTML(`<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="3" width="18" height="18" rx="2"/><rect x="7" y="7" width="10" height="10" rx="1.5"/><path d="M9.3 14.4v-4.8h2.2c1.6 0 2.6 1 2.6 2.4s-1 2.4-2.6 2.4H9.3Z"/><path d="M15.2 9.6v4.8"/></svg>`)
	case "sub2api":
		return template.HTML(`<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M7 18a4 4 0 0 1 0-8c.2 0 .5 0 .7.1A5.5 5.5 0 1 1 18 12h-1"/><path d="M8.5 18.5v2"/><path d="M12 16.5v4"/><path d="M15.5 18.5v2"/><circle cx="8.5" cy="20.5" r=".7" fill="#111111"/><circle cx="12" cy="20.5" r=".7" fill="#111111"/><circle cx="15.5" cy="20.5" r=".7" fill="#111111"/></svg>`)
	case "codex", "codex_local":
		return template.HTML(`<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M7 18a4 4 0 0 1 0-8c.2 0 .5 0 .7.1A5.5 5.5 0 1 1 18 12h-1"/><path d="M8.5 18.5v2"/><path d="M12 16.5v4"/><path d="M15.5 18.5v2"/><circle cx="8.5" cy="20.5" r=".7" fill="#111111"/><circle cx="12" cy="20.5" r=".7" fill="#111111"/><circle cx="15.5" cy="20.5" r=".7" fill="#111111"/></svg>`)
	default:
		return template.HTML(`<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 20h16"/><path d="M7 17V9"/><path d="M12 17V5"/><path d="M17 17v-7"/></svg>`)
	}
}

func writeDashboardError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	message := "dashboard render failed"
	if err != nil {
		message = err.Error()
	}
	_, _ = fmt.Fprintf(w, "<!doctype html><html lang=\"zh-CN\"><meta charset=\"utf-8\"><title>Error</title><body style=\"font-family:sans-serif;padding:24px\">%s</body></html>", template.HTMLEscapeString(message))
}
