package collector

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"infohub/internal/config"
	"infohub/internal/model"
)

type ClaudeRelayCollector struct {
	service *serviceJSONClient
}

func NewClaudeRelayCollector(cfg config.HTTPCollectorConfig, logger *slog.Logger) *ClaudeRelayCollector {
	return &ClaudeRelayCollector{
		service: newServiceJSONClient("claude_relay", cfg, logger),
	}
}

func (c *ClaudeRelayCollector) Name() string {
	return "claude_relay"
}

func (c *ClaudeRelayCollector) Collect(ctx context.Context) ([]model.DataItem, error) {
	session, err := c.service.newSession(ctx)
	if err != nil {
		return nil, err
	}

	accountsPayload, err := session.fetchJSON(ctx, "GET", "accounts", nil, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch claude accounts: %w", err)
	}
	usagePayload, err := session.fetchJSON(ctx, "GET", "usage", nil, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch claude account usage: %w", err)
	}

	rawAccounts, ok := nestedValue(accountsPayload, "data")
	if !ok {
		return nil, fmt.Errorf("claude accounts payload missing data")
	}
	accountList, ok := rawAccounts.([]any)
	if !ok {
		return nil, fmt.Errorf("claude accounts data is not an array")
	}

	usageByAccount := map[string]any{}
	if rawUsage, ok := nestedValue(usagePayload, "data"); ok {
		if usageMap, ok := rawUsage.(map[string]any); ok {
			usageByAccount = usageMap
		}
	}

	var (
		totalAllTokens float64
		totalTokens    float64
		totalRequests  float64
		totalCost      float64
		enabledNames   []string
		items          []model.DataItem
	)

	for _, rawAccount := range accountList {
		account, ok := rawAccount.(map[string]any)
		if !ok || !claudeRelayAccountVisible(account) {
			continue
		}

		accountID := firstString(account, "id", "accountId", "account_id")
		name := firstString(account, "name", "email", "accountName")
		if name == "" {
			name = accountID
		}
		if name == "" {
			name = "未命名账号"
		}
		enabledNames = append(enabledNames, name)

		totalAllTokens += floatPath(account, "usage.daily.allTokens")
		totalTokens += floatPath(account, "usage.daily.tokens")
		totalRequests += floatPath(account, "usage.daily.requests")
		totalCost += floatPath(account, "usage.daily.cost")

		usageRecord := account
		if accountID != "" {
			if rawUsage, ok := usageByAccount[accountID]; ok {
				if usageMap, ok := rawUsage.(map[string]any); ok {
					usageRecord = usageMap
				}
			}
		}

		items = append(items,
			claudeRelayQuotaItem(c.Name(), accountID, name, "5H", firstFloat(
				floatCandidate(usageRecord, "fiveHour.utilization"),
				floatCandidate(account, "claudeUsage.fiveHour.utilization"),
			), firstStringValue(
				stringCandidate(usageRecord, "fiveHour.resetsAt"),
				stringCandidate(account, "claudeUsage.fiveHour.resetsAt"),
			), firstFloat(
				floatCandidate(usageRecord, "fiveHour.remainingSeconds"),
			)),
			claudeRelayQuotaItem(c.Name(), accountID, name, "Week", firstFloat(
				floatCandidate(usageRecord, "sevenDay.utilization"),
				floatCandidate(account, "claudeUsage.sevenDay.utilization"),
			), firstStringValue(
				stringCandidate(usageRecord, "sevenDay.resetsAt"),
				stringCandidate(account, "claudeUsage.sevenDay.resetsAt"),
			), firstFloat(
				floatCandidate(usageRecord, "sevenDay.remainingSeconds"),
			)),
		)
	}

	tokenValue := totalAllTokens
	if tokenValue == 0 {
		tokenValue = totalTokens
	}

	tokenExtra := map[string]any{
		"enabled_accounts":      len(enabledNames),
		"enabled_account_names": enabledNames,
		"daily_tokens":          totalTokens,
		"daily_requests":        totalRequests,
		"daily_cost":            totalCost,
	}

	items = append([]model.DataItem{{
		Source:    c.Name(),
		Category:  "token_usage",
		Title:     "今日 Token 用量",
		Value:     formatFloat(tokenValue),
		Extra:     tokenExtra,
		FetchedAt: 0,
	}}, items...)

	return withFetchedAt(items), nil
}

func claudeRelayAccountEnabled(account map[string]any) bool {
	if !claudeRelayAccountVisible(account) {
		return false
	}

	if schedulable, ok := account["schedulable"]; ok {
		if enabled, ok := boolValue(schedulable); ok && !enabled {
			return false
		}
	}

	return true
}

func claudeRelayAccountVisible(account map[string]any) bool {
	isActive, ok := nestedValue(account, "isActive")
	if ok {
		if active, ok := boolValue(isActive); ok && !active {
			return false
		}
	}

	status := firstString(account, "status")
	if stringsEqualFold(status, "inactive") || stringsEqualFold(status, "disabled") {
		return false
	}

	return true
}

func claudeRelayQuotaItem(source, accountID, name, window string, usedPercent float64, resetAt string, remainingSeconds float64) model.DataItem {
	extra := map[string]any{
		"account_id":        accountID,
		"used_percent":      usedPercent,
		"remaining_percent": remainingPercent(usedPercent),
		"remaining_seconds": remainingSeconds,
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

func nestedFloat(payload any, path string) (float64, bool) {
	value, ok := nestedValue(payload, path)
	if !ok {
		return 0, false
	}
	return floatValue(value)
}

func nestedString(payload any, path string) (string, bool) {
	value, ok := nestedValue(payload, path)
	if !ok {
		return "", false
	}
	text := stringify(value)
	return text, text != ""
}

func floatCandidate(payload any, path string) floatWithOK {
	value, ok := nestedFloat(payload, path)
	return floatWithOK{value: value, ok: ok}
}

func stringCandidate(payload any, path string) stringWithOK {
	value, ok := nestedString(payload, path)
	return stringWithOK{value: value, ok: ok}
}

func firstFloat(values ...floatWithOK) float64 {
	for _, value := range values {
		if value.ok {
			return value.value
		}
	}
	return 0
}

func firstStringValue(values ...stringWithOK) string {
	for _, value := range values {
		if value.ok && value.value != "" {
			return value.value
		}
	}
	return ""
}

func floatPath(payload any, path string) float64 {
	value, ok := nestedFloat(payload, path)
	if !ok {
		return 0
	}
	return value
}

func stringsEqualFold(left, right string) bool {
	return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right))
}

type floatWithOK struct {
	value float64
	ok    bool
}

type stringWithOK struct {
	value string
	ok    bool
}
