package localscan

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"infohub/internal/store"
)

func claudeLine(t *testing.T, at string, input, output int) []byte {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"type":      "assistant",
		"timestamp": at,
		"message": map[string]any{
			"model": "claude-sonnet-4-6",
			"usage": map[string]any{"input_tokens": input, "output_tokens": output},
		},
	})
	if err != nil {
		t.Fatalf("marshal line: %v", err)
	}
	return append(payload, '\n')
}

func TestScannerIncrementalOffsets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(path, claudeLine(t, "2026-04-26T10:00:00Z", 100, 50), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	scanner := &Scanner{Source: SourceClaude, Paths: []string{dir}}

	states, records, err := scanner.ScanIncremental(context.Background(), nil)
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if len(records) != 1 || len(states) != 1 {
		t.Fatalf("first scan: records=%d states=%d", len(records), len(states))
	}
	if records[0].Input != 100 || records[0].ByteOffset != 0 {
		t.Fatalf("unexpected record: %+v", records[0])
	}

	stateMap := map[string]store.LocalParseState{states[0].FilePath: states[0]}

	// Unchanged file: nothing new.
	states2, records2, err := scanner.ScanIncremental(context.Background(), stateMap)
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if len(records2) != 0 || len(states2) != 0 {
		t.Fatalf("expected no-op rescan, got records=%d states=%d", len(records2), len(states2))
	}

	// Appended line parsed from the previous offset only.
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open append: %v", err)
	}
	if _, err := file.Write(claudeLine(t, "2026-04-26T11:00:00Z", 20, 5)); err != nil {
		t.Fatalf("append: %v", err)
	}
	_ = file.Close()

	states3, records3, err := scanner.ScanIncremental(context.Background(), stateMap)
	if err != nil {
		t.Fatalf("third scan: %v", err)
	}
	if len(records3) != 1 {
		t.Fatalf("expected 1 incremental record, got %d", len(records3))
	}
	if records3[0].ByteOffset != states[0].ByteOffset {
		t.Fatalf("incremental record offset = %d, want %d", records3[0].ByteOffset, states[0].ByteOffset)
	}
	if states3[0].Reset {
		t.Fatal("append must not trigger reset")
	}
}

func TestScannerIncrementalTruncationReset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	long := append(claudeLine(t, "2026-04-26T10:00:00Z", 100, 50), claudeLine(t, "2026-04-26T10:30:00Z", 10, 5)...)
	if err := os.WriteFile(path, long, 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	scanner := &Scanner{Source: SourceClaude, Paths: []string{dir}}
	states, _, err := scanner.ScanIncremental(context.Background(), nil)
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}
	stateMap := map[string]store.LocalParseState{states[0].FilePath: states[0]}

	// Truncate to a single line: file shrinks below the stored offset.
	if err := os.WriteFile(path, claudeLine(t, "2026-04-26T12:00:00Z", 7, 3), 0o644); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	states2, records2, err := scanner.ScanIncremental(context.Background(), stateMap)
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if len(records2) != 1 || records2[0].Input != 7 {
		t.Fatalf("expected re-parse from start, got %+v", records2)
	}
	if !states2[0].Reset {
		t.Fatal("truncation must set Reset")
	}
}

func TestScannerIncrementalParserVersionReset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(path, claudeLine(t, "2026-04-26T10:00:00Z", 100, 50), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	scanner := &Scanner{Source: SourceClaude, Paths: []string{dir}}
	states, _, err := scanner.ScanIncremental(context.Background(), nil)
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}

	outdated := states[0]
	outdated.ParserVersion = outdated.ParserVersion - 1
	stateMap := map[string]store.LocalParseState{outdated.FilePath: outdated}

	states2, records2, err := scanner.ScanIncremental(context.Background(), stateMap)
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if len(records2) != 1 || !states2[0].Reset {
		t.Fatalf("expected full re-parse with reset, got records=%d reset=%v", len(records2), states2[0].Reset)
	}
}

func TestScannerMissingPathError(t *testing.T) {
	scanner := &Scanner{Source: SourceClaude, Paths: []string{filepath.Join(t.TempDir(), "missing")}}
	if _, _, err := scanner.ScanIncremental(context.Background(), nil); err == nil || err.Error() != "claude path missing" {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := scanner.ScanFull(context.Background()); err == nil || err.Error() != "claude path missing" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCodexTokenCountQuotaExtraction(t *testing.T) {
	payload := map[string]any{
		"type":      "event_msg",
		"timestamp": "2026-04-26T10:00:00Z",
		"payload": map[string]any{
			"type": "token_count",
			"info": map[string]any{
				"last_token_usage": map[string]any{"input_tokens": 5, "output_tokens": 2},
			},
			"rate_limits": map[string]any{
				"primary":   map[string]any{"used_percent": 12.5, "resets_at": "2026-04-26T15:00:00Z"},
				"secondary": map[string]any{"used_percent": 40.0, "resets_at": "2026-04-28T00:00:00Z"},
			},
		},
	}

	event, ok := ExtractEvent(SourceCodex, normalizeJSON(t, payload))
	if !ok {
		t.Fatal("expected token_count event to parse")
	}
	if !event.Quota.FiveHour.OK || event.Quota.FiveHour.UsedPercent != 12.5 {
		t.Fatalf("unexpected 5h quota: %+v", event.Quota.FiveHour)
	}
	if !event.Quota.Week.OK || event.Quota.Week.UsedPercent != 40.0 {
		t.Fatalf("unexpected week quota: %+v", event.Quota.Week)
	}
	if event.At.IsZero() {
		t.Fatal("expected timestamp")
	}
}

func TestCodexScannerInheritsModelAndSplitsCachedInput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout-1.jsonl")
	lines := []map[string]any{
		{
			"timestamp": "2026-04-26T10:00:00Z",
			"type":      "turn_context",
			"payload": map[string]any{
				"model": "gpt-5-codex",
			},
		},
		{
			"timestamp": "2026-04-26T10:01:00Z",
			"type":      "event_msg",
			"payload": map[string]any{
				"type": "token_count",
				"info": map[string]any{
					"last_token_usage": map[string]any{
						"input_tokens":        1000,
						"cached_input_tokens": 750,
						"output_tokens":       20,
						"total_tokens":        1020,
					},
				},
			},
		},
	}
	var raw []byte
	for _, line := range lines {
		payload, err := json.Marshal(line)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		raw = append(raw, payload...)
		raw = append(raw, '\n')
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	scanner := &Scanner{Source: SourceCodex, Paths: []string{dir}}
	events, err := scanner.ScanFull(context.Background())
	if err != nil {
		t.Fatalf("ScanFull: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Model != "gpt-5-codex" {
		t.Fatalf("unexpected inherited model: %q", events[0].Model)
	}
	if events[0].Input != 250 || events[0].CacheRead != 750 {
		t.Fatalf("unexpected input split: input=%v cache=%v", events[0].Input, events[0].CacheRead)
	}
	if events[0].TotalTokens() != 1020 {
		t.Fatalf("unexpected total tokens: %v", events[0].TotalTokens())
	}
}

// normalizeJSON round-trips through encoding/json so numbers arrive as
// json.Number, matching what the scanner's decoder produces.
func normalizeJSON(t *testing.T, value any) any {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded any
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return decoded
}
