package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"infohub/internal/model"
)

type httpJSONCollector struct {
	name     string
	client   *http.Client
	logger   *slog.Logger
	baseURL  string
	endpoint string
	headers  map[string]string
}

func (c *httpJSONCollector) fetch(ctx context.Context) (any, error) {
	if strings.TrimSpace(c.baseURL) == "" {
		return nil, fmt.Errorf("%s base_url is empty", c.name)
	}
	if strings.TrimSpace(c.endpoint) == "" {
		return nil, fmt.Errorf("%s endpoint is empty", c.name)
	}

	targetURL, err := joinURL(c.baseURL, c.endpoint)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	for key, value := range c.headers {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			continue
		}
		req.Header.Set(key, value)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request upstream: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("read upstream response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("upstream status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode upstream response: %w", err)
	}

	return payload, nil
}

func newHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
}

func buildHeaders(apiKey string, extra map[string]string) map[string]string {
	headers := map[string]string{}
	for key, value := range extra {
		headers[key] = value
	}
	if strings.TrimSpace(apiKey) != "" {
		headers["Authorization"] = "Bearer " + strings.TrimSpace(apiKey)
	}
	return headers
}

func joinURL(baseURL, endpoint string) (string, error) {
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "", fmt.Errorf("parse base url: %w", err)
	}
	relative, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return "", fmt.Errorf("parse endpoint: %w", err)
	}
	return base.ResolveReference(relative).String(), nil
}

func parseEnvelopeItems(source string, payload any, defaultCategory string) ([]model.DataItem, bool, error) {
	switch typed := payload.(type) {
	case []any:
		items, err := decodeItems(source, typed, defaultCategory)
		return items, true, err
	case map[string]any:
		for _, key := range []string{"items", "data", "result"} {
			raw, ok := typed[key]
			if !ok {
				continue
			}
			if key == "data" || key == "result" {
				if nested, ok := raw.(map[string]any); ok {
					for _, nestedKey := range []string{"items", "list", "records"} {
						if itemsRaw, ok := nested[nestedKey]; ok {
							items, err := decodeRawItems(source, itemsRaw, defaultCategory)
							return items, true, err
						}
					}
				}
			}

			items, err := decodeRawItems(source, raw, defaultCategory)
			return items, true, err
		}
	}

	return nil, false, nil
}

func decodeRawItems(source string, raw any, defaultCategory string) ([]model.DataItem, error) {
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("items is not an array")
	}
	return decodeItems(source, items, defaultCategory)
}

func decodeItems(source string, rawItems []any, defaultCategory string) ([]model.DataItem, error) {
	items := make([]model.DataItem, 0, len(rawItems))
	for _, raw := range rawItems {
		record, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("item is not an object")
		}

		item := model.DataItem{
			Source:    source,
			Category:  firstString(record, "category"),
			Title:     firstString(record, "title", "name"),
			Value:     firstValueString(record, "value", "amount", "status"),
			FetchedAt: firstInt64(record, "fetched_at", "fetchedAt"),
		}
		if item.Category == "" {
			item.Category = defaultCategory
		}
		if item.Title == "" {
			item.Title = item.Category
		}
		if item.Value == "" {
			item.Value = "-"
		}
		if item.FetchedAt == 0 {
			item.FetchedAt = time.Now().Unix()
		}
		if extra, ok := record["extra"].(map[string]any); ok && len(extra) > 0 {
			item.Extra = extra
		}

		items = append(items, item)
	}

	return items, nil
}

func findValue(payload any, keys ...string) (any, bool) {
	expected := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		expected[normalizeKey(key)] = struct{}{}
	}
	return searchValue(payload, expected)
}

func searchValue(payload any, expected map[string]struct{}) (any, bool) {
	switch typed := payload.(type) {
	case map[string]any:
		for key, value := range typed {
			if _, ok := expected[normalizeKey(key)]; ok {
				return value, true
			}
			if nested, ok := searchValue(value, expected); ok {
				return nested, true
			}
		}
	case []any:
		for _, value := range typed {
			if nested, ok := searchValue(value, expected); ok {
				return nested, true
			}
		}
	}
	return nil, false
}

func normalizeKey(key string) string {
	replacer := strings.NewReplacer("-", "_", ".", "_", " ", "_")
	return strings.ToLower(replacer.Replace(strings.TrimSpace(key)))
}

func stringify(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case json.Number:
		return typed.String()
	case float64:
		if typed == float64(int64(typed)) {
			return strconv.FormatInt(int64(typed), 10)
		}
		return strconv.FormatFloat(typed, 'f', 2, 64)
	case float32:
		if typed == float32(int64(typed)) {
			return strconv.FormatInt(int64(typed), 10)
		}
		return strconv.FormatFloat(float64(typed), 'f', 2, 32)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case int32:
		return strconv.FormatInt(int64(typed), 10)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	default:
		raw, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprintf("%v", typed)
		}
		return string(raw)
	}
}

func firstString(record map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := record[key]; ok {
			if text := stringify(value); text != "" {
				return text
			}
		}
	}
	return ""
}

func firstValueString(record map[string]any, keys ...string) string {
	return firstString(record, keys...)
}

func firstInt64(record map[string]any, keys ...string) int64 {
	for _, key := range keys {
		value, ok := record[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case int64:
			return typed
		case int:
			return int64(typed)
		case float64:
			return int64(typed)
		case string:
			parsed, err := strconv.ParseInt(typed, 10, 64)
			if err == nil {
				return parsed
			}
		}
	}
	return 0
}

func withFetchedAt(items []model.DataItem) []model.DataItem {
	now := time.Now().Unix()
	for index := range items {
		if items[index].FetchedAt == 0 {
			items[index].FetchedAt = now
		}
		if items[index].Source == "" {
			items[index].Source = items[index].Source
		}
	}
	return items
}
