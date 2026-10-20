package api

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"infohub/internal/model"
)

type DashboardOptions struct {
	Template string
	Accounts []string
}

var errDashboardTemplate = errors.New("unsupported dashboard template")

func dashboardErrorStatus(err error) int {
	if errors.Is(err, errDashboardTemplate) {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

func dashboardAccounts(accounts []string) []string {
	if accounts == nil {
		return []string{"cycyzg@gmail.com", "qqcyan@gmail.com"}
	}
	return accounts
}

func filterCodexAccounts(rows []einkQuotaRow, accounts []string) []einkQuotaRow {
	result := make([]einkQuotaRow, 0, len(accounts))
	seen := map[string]bool{}
	for _, email := range accounts {
		email = strings.ToLower(strings.TrimSpace(email))
		if email == "" || seen[email] {
			continue
		}
		seen[email] = true
		var matches []einkQuotaRow
		for _, row := range rows {
			if strings.EqualFold(strings.TrimSpace(row.Email), email) || (row.Email == "" && strings.EqualFold(strings.TrimSpace(row.Account), email)) {
				matches = append(matches, row)
			}
		}
		row := pickFocusQuotaRow(matches)
		row.Account, row.Email = email, email
		result = append(result, row)
	}
	return result
}

func (h *Handler) dashboardPage(r *http.Request) (einkDashboardPage, error) {
	layout := strings.TrimSpace(r.URL.Query().Get("template"))
	if layout == "" {
		layout = h.dashboardOptions.Template
	}
	if layout == "" {
		layout = "trio"
	}
	if layout != "trio" && layout != "codex" && layout != "deepseek-codex" {
		return einkDashboardPage{}, fmt.Errorf("%w %q", errDashboardTemplate, layout)
	}
	var page einkDashboardPage
	var snapshots map[string]model.SourceSnapshot
	if h.dashboardMockEnabled {
		page = buildMockEInkDashboardPage(dashboardRefreshSeconds(r))
	} else {
		var err error
		if snapshots, err = h.store.GetAll(); err != nil {
			return page, err
		}
		page = buildEInkDashboardPage(snapshots, dashboardRefreshSeconds(r), h.dashboardSources)
	}
	page.Device.Template = layout
	page.Device.CodexAccounts = filterCodexAccounts(page.Device.CodexRows, dashboardAccounts(h.dashboardOptions.Accounts))
	if layout == "trio" {
		if h.dashboardMockEnabled {
			page.Device.Platforms = mockTrioPlatforms(page.Device)
		} else {
			page.Device.Platforms = buildTrioPlatforms(snapshots, page.Device)
			if updatedAt := trioUpdatedAt(snapshots, h.dashboardSources); updatedAt > 0 {
				page.UpdatedAtUnix, page.UpdatedAt = updatedAt, formatHeaderTime(updatedAt)
				page.Device.UpdatedAtUnix, page.Device.UpdatedAt = updatedAt, page.UpdatedAt
			}
		}
	}
	return page, nil
}

func buildCodexWeek(items []model.DataItem) einkDeviceOverview {
	return buildProductWeek(items, "codex", "Codex 本周用量")
}

func buildProductWeek(items []model.DataItem, product, title string) einkDeviceOverview {
	result := einkDeviceOverview{Title: title, Value: "--", DisplayValue: "--", Label: "Tokens"}
	var total float64
	found := false
	start, end := "", ""
	for _, item := range items {
		if item.Category != "token_usage_product_week" || stringExtra(item.Extra, "product") != product {
			continue
		}
		value, err := strconv.ParseFloat(item.Value, 64)
		if err != nil || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			return result
		}
		itemStart, itemEnd := stringExtra(item.Extra, "start_date"), stringExtra(item.Extra, "end_date")
		endDate, dateErr := time.Parse("2006-01-02", itemEnd)
		_, expectedStart := model.UsageDates(endDate)
		if dateErr != nil || itemStart != expectedStart {
			return result
		}
		if found && (start != itemStart || end != itemEnd) {
			return result
		}
		start, end, found = itemStart, itemEnd, true
		total += value
	}
	if found {
		result.Value = formatInt64(int64(math.Round(total)))
		result.ValueNumeric = int64(math.Round(total))
		result.DisplayValue = fmt.Sprintf("%.1fM", total/1e6)
	}
	return result
}

func dashboardUsageRanges(items []model.DataItem, updatedAt int64) (string, string) {
	if updatedAt <= 0 {
		return "--", "--"
	}
	today, week := model.UsageDates(time.Unix(updatedAt, 0))
	for _, item := range items {
		if stringExtra(item.Extra, "product") != "codex" {
			continue
		}
		if date := stringExtra(item.Extra, "start_date"); date != "" {
			if item.Category == "token_usage_product" {
				today = date
			}
			if item.Category == "token_usage_product_week" {
				week = date
			}
		}
	}
	short := func(value string) string {
		if date, err := time.Parse("2006-01-02", value); err == nil {
			return date.Format("01/02")
		}
		return "--"
	}
	return short(today) + " 00:00 至今", short(week) + " 周一至今"
}

func shortQuotaReset(value string) string {
	if date, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return date.In(model.ShanghaiLocation).Format("01/02 15:04")
	}
	return "--"
}
