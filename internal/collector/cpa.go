package collector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"infohub/internal/config"
	"infohub/internal/model"
	"infohub/internal/store"
)

const (
	cpaUsageMachine       = "cpa"
	cpaUsageBatchSize     = 500
	cpaUsageMaxBatches    = 20
	cpaClaudeUsageURL     = "https://api.anthropic.com/api/oauth/usage"
	cpaMaxResponseBytes   = 8 << 20
	cpaQuotaSourceOAuth   = "cpa_oauth_usage"
	cpaUsageSourcePrefix  = "cpa_"
	cpaManagementAPIRoute = "/v0/management/"
)

// CPACollector reads a CLIProxyAPI management API. Claude 5H/Week quota is
// fetched through CPA's api-call proxy with the stored OAuth credential;
// per-request token usage is drained from CPA's destructive usage queue and
// cached in sqlite so today/week totals survive CPA's short queue retention.
type CPACollector struct {
	cfg        config.CPACollectorConfig
	logger     *slog.Logger
	httpClient *http.Client
	usageStore CPAUsageStore
	now        func() time.Time

	quotaM       sync.Mutex
	quotaAt      time.Time
	quotaItems   []model.DataItem
	quotaAccount cpaAuthFile
}

// CPAUsageStore caches drained CPA usage records (the sqlite store).
type CPAUsageStore interface {
	SaveIngestedUsage(machine, source string, resetFiles []string, records []store.LocalUsageRecord) error
	ListLocalUsageRecords(source string, start time.Time, end time.Time) ([]store.LocalUsageRecord, error)
}

type cpaAuthFile struct {
	AuthIndex string `json:"auth_index"`
	Provider  string `json:"provider"`
	Type      string `json:"type"`
	Email     string `json:"email"`
	Label     string `json:"label"`
	Disabled  bool   `json:"disabled"`
	Status    string `json:"status"`
}

type cpaUsageRecord struct {
	Timestamp time.Time `json:"timestamp"`
	Provider  string    `json:"provider"`
	Model     string    `json:"model"`
	AuthIndex string    `json:"auth_index"`
	RequestID string    `json:"request_id"`
	Failed    bool      `json:"failed"`
	Tokens    struct {
		InputTokens         float64 `json:"input_tokens"`
		OutputTokens        float64 `json:"output_tokens"`
		ReasoningTokens     float64 `json:"reasoning_tokens"`
		CacheReadTokens     float64 `json:"cache_read_tokens"`
		CacheCreationTokens float64 `json:"cache_creation_tokens"`
		TotalTokens         float64 `json:"total_tokens"`
	} `json:"tokens"`
}

func NewCPACollector(cfg config.CPACollectorConfig, usageStore CPAUsageStore, logger *slog.Logger) *CPACollector {
	return &CPACollector{
		cfg:        cfg,
		logger:     logger,
		httpClient: newHTTPClient(cfg.Timeout()),
		usageStore: usageStore,
		now:        time.Now,
	}
}

func (c *CPACollector) Name() string {
	return "cpa"
}

func (c *CPACollector) usageSource() string {
	return cpaUsageSourcePrefix + c.cfg.Provider
}

func (c *CPACollector) Collect(ctx context.Context) ([]model.DataItem, error) {
	now := c.now()
	account, quotaItems, quotaErr := c.quota(ctx, now)
	drained, drainErr := c.drainUsage(ctx, account)
	if drainErr != nil && c.logger != nil {
		c.logger.Warn("cpa usage drain failed", "drained", drained, "error", drainErr)
	}

	usageItems, err := c.usageItems(now)
	if err != nil {
		return nil, err
	}
	if quotaErr != nil && drainErr != nil {
		return nil, fmt.Errorf("cpa quota: %v; usage: %v", quotaErr, drainErr)
	}
	return append(usageItems, quotaItems...), nil
}

// quota returns the cached quota items, refreshing them through api-call at
// most once per quota interval. A failed refresh keeps the last good items.
func (c *CPACollector) quota(ctx context.Context, now time.Time) (cpaAuthFile, []model.DataItem, error) {
	c.quotaM.Lock()
	defer c.quotaM.Unlock()
	if !c.quotaAt.IsZero() && now.Sub(c.quotaAt) < c.cfg.QuotaInterval() {
		return c.quotaAccount, c.quotaItems, nil
	}

	account, err := c.selectAccount(ctx)
	if err == nil {
		var items []model.DataItem
		items, err = c.fetchQuota(ctx, account)
		if err == nil {
			c.quotaAt, c.quotaItems, c.quotaAccount = now, items, account
			return account, items, nil
		}
	}
	if c.logger != nil {
		c.logger.Warn("cpa quota refresh failed", "error", err)
	}
	return c.quotaAccount, c.quotaItems, err
}

func (c *CPACollector) selectAccount(ctx context.Context) (cpaAuthFile, error) {
	var payload struct {
		Files []cpaAuthFile `json:"files"`
	}
	if err := c.management(ctx, http.MethodGet, "auth-files", nil, &payload); err != nil {
		return cpaAuthFile{}, err
	}
	for _, file := range payload.Files {
		provider := strings.ToLower(firstNonEmpty(file.Provider, file.Type))
		if file.Disabled || file.AuthIndex == "" || provider != c.cfg.Provider {
			continue
		}
		if c.cfg.Account != "" && !strings.EqualFold(file.Email, c.cfg.Account) && !strings.EqualFold(file.Label, c.cfg.Account) {
			continue
		}
		return file, nil
	}
	return cpaAuthFile{}, fmt.Errorf("no active %s credential in cpa", c.cfg.Provider)
}

func (c *CPACollector) fetchQuota(ctx context.Context, account cpaAuthFile) ([]model.DataItem, error) {
	if c.cfg.Provider != "claude" {
		return nil, fmt.Errorf("cpa quota for provider %q is not supported", c.cfg.Provider)
	}
	request := map[string]any{
		"auth_index": account.AuthIndex,
		"method":     http.MethodGet,
		"url":        cpaClaudeUsageURL,
		// CPA substitutes $TOKEN$ with the stored OAuth access token.
		"header": map[string]string{
			"Authorization":  "Bearer $TOKEN$",
			"Content-Type":   "application/json",
			"anthropic-beta": "oauth-2025-04-20",
		},
	}
	var response struct {
		StatusCode int    `json:"status_code"`
		Body       string `json:"body"`
	}
	if err := c.management(ctx, http.MethodPost, "api-call", request, &response); err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("claude usage upstream status %d", response.StatusCode)
	}
	// Decode only the two windows: other keys (limits, spend, ...) vary in shape.
	var usage struct {
		FiveHour *claudeOnlineUsageWindow `json:"five_hour"`
		SevenDay *claudeOnlineUsageWindow `json:"seven_day"`
	}
	if err := json.Unmarshal([]byte(response.Body), &usage); err != nil {
		return nil, fmt.Errorf("decode claude usage: %w", err)
	}

	accountID := "cpa:" + account.AuthIndex
	var items []model.DataItem
	for _, window := range []struct {
		name  string
		value *claudeOnlineUsageWindow
	}{{"5H", usage.FiveHour}, {"Week", usage.SevenDay}} {
		if window.value == nil {
			continue
		}
		item := sub2apiQuotaItem(c.Name(), accountID, c.cfg.DisplayName, window.name, window.value.Utilization, window.value.ResetsAt)
		item.Extra["product"] = "claude"
		item.Extra["email"] = account.Email
		item.Extra["quota_source"] = cpaQuotaSourceOAuth
		items = append(items, item)
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("claude usage has no five_hour or seven_day window")
	}
	return items, nil
}

// drainUsage pops the CPA usage queue until it is empty and caches matching
// records. Popped records cannot be returned to CPA, so each batch is saved
// before the next pop.
func (c *CPACollector) drainUsage(ctx context.Context, account cpaAuthFile) (int, error) {
	if c.usageStore == nil {
		return 0, fmt.Errorf("cpa usage cache requires the sqlite store")
	}
	saved := 0
	for range cpaUsageMaxBatches {
		var raw []json.RawMessage
		if err := c.management(ctx, http.MethodGet, fmt.Sprintf("usage-queue?count=%d", cpaUsageBatchSize), nil, &raw); err != nil {
			return saved, err
		}
		records := make([]store.LocalUsageRecord, 0, len(raw))
		for _, entry := range raw {
			record, ok := c.convertUsage(entry, account)
			if ok {
				records = append(records, record)
			}
		}
		if len(records) > 0 {
			if err := c.usageStore.SaveIngestedUsage(cpaUsageMachine, c.usageSource(), nil, records); err != nil {
				return saved, fmt.Errorf("cache cpa usage: %w", err)
			}
			saved += len(records)
		}
		if len(raw) < cpaUsageBatchSize {
			return saved, nil
		}
	}
	return saved, nil
}

func (c *CPACollector) convertUsage(entry json.RawMessage, account cpaAuthFile) (store.LocalUsageRecord, bool) {
	// The queue may carry payloads encoded as JSON strings.
	var text string
	if json.Unmarshal(entry, &text) == nil {
		entry = json.RawMessage(text)
	}
	var record cpaUsageRecord
	if err := json.Unmarshal(entry, &record); err != nil || record.Timestamp.IsZero() {
		return store.LocalUsageRecord{}, false
	}
	if !strings.EqualFold(record.Provider, c.cfg.Provider) {
		return store.LocalUsageRecord{}, false
	}
	if c.cfg.Account != "" && account.AuthIndex != "" && record.AuthIndex != account.AuthIndex {
		return store.LocalUsageRecord{}, false
	}
	tokens := record.Tokens
	total := tokens.TotalTokens
	if total <= 0 {
		total = tokens.InputTokens + tokens.OutputTokens + tokens.CacheReadTokens + tokens.CacheCreationTokens
	}
	// request_id dedupes re-delivered records; fall back to a timestamp key.
	key := record.RequestID
	if key == "" {
		key = record.AuthIndex + "@" + record.Timestamp.UTC().Format(time.RFC3339Nano)
	}
	return store.LocalUsageRecord{
		FilePath:      "cpa:" + key,
		At:            record.Timestamp,
		Model:         record.Model,
		Input:         tokens.InputTokens,
		Output:        tokens.OutputTokens,
		CacheRead:     tokens.CacheReadTokens,
		CacheCreation: tokens.CacheCreationTokens,
		Reasoning:     tokens.ReasoningTokens,
		Total:         total,
		Quota5hUsed:   -1,
		QuotaWeekUsed: -1,
	}, true
}

// usageItems totals the cached records for today and the calendar week in
// the same token_usage_product(_week) shape Sub2API reports for Codex.
func (c *CPACollector) usageItems(now time.Time) ([]model.DataItem, error) {
	if c.usageStore == nil {
		return nil, nil
	}
	local := now.In(model.ShanghaiLocation)
	today, weekStart := model.UsageDates(local)
	dayStart, _ := time.ParseInLocation("2006-01-02", today, model.ShanghaiLocation)
	weekBegin, _ := time.ParseInLocation("2006-01-02", weekStart, model.ShanghaiLocation)
	records, err := c.usageStore.ListLocalUsageRecords(c.usageSource(), weekBegin, dayStart.AddDate(0, 0, 1))
	if err != nil {
		return nil, err
	}
	var dayTokens, weekTokens float64
	var dayRequests int
	for _, record := range records {
		weekTokens += record.Total
		if !record.At.Before(dayStart) {
			dayTokens += record.Total
			dayRequests++
		}
	}
	product := c.cfg.Provider
	display := c.cfg.DisplayName
	return []model.DataItem{
		{
			Source: c.Name(), Category: "token_usage_product", Title: display + " 今日 Token 用量", Value: formatFloat(dayTokens),
			Extra: map[string]any{"scope": "product", "product": product, "daily_requests": dayRequests, "start_date": today, "end_date": today, "timezone": "Asia/Shanghai"},
		},
		{
			Source: c.Name(), Category: "token_usage_product_week", Title: display + " 本周 Token 用量", Value: formatFloat(weekTokens),
			Extra: map[string]any{"product": product, "start_date": weekStart, "end_date": today, "timezone": "Asia/Shanghai"},
		},
	}, nil
}

func (c *CPACollector) management(ctx context.Context, method, path string, body any, target any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.BaseURL+cpaManagementAPIRoute+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.ManagementKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("cpa %s: %w", strings.SplitN(path, "?", 2)[0], err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, cpaMaxResponseBytes))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &httpStatusError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(payload))}
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return fmt.Errorf("decode cpa %s: %w", strings.SplitN(path, "?", 2)[0], err)
	}
	return nil
}
