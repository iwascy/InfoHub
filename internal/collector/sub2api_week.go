package collector

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"

	"infohub/internal/model"
)

func (c *Sub2APICollector) fetchCodexWeekUsage(ctx context.Context, session *serviceSession, scope, id, start, end string) model.DataItem {
	item := model.DataItem{
		Source: c.Name(), Category: "token_usage_product_week", Title: "Codex 本周 Token 用量", Value: "--",
		Extra: map[string]any{"product": "codex", scope: id, "start_date": start, "end_date": end, "timezone": "Asia/Shanghai"},
	}
	fail := func(err error) model.DataItem {
		item.Extra["error"] = err.Error()
		if c.service.logger != nil {
			c.service.logger.Warn("sub2api weekly usage unavailable", "scope", scope, "id", id, "error", err)
		}
		return item
	}
	if !c.service.hasEndpoint("usage_stats") {
		return fail(fmt.Errorf("usage_stats endpoint is not configured"))
	}
	// Explicit dates take precedence over upstream's rolling period=week.
	query := url.Values{scope: {id}, "period": {"week"}, "timezone": {"Asia/Shanghai"}, "start_date": {start}, "end_date": {end}}
	payload, err := session.fetchJSON(ctx, http.MethodGet, "usage_stats", query, nil)
	if err != nil {
		return fail(err)
	}
	var tokens float64
	if scope == "user_id" {
		endpoints, ok := nestedValue(payload, "data.upstream_endpoints")
		if !ok {
			endpoints, ok = nestedValue(payload, "upstream_endpoints")
		}
		list, valid := endpoints.([]any)
		if !ok || !valid {
			return fail(fmt.Errorf("weekly usage missing upstream_endpoints"))
		}
		for _, value := range list {
			endpoint, valid := value.(map[string]any)
			if !valid {
				return fail(fmt.Errorf("invalid weekly endpoint entry"))
			}
			if sub2apiProductForUpstream(firstString(endpoint, "endpoint")) != "codex" {
				continue
			}
			count, found := nestedFloat(endpoint, "total_tokens")
			if !found {
				count, found = nestedFloat(endpoint, "tokens")
			}
			if !found || count < 0 || math.IsNaN(count) || math.IsInf(count, 0) {
				return fail(fmt.Errorf("invalid weekly endpoint token count"))
			}
			tokens += count
		}
	} else {
		var ok bool
		tokens, ok = nestedFloat(payload, "data.total_tokens")
		if !ok {
			tokens, ok = nestedFloat(payload, "total_tokens")
		}
		if !ok {
			return fail(fmt.Errorf("weekly usage missing total_tokens"))
		}
	}
	if tokens < 0 || math.IsNaN(tokens) || math.IsInf(tokens, 0) {
		return fail(fmt.Errorf("invalid weekly token count"))
	}
	item.Value = formatFloat(tokens)
	return item
}
