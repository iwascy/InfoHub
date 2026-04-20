package collector

import (
	"context"
	"fmt"
	"log/slog"

	"infohub/internal/config"
	"infohub/internal/model"
)

type FeishuCollector struct {
	http       httpJSONCollector
	projectKey string
	appID      string
	appSecret  string
}

func NewFeishuCollector(cfg config.FeishuCollectorConfig, logger *slog.Logger) *FeishuCollector {
	return &FeishuCollector{
		http: httpJSONCollector{
			name:     "feishu",
			client:   newHTTPClient(cfg.Timeout()),
			logger:   logger,
			baseURL:  cfg.BaseURL,
			endpoint: cfg.Endpoint,
			headers:  cfg.Headers,
		},
		projectKey: cfg.ProjectKey,
		appID:      cfg.AppID,
		appSecret:  cfg.AppSecret,
	}
}

func (c *FeishuCollector) Name() string {
	return c.http.name
}

func (c *FeishuCollector) Collect(ctx context.Context) ([]model.DataItem, error) {
	if c.http.endpoint == "" {
		return nil, fmt.Errorf("feishu endpoint is empty; configure collectors.feishu.endpoint or add a proxy endpoint")
	}

	payload, err := c.http.fetch(ctx)
	if err != nil {
		return nil, err
	}
	if items, ok, err := parseEnvelopeItems(c.Name(), payload, "tasks"); ok {
		return withFetchedAt(items), err
	}

	rawTasks, ok := findValue(payload, "tasks", "work_items", "issues", "records", "list")
	if !ok {
		if total, ok := findValue(payload, "count", "total", "active_tasks"); ok {
			return withFetchedAt([]model.DataItem{{
				Source:    c.Name(),
				Category:  "tasks",
				Title:     "活跃任务数",
				Value:     stringify(total),
				Extra:     c.extra(),
				FetchedAt: 0,
			}}), nil
		}
		return nil, fmt.Errorf("cannot parse feishu task payload")
	}

	taskList, ok := rawTasks.([]any)
	if !ok {
		return nil, fmt.Errorf("feishu task list is not an array")
	}

	items := make([]model.DataItem, 0, len(taskList)+1)
	items = append(items, model.DataItem{
		Source:    c.Name(),
		Category:  "tasks",
		Title:     "活跃任务数",
		Value:     stringify(len(taskList)),
		Extra:     c.extra(),
		FetchedAt: 0,
	})

	for index, raw := range taskList {
		record, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		title := firstString(record, "title", "name", "summary")
		if title == "" {
			title = fmt.Sprintf("任务 %d", index+1)
		}
		value := firstString(record, "status", "state", "assignee")
		if value == "" {
			value = "-"
		}

		items = append(items, model.DataItem{
			Source:    c.Name(),
			Category:  "tasks",
			Title:     title,
			Value:     value,
			Extra:     c.extra(),
			FetchedAt: 0,
		})
	}

	return withFetchedAt(items), nil
}

func (c *FeishuCollector) extra() map[string]any {
	extra := map[string]any{}
	if c.projectKey != "" {
		extra["project_key"] = c.projectKey
	}
	if c.appID != "" {
		extra["app_id"] = c.appID
	}
	if c.appSecret != "" {
		extra["auth_mode"] = "app_credentials"
	}
	if len(extra) == 0 {
		return nil
	}
	return extra
}
