package collector

import (
	"context"
	"fmt"
	"log/slog"

	"infohub/internal/config"
	"infohub/internal/model"
)

type ClaudeRelayCollector struct {
	http httpJSONCollector
}

func NewClaudeRelayCollector(cfg config.HTTPCollectorConfig, logger *slog.Logger) *ClaudeRelayCollector {
	return &ClaudeRelayCollector{
		http: httpJSONCollector{
			name:     "claude_relay",
			client:   newHTTPClient(cfg.Timeout()),
			logger:   logger,
			baseURL:  cfg.BaseURL,
			endpoint: cfg.Endpoint,
			headers:  buildHeaders(cfg.APIKey, cfg.Headers),
		},
	}
}

func (c *ClaudeRelayCollector) Name() string {
	return c.http.name
}

func (c *ClaudeRelayCollector) Collect(ctx context.Context) ([]model.DataItem, error) {
	payload, err := c.http.fetch(ctx)
	if err != nil {
		return nil, err
	}
	if items, ok, err := parseEnvelopeItems(c.Name(), payload, "token_usage"); ok {
		return withFetchedAt(items), err
	}

	items := make([]model.DataItem, 0, 2)
	extra := map[string]any{}

	if modelName, ok := findValue(payload, "model", "active_model"); ok {
		extra["model"] = modelName
	}
	if limit, ok := findValue(payload, "limit", "token_limit", "quota_limit"); ok {
		extra["limit"] = limit
	}

	if usage, ok := findValue(payload, "today_tokens", "daily_tokens", "token_usage", "usage_tokens", "used_tokens"); ok {
		items = append(items, model.DataItem{
			Source:    c.Name(),
			Category:  "token_usage",
			Title:     "今日 Token 用量",
			Value:     stringify(usage),
			Extra:     extra,
			FetchedAt: 0,
		})
	}
	if quota, ok := findValue(payload, "quota_remaining", "remaining_quota", "remaining_tokens", "quota"); ok {
		items = append(items, model.DataItem{
			Source:    c.Name(),
			Category:  "quota",
			Title:     "剩余额度",
			Value:     stringify(quota),
			FetchedAt: 0,
		})
	}

	if len(items) == 0 {
		return nil, fmt.Errorf("cannot parse claude relay metrics from upstream payload")
	}

	return withFetchedAt(items), nil
}
