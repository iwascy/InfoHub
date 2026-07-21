package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"infohub/internal/collector"
	"infohub/internal/config"
	"infohub/internal/model"
)

// PrintLocalUsage scans the local JSONL files (and online quota, when
// enabled) exactly like the server-side builtin collectors would, then
// writes the result to w. It never talks to the infohub server.
func PrintLocalUsage(ctx context.Context, cfg Config, logger *slog.Logger, w io.Writer, asJSON bool) error {
	results := map[string][]model.DataItem{}

	for _, source := range []string{"claude_local", "codex_local"} {
		sourceCfg, ok := cfg.Sources[source]
		if !ok || !sourceCfg.Enabled {
			continue
		}

		localCfg := config.LocalCollectorConfig{Paths: sourceCfg.Paths, Mode: "builtin"}
		var localCollector *collector.LocalUsageCollector
		if source == "claude_local" {
			localCollector = collector.NewClaudeLocalCollector(localCfg, logger)
			if cfg.ClaudeQuota.Enabled {
				localCollector.SetClaudeOnlineQuotaClient(collector.NewClaudeOnlineQuotaClient(cfg.ClaudeQuota, logger))
			}
		} else {
			localCollector = collector.NewCodexLocalCollector(localCfg, logger)
			if cfg.CodexQuota.Enabled {
				localCollector.SetCodexOnlineQuotaClient(collector.NewCodexOnlineQuotaClient(cfg.CodexQuota, logger))
			}
		}

		collectStart := time.Now()
		items, err := localCollector.Collect(ctx)
		if err != nil {
			if logger != nil {
				logger.Warn("local usage scan failed", "source", source, "error", err)
			}
			continue
		}
		if logger != nil {
			logger.Info("local usage scan complete", "source", source, "items", len(items), "duration", time.Since(collectStart))
		}
		results[source] = items
	}

	if len(results) == 0 {
		return fmt.Errorf("no local usage data found (check sources.*.paths)")
	}

	if asJSON {
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(results)
	}

	for _, source := range []string{"claude_local", "codex_local"} {
		items, ok := results[source]
		if !ok {
			continue
		}
		printSourceItems(w, source, items)
	}
	return nil
}

func printSourceItems(w io.Writer, source string, items []model.DataItem) {
	title := "Claude Local"
	if source == "codex_local" {
		title = "Codex Local"
	}
	fmt.Fprintf(w, "%s (%s)\n", title, source)

	for _, item := range items {
		switch {
		case item.Category == "token_usage":
			fmt.Fprintf(w, "  今日 Token 用量: %s  (请求 %s)\n", groupDigits(item.Value), formatExtraNumber(item.Extra["daily_requests"]))
			fmt.Fprintf(w, "    input %s | output %s | cache_read %s | cache_creation %s | reasoning %s\n",
				formatExtraNumber(item.Extra["input"]),
				formatExtraNumber(item.Extra["output"]),
				formatExtraNumber(item.Extra["cache_read"]),
				formatExtraNumber(item.Extra["cache_creation"]),
				formatExtraNumber(item.Extra["reasoning"]),
			)
		case item.Category == "quota" && hasWindowLabel(item.Extra):
			window, _ := item.Extra["window"].(string)
			line := fmt.Sprintf("  %s 额度剩余: %s", window, item.Value)
			if source, ok := item.Extra["quota_source"].(string); ok && source != "" {
				line += fmt.Sprintf("  [%s]", source)
			}
			if approx, ok := item.Extra["approx"].(bool); ok && approx {
				line += "  (估算)"
			}
			if resetAt, ok := item.Extra["reset_at"].(string); ok && resetAt != "" {
				line += fmt.Sprintf("  重置 %s", resetAt)
			}
			fmt.Fprintln(w, line)
		case item.Category == "quota":
			fmt.Fprintf(w, "  %s: %s tokens\n", item.Title, groupDigits(strings.TrimSuffix(item.Value, " tokens")))
		case item.Title == "model_top1":
			share := formatExtraNumber(item.Extra["share_percent"])
			fmt.Fprintf(w, "  Top 模型: %s (%s tokens, %s%%)\n", item.Value, formatExtraNumber(item.Extra["tokens"]), share)
		case item.Title == "cache_hit":
			fmt.Fprintf(w, "  缓存命中率: %s\n", item.Value)
		case item.Title == "reasoning_share":
			fmt.Fprintf(w, "  推理占比: %s\n", item.Value)
		}
	}
	fmt.Fprintln(w)
}

func hasWindowLabel(extra map[string]any) bool {
	window, _ := extra["window"].(string)
	return window == "5H" || window == "Week"
}

func formatExtraNumber(value any) string {
	switch typed := value.(type) {
	case float64:
		return groupDigits(strconv.FormatFloat(typed, 'f', -1, 64))
	case int64:
		return groupDigits(strconv.FormatInt(typed, 10))
	case int:
		return groupDigits(strconv.Itoa(typed))
	case json.Number:
		return groupDigits(typed.String())
	case nil:
		return "0"
	default:
		return fmt.Sprintf("%v", typed)
	}
}

// groupDigits inserts thousands separators into the integer part of a
// non-negative decimal string; anything else is returned unchanged.
func groupDigits(value string) string {
	value = strings.TrimSpace(value)
	intPart, fracPart, hasFrac := strings.Cut(value, ".")
	if intPart == "" || !isDigits(intPart) || (hasFrac && !isDigits(fracPart)) {
		return value
	}
	var out strings.Builder
	for index, digit := range intPart {
		if index > 0 && (len(intPart)-index)%3 == 0 {
			out.WriteByte(',')
		}
		out.WriteRune(digit)
	}
	if hasFrac {
		out.WriteString("." + fracPart)
	}
	return out.String()
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}
