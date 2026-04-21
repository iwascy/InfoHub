package collector

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"

	"infohub/internal/config"
	"infohub/internal/model"
)

type Sub2APICollector struct {
	service *serviceJSONClient
}

func NewSub2APICollector(cfg config.HTTPCollectorConfig, logger *slog.Logger) *Sub2APICollector {
	return &Sub2APICollector{
		service: newServiceJSONClient("sub2api", cfg, logger),
	}
}

func (c *Sub2APICollector) Name() string {
	return "sub2api"
}

func (c *Sub2APICollector) Collect(ctx context.Context) ([]model.DataItem, error) {
	session, err := c.service.newSession(ctx)
	if err != nil {
		return nil, err
	}

	query := url.Values{
		"page":      {"1"},
		"page_size": {"1000"},
		"platform":  {"openai"},
		"type":      {"oauth"},
		"status":    {"active"},
	}
	accountsPayload, err := session.fetchJSON(ctx, http.MethodGet, "accounts", query, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch sub2api accounts: %w", err)
	}

	rawAccounts, ok := nestedValue(accountsPayload, "data.items")
	if !ok {
		return nil, fmt.Errorf("sub2api accounts payload missing data.items")
	}
	accountList, ok := rawAccounts.([]any)
	if !ok {
		return nil, fmt.Errorf("sub2api accounts list is not an array")
	}

	type accountQuota struct {
		ID        string
		RawID     any
		Name      string
		Used5h    float64
		Reset5h   string
		UsedWeek  float64
		ResetWeek string
	}

	enabledAccounts := make([]accountQuota, 0, len(accountList))
	accountIDs := make([]any, 0, len(accountList))
	for _, rawAccount := range accountList {
		account, ok := rawAccount.(map[string]any)
		if !ok || !sub2apiAccountEnabled(account) {
			continue
		}

		accountID := firstString(account, "id", "accountId", "account_id")
		rawID, hasRawID := account["id"]
		if !hasRawID {
			rawID = accountID
		}
		name := firstString(account, "name", "email", "accountName")
		if name == "" {
			name = accountID
		}
		if name == "" {
			name = "未命名账号"
		}

		enabledAccounts = append(enabledAccounts, accountQuota{
			ID:        accountID,
			RawID:     rawID,
			Name:      name,
			Used5h:    floatPath(account, "extra.codex_5h_used_percent"),
			Reset5h:   firstStringValue(stringCandidate(account, "extra.codex_5h_reset_at")),
			UsedWeek:  floatPath(account, "extra.codex_7d_used_percent"),
			ResetWeek: firstStringValue(stringCandidate(account, "extra.codex_7d_reset_at")),
		})
		if rawID != nil && stringify(rawID) != "" {
			accountIDs = append(accountIDs, rawID)
		}
	}

	statsByAccount, err := c.fetchTodayStats(ctx, session, accountIDs)
	if err != nil {
		return nil, err
	}

	var (
		totalTokens   float64
		totalRequests float64
		totalCost     float64
		items         []model.DataItem
		names         []string
	)

	for _, account := range enabledAccounts {
		names = append(names, account.Name)
		if stats, ok := statsByAccount[account.ID]; ok {
			totalTokens += floatPath(stats, "tokens")
			totalRequests += floatPath(stats, "requests")
			totalCost += floatPath(stats, "cost")
		}

		items = append(items,
			sub2apiQuotaItem(c.Name(), account.ID, account.Name, "5H", account.Used5h, account.Reset5h),
			sub2apiQuotaItem(c.Name(), account.ID, account.Name, "Week", account.UsedWeek, account.ResetWeek),
		)
	}

	items = append([]model.DataItem{{
		Source:   c.Name(),
		Category: "token_usage",
		Title:    "今日 Token 用量",
		Value:    formatFloat(totalTokens),
		Extra: map[string]any{
			"enabled_accounts":      len(enabledAccounts),
			"enabled_account_names": names,
			"daily_requests":        totalRequests,
			"daily_cost":            totalCost,
		},
		FetchedAt: 0,
	}}, items...)

	return withFetchedAt(items), nil
}

func (c *Sub2APICollector) fetchTodayStats(ctx context.Context, session *serviceSession, accountIDs []any) (map[string]map[string]any, error) {
	if len(accountIDs) == 0 {
		return map[string]map[string]any{}, nil
	}

	requestBodies := []map[string]any{
		{"account_ids": accountIDs},
		{"accountIds": accountIDs},
		{"ids": accountIDs},
	}

	var lastErr error
	for _, body := range requestBodies {
		payload, err := session.fetchJSON(ctx, http.MethodPost, "today_stats", nil, body)
		if err != nil {
			lastErr = err
			continue
		}

		statsByAccount := map[string]map[string]any{}
		rawStats, ok := nestedValue(payload, "data.stats")
		if !ok {
			return statsByAccount, fmt.Errorf("sub2api today stats payload missing data.stats")
		}

		statsMap, ok := rawStats.(map[string]any)
		if !ok {
			return statsByAccount, fmt.Errorf("sub2api today stats is not an object")
		}

		for accountID, rawStat := range statsMap {
			stat, ok := rawStat.(map[string]any)
			if !ok {
				continue
			}
			statsByAccount[accountID] = stat
		}

		return statsByAccount, nil
	}

	return nil, fmt.Errorf("fetch sub2api today stats: %w", lastErr)
}

func sub2apiAccountEnabled(account map[string]any) bool {
	status := firstString(account, "status")
	if status != "" && !stringsEqualFold(status, "active") {
		return false
	}

	if schedulable, ok := account["schedulable"]; ok {
		if enabled, ok := boolValue(schedulable); ok && !enabled {
			return false
		}
	}

	return true
}

func sub2apiQuotaItem(source, accountID, name, window string, usedPercent float64, resetAt string) model.DataItem {
	extra := map[string]any{
		"account_id":        accountID,
		"used_percent":      usedPercent,
		"remaining_percent": remainingPercent(usedPercent),
		"window":            window,
	}
	if resetAt != "" {
		extra["reset_at"] = resetAt
	}

	return model.DataItem{
		Source:    source,
		Category:  "quota",
		Title:     fmt.Sprintf("账号 %s %s 额度", name, window),
		Value:     formatPercent(remainingPercent(usedPercent)),
		Extra:     extra,
		FetchedAt: 0,
	}
}
