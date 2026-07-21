package localscan

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ExtractEvent parses one decoded JSONL payload for the given source.
func ExtractEvent(source string, payload any) (Event, bool) {
	switch source {
	case SourceClaude:
		return extractClaudeEvent(payload)
	case SourceCodex:
		return extractCodexEvent(payload)
	default:
		return Event{}, false
	}
}

func extractClaudeEvent(payload any) (Event, bool) {
	record, ok := payload.(map[string]any)
	if !ok {
		return Event{}, false
	}

	eventType := strings.TrimSpace(Stringify(record["type"]))
	if eventType != "" && eventType != "assistant" && eventType != "user" {
		return Event{}, false
	}

	usage, ok := NestedMap(record, "message.usage")
	if !ok {
		return Event{}, false
	}

	at, ok := FirstTime(record, "timestamp", "created_at")
	if !ok {
		return Event{}, false
	}

	return Event{
		At:            at,
		Model:         FirstNestedString(record, "message.model", "model"),
		Input:         NumberAt(usage, "input_tokens"),
		Output:        NumberAt(usage, "output_tokens"),
		CacheRead:     NumberAt(usage, "cache_read_input_tokens"),
		CacheCreation: NumberAt(usage, "cache_creation_input_tokens"),
		Total:         FirstNumber(usage, "total_tokens", "totalTokens", "total"),
	}, true
}

func extractCodexEvent(payload any) (Event, bool) {
	record, ok := payload.(map[string]any)
	if !ok {
		return Event{}, false
	}

	if event, ok := extractCodexTokenCountEvent(record); ok {
		return event, true
	}

	usage, ok := FirstNestedMap(record,
		"payload.usage",
		"response.usage",
		"usage",
		"payload.response.usage",
	)
	if !ok {
		return Event{}, false
	}

	at, ok := FirstTime(record, "created_at", "payload.created_at", "response.created_at", "timestamp")
	if !ok {
		return Event{}, false
	}

	input := NumberAt(usage, "input_tokens")
	cacheRead := NumberAt(usage, "cached_input_tokens")
	return Event{
		At:        at,
		Model:     FirstNestedString(record, "payload.model", "response.model", "model", "payload.response.model"),
		Input:     nonCachedInput(input, cacheRead),
		Output:    NumberAt(usage, "output_tokens"),
		CacheRead: cacheRead,
		Reasoning: NumberAt(usage, "reasoning_tokens"),
		Total:     FirstNumber(usage, "total_tokens", "totalTokens", "total"),
	}, true
}

func extractCodexTokenCountEvent(record map[string]any) (Event, bool) {
	eventType := strings.TrimSpace(Stringify(record["type"]))
	payloadType := strings.TrimSpace(FirstNestedString(record, "payload.type"))
	if eventType != "event_msg" || payloadType != "token_count" {
		return Event{}, false
	}

	usage, ok := FirstNestedMap(record,
		"payload.info.last_token_usage",
		"payload.info.total_token_usage",
	)
	rateLimits := extractCodexRateLimits(record)
	if !ok && !rateLimits.HasAny() {
		return Event{}, false
	}

	at, ok := FirstTime(record, "timestamp", "created_at", "payload.created_at")
	if !ok {
		return Event{}, false
	}

	event := Event{
		At:    at,
		Model: FirstNestedString(record, "payload.model", "response.model", "model", "payload.response.model"),
		Quota: rateLimits,
	}
	if usage != nil {
		input := FirstNumber(usage, "input_tokens", "inputTokens", "input")
		cacheRead := FirstNumber(usage, "cached_input_tokens", "cachedInputTokens", "cached_input")
		event.Input = nonCachedInput(input, cacheRead)
		event.Output = FirstNumber(usage, "output_tokens", "outputTokens", "output")
		event.CacheRead = cacheRead
		event.Reasoning = FirstNumber(usage, "reasoning_tokens", "reasoning_output_tokens", "reasoningOutputTokens", "reasoning")
		event.Total = FirstNumber(usage, "total_tokens", "totalTokens", "total")
	}
	return event, event.TotalTokens() > 0 || event.Quota.HasAny()
}

func nonCachedInput(input, cacheRead float64) float64 {
	if cacheRead <= 0 {
		return input
	}
	nonCached := input - cacheRead
	if nonCached < 0 {
		return 0
	}
	return nonCached
}

func extractCodexRateLimits(record map[string]any) RateLimits {
	rateLimits, ok := NestedMap(record, "payload.rate_limits")
	if !ok {
		return RateLimits{}
	}
	return RateLimits{
		FiveHour: ExtractCodexRateLimit(rateLimits, "primary"),
		Week:     ExtractCodexRateLimit(rateLimits, "secondary"),
	}
}

// ExtractCodexRateLimit parses one Codex rate-limit window object (also used
// by the WHAM online quota client, whose payload nests the same shape).
func ExtractCodexRateLimit(rateLimits map[string]any, key string) QuotaObservation {
	limit, ok := NestedMap(rateLimits, key)
	if !ok {
		return QuotaObservation{}
	}
	used, ok := FloatValue(limit["used_percent"])
	if !ok {
		return QuotaObservation{}
	}
	observation := QuotaObservation{
		OK:          true,
		UsedPercent: used,
	}
	var resetVal any
	if v, present := limit["resets_at"]; present && v != nil {
		resetVal = v
	} else if v, present := limit["reset_at"]; present && v != nil {
		resetVal = v
	}
	if reset, ok := ParseEventTime(resetVal); ok {
		observation.ResetAt = reset.Format(time.RFC3339)
	}
	return observation
}

// ParseCCUsageEvents decodes the JSON emitted by the ccusage CLI into events.
func ParseCCUsageEvents(payload []byte, fallbackTime time.Time) ([]Event, error) {
	var decoded any
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode ccusage json: %w", err)
	}

	var events []Event
	visitCCUsageValue(decoded, fallbackTime, &events)
	if len(events) == 0 {
		return nil, fmt.Errorf("ccusage payload contains no usage rows")
	}
	return events, nil
}

func visitCCUsageValue(value any, inheritedAt time.Time, events *[]Event) {
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			visitCCUsageValue(item, inheritedAt, events)
		}
	case map[string]any:
		at := inheritedAt
		if parsed, ok := FirstTime(typed,
			"timestamp",
			"date",
			"day",
			"startTime",
			"start_time",
			"endTime",
			"end_time",
			"from",
			"since",
		); ok {
			at = parsed
		}
		if event, ok := extractCCUsageEvent(typed, at); ok {
			*events = append(*events, event)
			return
		}
		for key, nested := range typed {
			if key == "usage" || key == "tokenCounts" || key == "tokens" {
				continue
			}
			visitCCUsageValue(nested, at, events)
		}
	}
}

func extractCCUsageEvent(record map[string]any, at time.Time) (Event, bool) {
	usage := record
	if nested, ok := FirstNestedMap(record, "usage", "tokenCounts", "tokens"); ok {
		usage = nested
	}
	event := Event{
		At:            at,
		Model:         FirstNestedString(record, "model", "modelName", "model_name"),
		Input:         FirstNumber(usage, "input_tokens", "inputTokens", "input"),
		Output:        FirstNumber(usage, "output_tokens", "outputTokens", "output"),
		CacheRead:     FirstNumber(usage, "cache_read_input_tokens", "cacheReadInputTokens", "cacheReadTokens", "cache_read"),
		CacheCreation: FirstNumber(usage, "cache_creation_input_tokens", "cacheCreationInputTokens", "cacheCreationTokens", "cache_creation"),
	}
	if event.TotalTokens() == 0 {
		event.Input = FirstNumber(usage, "total_tokens", "totalTokens", "total")
	}
	return event, event.TotalTokens() > 0
}
