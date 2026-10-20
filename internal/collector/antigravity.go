package collector

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"infohub/internal/config"
	"infohub/internal/model"
	"infohub/internal/store"
)

// AntigravitySourcePrefix namespaces agent-reported Antigravity quota groups
// in agent_quota_observations ("antigravity:<group display name>").
const AntigravitySourcePrefix = "antigravity:"

// AntigravityCollector turns the latest agent-reported Antigravity quota
// groups into quota items; the server holds no Google credentials itself.
type AntigravityCollector struct {
	cfg   config.AntigravityConfig
	store store.AgentQuotaGroupStore
	now   func() time.Time
}

func NewAntigravityCollector(cfg config.AntigravityConfig, groupStore store.AgentQuotaGroupStore) *AntigravityCollector {
	return &AntigravityCollector{cfg: cfg, store: groupStore, now: time.Now}
}

func (c *AntigravityCollector) Name() string {
	return "antigravity"
}

func (c *AntigravityCollector) Collect(ctx context.Context) ([]model.DataItem, error) {
	if c.store == nil {
		return nil, fmt.Errorf("antigravity collector requires the sqlite store")
	}
	observations, err := c.store.ListAgentQuotaObservations(AntigravitySourcePrefix)
	if err != nil {
		return nil, err
	}
	now := c.now()
	// Several machines may report the same account; keep the freshest group.
	latest := map[string]store.AgentQuotaObservation{}
	for _, obs := range observations {
		if c.cfg.Stale() > 0 && now.Sub(obs.ObservedAt) > c.cfg.Stale() {
			continue
		}
		group := strings.TrimPrefix(obs.Source, AntigravitySourcePrefix)
		if current, ok := latest[group]; !ok || obs.ObservedAt.After(current.ObservedAt) {
			latest[group] = obs
		}
	}
	groups := make([]string, 0, len(latest))
	for group := range latest {
		groups = append(groups, group)
	}
	// Upstream lists the main "Gemini Models" pool first; keep it on top.
	sort.Slice(groups, func(i, j int) bool {
		if (groups[i] == "Gemini Models") != (groups[j] == "Gemini Models") {
			return groups[i] == "Gemini Models"
		}
		return groups[i] < groups[j]
	})

	var items []model.DataItem
	if usageStore, ok := c.store.(store.LocalUsageStateStore); ok {
		usageItems, err := c.usageItems(usageStore, now)
		if err != nil {
			return nil, err
		}
		items = append(items, usageItems...)
	}
	for _, group := range groups {
		obs := latest[group]
		for _, window := range []struct {
			name  string
			used  float64
			reset string
		}{
			{"5H", obs.Quota5hUsed, obs.Quota5hReset},
			{"Week", obs.QuotaWeekUsed, obs.QuotaWeekReset},
		} {
			if window.used < 0 {
				continue
			}
			if resetPassed(window.reset, now) {
				// The window refreshed since the agent looked; keep the row
				// instead of hiding it, the next report brings the new reset.
				window.used, window.reset = 0, ""
			}
			item := sub2apiQuotaItem(c.Name(), AntigravitySourcePrefix+group, antigravityGroupLabel(group), window.name, window.used, window.reset)
			item.Extra["product"] = "antigravity"
			item.Extra["group"] = group
			item.Extra["machine"] = obs.Machine
			item.Extra["quota_source"] = "agent_quota_summary"
			item.Extra["observed_at"] = obs.ObservedAt.Unix()
			items = append(items, item)
		}
	}
	return items, nil
}

// antigravityGroupLabel shortens upstream group names for the narrow panel
// column ("Gemini Models" -> "Gemini").
func antigravityGroupLabel(group string) string {
	if group == "Gemini Models" {
		return "Gemini"
	}
	return group
}

func resetPassed(resetAt string, now time.Time) bool {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(resetAt))
	return err == nil && !parsed.After(now)
}

func (c *AntigravityCollector) usageItems(usageStore store.LocalUsageStateStore, now time.Time) ([]model.DataItem, error) {
	local := now.In(model.ShanghaiLocation)
	today, weekStart := model.UsageDates(local)
	dayStart, _ := time.ParseInLocation("2006-01-02", today, model.ShanghaiLocation)
	weekBegin, _ := time.ParseInLocation("2006-01-02", weekStart, model.ShanghaiLocation)
	records, err := usageStore.ListLocalUsageRecords(c.Name(), weekBegin, dayStart.AddDate(0, 0, 1))
	if err != nil || len(records) == 0 {
		return nil, err
	}
	var dayTokens, weekTokens float64
	for _, record := range records {
		weekTokens += record.Total
		if !record.At.Before(dayStart) {
			dayTokens += record.Total
		}
	}
	return []model.DataItem{
		{
			Source: c.Name(), Category: "token_usage_product", Title: "Antigravity 今日 Token 用量", Value: formatFloat(dayTokens),
			Extra: map[string]any{"scope": "product", "product": "antigravity", "start_date": today, "end_date": today, "timezone": "Asia/Shanghai"},
		},
		{
			Source: c.Name(), Category: "token_usage_product_week", Title: "Antigravity 本周 Token 用量", Value: formatFloat(weekTokens),
			Extra: map[string]any{"product": "antigravity", "start_date": weekStart, "end_date": today, "timezone": "Asia/Shanghai"},
		},
	}, nil
}
