package localscan

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func NestedValue(payload any, path string) (any, bool) {
	if strings.TrimSpace(path) == "" {
		return nil, false
	}

	current := payload
	for _, part := range strings.Split(path, ".") {
		key := strings.TrimSpace(part)
		if key == "" {
			return nil, false
		}

		record, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}

		next, ok := record[key]
		if !ok {
			return nil, false
		}
		current = next
	}

	return current, true
}

func NestedMap(record map[string]any, path string) (map[string]any, bool) {
	value, ok := NestedValue(record, path)
	if !ok {
		return nil, false
	}
	typed, ok := value.(map[string]any)
	return typed, ok
}

func FirstNestedMap(record map[string]any, paths ...string) (map[string]any, bool) {
	for _, path := range paths {
		if value, ok := NestedMap(record, path); ok {
			return value, true
		}
	}
	return nil, false
}

func FirstNestedString(record map[string]any, paths ...string) string {
	for _, path := range paths {
		value, ok := NestedValue(record, path)
		if !ok {
			continue
		}
		if text := strings.TrimSpace(Stringify(value)); text != "" {
			return text
		}
	}
	return ""
}

func FirstTime(record map[string]any, paths ...string) (time.Time, bool) {
	for _, path := range paths {
		value, ok := NestedValue(record, path)
		if !ok {
			continue
		}
		if parsed, ok := ParseEventTime(value); ok {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func ParseEventTime(value any) (time.Time, bool) {
	switch typed := value.(type) {
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			return time.Unix(parsed, 0), true
		}
		if parsed, err := typed.Float64(); err == nil {
			seconds := int64(parsed)
			nanos := int64((parsed - float64(seconds)) * 1e9)
			return time.Unix(seconds, nanos), true
		}
	case float64:
		seconds := int64(typed)
		nanos := int64((typed - float64(seconds)) * 1e9)
		return time.Unix(seconds, nanos), true
	case int64:
		return time.Unix(typed, 0), true
	case int:
		return time.Unix(int64(typed), 0), true
	case string:
		text := strings.TrimSpace(typed)
		if text == "" {
			return time.Time{}, false
		}
		if parsed, err := time.Parse(time.RFC3339Nano, text); err == nil {
			return parsed, true
		}
		if parsed, err := time.ParseInLocation("2006-01-02", text, time.Local); err == nil {
			return parsed, true
		}
		if parsed, err := strconv.ParseInt(text, 10, 64); err == nil {
			return time.Unix(parsed, 0), true
		}
		if parsed, err := strconv.ParseFloat(text, 64); err == nil {
			seconds := int64(parsed)
			nanos := int64((parsed - float64(seconds)) * 1e9)
			return time.Unix(seconds, nanos), true
		}
	}
	return time.Time{}, false
}

func NumberAt(record map[string]any, key string) float64 {
	value, ok := record[key]
	if !ok {
		return 0
	}
	number, ok := FloatValue(value)
	if !ok {
		return 0
	}
	return number
}

func FirstNumber(record map[string]any, keys ...string) float64 {
	for _, key := range keys {
		if number := NumberAt(record, key); number != 0 {
			return number
		}
	}
	return 0
}

func FloatValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func Stringify(value any) string {
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
