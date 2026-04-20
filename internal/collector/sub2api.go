package collector

import (
	"context"
	"fmt"
	"log/slog"

	"infohub/internal/config"
	"infohub/internal/model"
)

type Sub2APICollector struct {
	http httpJSONCollector
}

func NewSub2APICollector(cfg config.HTTPCollectorConfig, logger *slog.Logger) *Sub2APICollector {
	return &Sub2APICollector{
		http: httpJSONCollector{
			name:     "sub2api",
			client:   newHTTPClient(cfg.Timeout()),
			logger:   logger,
			baseURL:  cfg.BaseURL,
			endpoint: cfg.Endpoint,
			headers:  buildHeaders(cfg.APIKey, cfg.Headers),
		},
	}
}

func (c *Sub2APICollector) Name() string {
	return c.http.name
}

func (c *Sub2APICollector) Collect(ctx context.Context) ([]model.DataItem, error) {
	payload, err := c.http.fetch(ctx)
	if err != nil {
		return nil, err
	}
	if items, ok, err := parseEnvelopeItems(c.Name(), payload, "quota"); ok {
		return withFetchedAt(items), err
	}

	items := make([]model.DataItem, 0, 3)
	if balance, ok := findValue(payload, "balance", "credit_balance", "remaining_balance"); ok {
		items = append(items, model.DataItem{
			Source:    c.Name(),
			Category:  "balance",
			Title:     "账户余额",
			Value:     stringify(balance),
			FetchedAt: 0,
		})
	}
	if quota, ok := findValue(payload, "quota_remaining", "remaining_quota", "quota"); ok {
		items = append(items, model.DataItem{
			Source:    c.Name(),
			Category:  "quota",
			Title:     "剩余配额",
			Value:     stringify(quota),
			FetchedAt: 0,
		})
	}
	if requests, ok := findValue(payload, "requests_today", "today_requests", "request_count"); ok {
		items = append(items, model.DataItem{
			Source:    c.Name(),
			Category:  "request_usage",
			Title:     "今日请求数",
			Value:     stringify(requests),
			FetchedAt: 0,
		})
	}

	if len(items) == 0 {
		return nil, fmt.Errorf("cannot parse sub2api metrics from upstream payload")
	}

	return withFetchedAt(items), nil
}
