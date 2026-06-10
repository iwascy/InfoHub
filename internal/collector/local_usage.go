package collector

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"sort"
	"strings"
	"time"

	"infohub/internal/config"
	"infohub/internal/localscan"
	"infohub/internal/model"
	"infohub/internal/store"
)

const (
	localClaudeSource = localscan.SourceClaude
	localCodexSource  = localscan.SourceCodex
)

// Parsing/scanning lives in internal/localscan so the infohub-agent binary
// can reuse it; the aliases keep this package's historical names working.
type (
	localUsageEvent       = localscan.Event
	localQuotaObservation = localscan.QuotaObservation
	localRateLimits       = localscan.RateLimits
)

type LocalUsageCollector struct {
	source            string
	cfg               config.LocalCollectorConfig
	logger            *slog.Logger
	now               func() time.Time
	store             store.LocalUsageStateStore
	onlineClaudeQuota onlineQuotaFetcher
	onlineCodexQuota  onlineQuotaFetcher
}

type onlineQuotaFetcher interface {
	FetchRateLimits(context.Context) (localRateLimits, bool, error)
	LastStatus() string
}

type localUsageBucket struct {
	Tokens        float64
	Input         float64
	Output        float64
	CacheRead     float64
	CacheCreation float64
	Reasoning     float64
	Messages      float64
	Models        map[string]float64
}

type localWindow struct {
	Key       string
	Label     string
	Title     string
	Start     time.Time
	End       time.Time
	QuotaCap  float64
	QuotaUnit string
}

func NewClaudeLocalCollector(cfg config.LocalCollectorConfig, logger *slog.Logger) *LocalUsageCollector {
	return &LocalUsageCollector{source: localClaudeSource, cfg: cfg, logger: logger, now: time.Now}
}

func NewCodexLocalCollector(cfg config.LocalCollectorConfig, logger *slog.Logger) *LocalUsageCollector {
	return &LocalUsageCollector{source: localCodexSource, cfg: cfg, logger: logger, now: time.Now}
}

func (c *LocalUsageCollector) Name() string {
	return c.source
}

func (c *LocalUsageCollector) SetStore(dataStore store.Store) {
	if usageStore, ok := dataStore.(store.LocalUsageStateStore); ok {
		c.store = usageStore
	}
}

func (c *LocalUsageCollector) SetCodexOnlineQuotaClient(client *CodexOnlineQuotaClient) {
	c.onlineCodexQuota = client
}

func (c *LocalUsageCollector) SetClaudeOnlineQuotaClient(client *ClaudeOnlineQuotaClient) {
	c.onlineClaudeQuota = client
}

// SetClaudeQuotaFetcher injects an alternative quota source (e.g. the
// store-backed fetcher used in remote mode).
func (c *LocalUsageCollector) SetClaudeQuotaFetcher(fetcher onlineQuotaFetcher) {
	c.onlineClaudeQuota = fetcher
}

// SetCodexQuotaFetcher injects an alternative gap-filling quota source for
// codex (e.g. the store-backed fetcher used in remote mode).
func (c *LocalUsageCollector) SetCodexQuotaFetcher(fetcher onlineQuotaFetcher) {
	c.onlineCodexQuota = fetcher
}

func (c *LocalUsageCollector) Collect(ctx context.Context) ([]model.DataItem, error) {
	events, err := c.collectEvents(ctx)
	if err != nil {
		return nil, err
	}

	items := c.buildItems(ctx, events)
	return withFetchedAt(items), nil
}

func (c *LocalUsageCollector) collectEvents(ctx context.Context) ([]localUsageEvent, error) {
	switch strings.ToLower(strings.TrimSpace(c.cfg.Mode)) {
	case "", "builtin":
		return c.scanBuiltin(ctx)
	case "ccusage":
		if c.source != localClaudeSource {
			return c.scanBuiltin(ctx)
		}
		events, err := c.scanCCUsage(ctx)
		if err == nil {
			return events, nil
		}
		if c.logger != nil {
			c.logger.Warn("ccusage local collector failed; fallback to builtin", "source", c.source, "error", err)
		}
		return c.scanBuiltin(ctx)
	case "remote":
		// No filesystem scan: aggregate records pushed by infohub-agent.
		if c.store == nil {
			return nil, fmt.Errorf("%s remote mode requires a sqlite store", c.source)
		}
		return c.readStoredEvents()
	default:
		return nil, fmt.Errorf("unsupported %s mode %q", c.source, c.cfg.Mode)
	}
}

func (c *LocalUsageCollector) scanner() *localscan.Scanner {
	return &localscan.Scanner{
		Source: c.source,
		Paths:  c.cfg.Paths,
		Logger: c.logger,
		Now:    c.now,
	}
}

func (c *LocalUsageCollector) scanBuiltin(ctx context.Context) ([]localUsageEvent, error) {
	if c.store != nil {
		return c.scanBuiltinIncremental(ctx)
	}
	return c.scanner().ScanFull(ctx)
}

func (c *LocalUsageCollector) scanBuiltinIncremental(ctx context.Context) ([]localUsageEvent, error) {
	states, err := c.store.LoadLocalParseStates(c.source)
	if err != nil {
		return nil, err
	}

	nextStates, records, err := c.scanner().ScanIncremental(ctx, states)
	if err != nil {
		return nil, err
	}

	if len(nextStates) > 0 || len(records) > 0 {
		if err := c.store.SaveLocalUsageBatch(c.source, nextStates, records); err != nil {
			return nil, err
		}
	}

	return c.readStoredEvents()
}

func (c *LocalUsageCollector) readStoredEvents() ([]localUsageEvent, error) {
	start, end := c.recordQueryRange()
	storedRecords, err := c.store.ListLocalUsageRecords(c.source, start, end)
	if err != nil {
		return nil, err
	}
	events := make([]localUsageEvent, 0, len(storedRecords))
	for _, record := range storedRecords {
		events = append(events, localscan.EventFromRecord(record))
	}
	return events, nil
}

func (c *LocalUsageCollector) recordQueryRange() (time.Time, time.Time) {
	windows := c.windows(c.now())
	if len(windows) == 0 {
		now := c.now()
		return now, now
	}
	start := windows[0].Start
	end := windows[0].End
	for _, window := range windows[1:] {
		if window.Start.Before(start) {
			start = window.Start
		}
		if window.End.After(end) {
			end = window.End
		}
	}
	return start, end
}

func (c *LocalUsageCollector) scanCCUsage(ctx context.Context) ([]localUsageEvent, error) {
	bin := strings.TrimSpace(c.cfg.CCUsageBin)
	if bin == "" {
		bin = "npx"
	}
	args := c.cfg.CCUsageArgs
	if len(args) == 0 {
		args = []string{"ccusage@latest", "--json"}
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("run ccusage: %w", err)
	}
	return localscan.ParseCCUsageEvents(output, c.now())
}

func (c *LocalUsageCollector) buildItems(ctx context.Context, events []localUsageEvent) []model.DataItem {
	now := c.now()
	windows := c.windows(now)
	buckets := make(map[string]*localUsageBucket, len(windows))
	for _, window := range windows {
		buckets[window.Key] = &localUsageBucket{Models: map[string]float64{}}
	}

	var latestQuota localRateLimits
	var latestQuotaAt time.Time
	for _, event := range events {
		if event.Quota.HasAny() && (latestQuotaAt.IsZero() || event.At.After(latestQuotaAt)) {
			latestQuota = event.Quota
			latestQuotaAt = event.At
		}
		for _, window := range windows {
			if event.At.Before(window.Start) || !event.At.Before(window.End) {
				continue
			}
			buckets[window.Key].add(event)
		}
	}
	latestQuota = discardExpiredRateLimits(latestQuota, now)

	quotaSourceForWindow := map[string]string{}
	if latestQuota.FiveHour.OK {
		quotaSourceForWindow["5H"] = "codex_rate_limits"
	}
	if latestQuota.Week.OK {
		quotaSourceForWindow["Week"] = "codex_rate_limits"
	}
	if c.source == localClaudeSource {
		if c.onlineClaudeQuota != nil {
			onlineLimits, ok, err := c.onlineClaudeQuota.FetchRateLimits(ctx)
			if err != nil && c.logger != nil {
				c.logger.Warn("claude online quota unavailable", "error", err)
			}
			if ok {
				onlineLimits = discardExpiredRateLimits(onlineLimits, now)
				if onlineLimits.FiveHour.OK {
					latestQuota.FiveHour = onlineLimits.FiveHour
					quotaSourceForWindow["5H"] = "claude_oauth_usage"
				}
				if onlineLimits.Week.OK {
					latestQuota.Week = onlineLimits.Week
					quotaSourceForWindow["Week"] = "claude_oauth_usage"
				}
			}
		}
	}
	onlineQuotaStatus := ""
	if c.source == localCodexSource {
		onlineQuotaStatus = codexOnlineQuotaStatusDisabled
		if c.onlineCodexQuota != nil {
			onlineQuotaStatus = codexOnlineQuotaStatusOK
			if !latestQuota.FiveHour.OK || !latestQuota.Week.OK {
				onlineLimits, ok, err := c.onlineCodexQuota.FetchRateLimits(ctx)
				onlineQuotaStatus = normalizeCodexOnlineQuotaStatus(c.onlineCodexQuota.LastStatus())
				if err != nil {
					onlineQuotaStatus = codexOnlineQuotaStatusTransportError
					if c.logger != nil {
						c.logger.Warn("codex online quota fallback failed", "error", err)
					}
				}
				if ok {
					onlineQuotaStatus = codexOnlineQuotaStatusOK
					onlineLimits = discardExpiredRateLimits(onlineLimits, now)
					if !latestQuota.FiveHour.OK && onlineLimits.FiveHour.OK {
						latestQuota.FiveHour = onlineLimits.FiveHour
						quotaSourceForWindow["5H"] = "codex_wham_usage"
					}
					if !latestQuota.Week.OK && onlineLimits.Week.OK {
						latestQuota.Week = onlineLimits.Week
						quotaSourceForWindow["Week"] = "codex_wham_usage"
					}
				}
			}
		}
	}

	items := make([]model.DataItem, 0, 8)
	today := buckets["today"]
	if today == nil {
		today = &localUsageBucket{Models: map[string]float64{}}
	}
	items = append(items, model.DataItem{
		Source:   c.source,
		Category: "token_usage",
		Title:    "今日 Token 用量",
		Value:    formatFloat(today.TotalTokens()),
		Extra: map[string]any{
			"daily_requests":        today.Messages,
			"daily_cost":            0,
			"enabled_accounts":      1,
			"enabled_account_names": []string{c.displayName()},
			"input":                 today.Input,
			"output":                today.Output,
			"cache_read":            today.CacheRead,
			"cache_creation":        today.CacheCreation,
			"reasoning":             today.Reasoning,
		},
	})

	for _, window := range windows {
		bucket := buckets[window.Key]
		if bucket == nil {
			continue
		}
		if window.Label == "5H" || window.Label == "Week" {
			items = append(items, c.quotaItem(window, bucket, latestQuota.ForWindow(window.Label), quotaSourceForWindow[window.Label], onlineQuotaStatus))
		}
		if window.Key == "today" || window.Key == "month" || window.Key == "weekly" {
			items = append(items, c.windowUsageItem(window, bucket))
		}
	}

	if modelName, tokens, ok := topModel(today.Models); ok {
		items = append(items, model.DataItem{
			Source:   c.source,
			Category: "usage",
			Title:    "model_top1",
			Value:    modelName,
			Extra: map[string]any{
				"tokens":        tokens,
				"share_percent": percentOf(tokens, today.TotalTokens()),
			},
		})
	}

	if c.source == localClaudeSource {
		items = append(items, model.DataItem{
			Source:   c.source,
			Category: "usage",
			Title:    "cache_hit",
			Value:    formatPercent(percentOf(today.CacheRead, today.Input+today.CacheRead)),
			Extra: map[string]any{
				"cache_read":  today.CacheRead,
				"total_input": today.Input + today.CacheRead,
			},
		})
	} else {
		items = append(items, model.DataItem{
			Source:   c.source,
			Category: "usage",
			Title:    "reasoning_share",
			Value:    formatPercent(percentOf(today.Reasoning, today.Output)),
			Extra: map[string]any{
				"reasoning": today.Reasoning,
				"output":    today.Output,
			},
		})
	}

	return items
}

func (c *LocalUsageCollector) quotaItem(window localWindow, bucket *localUsageBucket, observed localQuotaObservation, observedSource string, onlineQuotaStatus string) model.DataItem {
	used := bucket.Messages
	if window.QuotaUnit == "tokens" {
		used = bucket.TotalTokens()
	}
	usedPercent := 0.0
	if window.QuotaCap > 0 {
		usedPercent = percentOf(used, window.QuotaCap)
	}
	resetAt := window.End.Format(time.RFC3339)
	quotaSource := "estimated_cap"
	if observed.OK {
		usedPercent = observed.UsedPercent
		quotaSource = strings.TrimSpace(observedSource)
		if quotaSource == "" {
			if c.source == localClaudeSource {
				quotaSource = "claude_oauth_usage"
			} else {
				quotaSource = "codex_rate_limits"
			}
		}
		if strings.TrimSpace(observed.ResetAt) != "" {
			resetAt = observed.ResetAt
		}
	}
	extra := map[string]any{
		"account_id":        c.source,
		"used_percent":      usedPercent,
		"remaining_percent": remainingPercent(usedPercent),
		"window":            window.Label,
		"used":              used,
		"cap":               window.QuotaCap,
		"quota_unit":        window.QuotaUnit,
		"window_start_at":   window.Start.Format(time.RFC3339),
		"window_end_at":     window.End.Format(time.RFC3339),
		"reset_at":          resetAt,
		"models":            bucket.Models,
		"approx":            !observed.OK,
		"quota_source":      quotaSource,
	}
	if c.source == localCodexSource {
		extra["online_quota_status"] = normalizeCodexOnlineQuotaStatus(onlineQuotaStatus)
	}
	if strings.TrimSpace(c.cfg.Quota.Plan) != "" {
		extra["plan"] = strings.TrimSpace(c.cfg.Quota.Plan)
	}

	return model.DataItem{
		Source:   c.source,
		Category: "quota",
		Title:    fmt.Sprintf("账号 %s %s 额度", c.displayName(), window.Label),
		Value:    formatPercent(remainingPercent(usedPercent)),
		Extra:    extra,
	}
}

func (c *LocalUsageCollector) windowUsageItem(window localWindow, bucket *localUsageBucket) model.DataItem {
	return model.DataItem{
		Source:   c.source,
		Category: "quota",
		Title:    window.Title,
		Value:    formatFloat(bucket.TotalTokens()) + " tokens",
		Extra: map[string]any{
			"window":         window.Key,
			"input":          bucket.Input,
			"output":         bucket.Output,
			"cache_read":     bucket.CacheRead,
			"cache_creation": bucket.CacheCreation,
			"reasoning":      bucket.Reasoning,
			"messages":       bucket.Messages,
			"models":         bucket.Models,
		},
	}
}

func (c *LocalUsageCollector) windows(now time.Time) []localWindow {
	localNow := now.In(time.Local)
	todayStart := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, time.Local)

	if c.source == localCodexSource {
		weekday := int(localNow.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		weekStart := todayStart.AddDate(0, 0, -(weekday - 1))
		weeklyCap := c.cfg.Quota.WeeklyCap
		quotaUnit := "messages"
		if weeklyCap <= 0 && c.cfg.Quota.WeeklyTokenCap > 0 {
			weeklyCap = c.cfg.Quota.WeeklyTokenCap
			quotaUnit = "tokens"
		}
		return []localWindow{
			{Key: "5h", Label: "5H", Title: "5H 用量", Start: now.Add(-5 * time.Hour), End: now, QuotaCap: c.cfg.Quota.FiveHourCap, QuotaUnit: "messages"},
			{Key: "today", Label: "Today", Title: "今日用量", Start: todayStart, End: todayStart.AddDate(0, 0, 1), QuotaUnit: "tokens"},
			{Key: "weekly", Label: "Week", Title: "本周用量", Start: weekStart, End: weekStart.AddDate(0, 0, 7), QuotaCap: weeklyCap, QuotaUnit: quotaUnit},
		}
	}

	weeklyCap := c.cfg.Quota.WeeklyCap
	if weeklyCap <= 0 {
		weeklyCap = c.cfg.Quota.MonthlyCap
	}
	return []localWindow{
		{Key: "5h", Label: "5H", Title: "5H 用量", Start: now.Add(-5 * time.Hour), End: now, QuotaCap: c.cfg.Quota.FiveHourCap, QuotaUnit: "messages"},
		{Key: "today", Label: "Today", Title: "今日用量", Start: todayStart, End: todayStart.AddDate(0, 0, 1), QuotaUnit: "tokens"},
		{Key: "weekly", Label: "Week", Title: "7D 用量", Start: now.AddDate(0, 0, -7), End: now, QuotaCap: weeklyCap, QuotaUnit: "messages"},
	}
}

func (c *LocalUsageCollector) displayName() string {
	if c.source == localCodexSource {
		return "Codex Local"
	}
	return "Claude Local"
}

func (b *localUsageBucket) add(event localUsageEvent) {
	b.Tokens += event.TotalTokens()
	b.Input += event.Input
	b.Output += event.Output
	b.CacheRead += event.CacheRead
	b.CacheCreation += event.CacheCreation
	b.Reasoning += event.Reasoning
	b.Messages++
	modelName := strings.TrimSpace(event.Model)
	if modelName == "" {
		modelName = "unknown"
	}
	b.Models[modelName] += event.TotalTokens()
}

func (b localUsageBucket) TotalTokens() float64 {
	return b.Tokens
}

func discardExpiredRateLimits(limits localRateLimits, now time.Time) localRateLimits {
	return localscan.DiscardExpired(limits, now)
}

func normalizeCodexOnlineQuotaStatus(status string) string {
	switch strings.TrimSpace(status) {
	case codexOnlineQuotaStatusTokenMissing,
		codexOnlineQuotaStatusUnauthorized,
		codexOnlineQuotaStatusRateLimited,
		codexOnlineQuotaStatusEndpoint404,
		codexOnlineQuotaStatusTransportError,
		codexOnlineQuotaStatusOK:
		return strings.TrimSpace(status)
	default:
		return codexOnlineQuotaStatusDisabled
	}
}

func extractCodexRateLimit(rateLimits map[string]any, key string) localQuotaObservation {
	return localscan.ExtractCodexRateLimit(rateLimits, key)
}

func parseCCUsageEvents(payload []byte, fallbackTime time.Time) ([]localUsageEvent, error) {
	return localscan.ParseCCUsageEvents(payload, fallbackTime)
}

func firstNestedMap(record map[string]any, paths ...string) (map[string]any, bool) {
	return localscan.FirstNestedMap(record, paths...)
}

func nestedMap(record map[string]any, path string) (map[string]any, bool) {
	return localscan.NestedMap(record, path)
}

func firstNestedString(record map[string]any, paths ...string) string {
	return localscan.FirstNestedString(record, paths...)
}

func firstTime(record map[string]any, paths ...string) (time.Time, bool) {
	return localscan.FirstTime(record, paths...)
}

func parseEventTime(value any) (time.Time, bool) {
	return localscan.ParseEventTime(value)
}

func topModel(models map[string]float64) (string, float64, bool) {
	if len(models) == 0 {
		return "", 0, false
	}
	names := make([]string, 0, len(models))
	for name := range models {
		names = append(names, name)
	}
	sort.Strings(names)

	topName := names[0]
	topTokens := models[topName]
	for _, name := range names[1:] {
		if models[name] > topTokens {
			topName = name
			topTokens = models[name]
		}
	}
	return topName, topTokens, true
}

func percentOf(value, total float64) float64 {
	if total <= 0 {
		return 0
	}
	return value / total * 100
}
