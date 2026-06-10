package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
)

func newPrintTestConfig(t *testing.T, claudeDir string) Config {
	t.Helper()
	return Config{
		MachineID: "test",
		StatePath: filepath.Join(t.TempDir(), "state.json"),
		Sources: map[string]SourceConfig{
			"claude_local": {Enabled: true, Paths: []string{claudeDir}},
			"codex_local":  {Enabled: false},
		},
	}
}

func TestPrintLocalUsageHumanReadable(t *testing.T) {
	claudeDir := t.TempDir()
	writeClaudeJSONL(t, filepath.Join(claudeDir, "session.jsonl"), 3)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var out bytes.Buffer
	if err := PrintLocalUsage(context.Background(), newPrintTestConfig(t, claudeDir), logger, &out, false); err != nil {
		t.Fatalf("PrintLocalUsage: %v", err)
	}

	text := out.String()
	for _, expected := range []string{"Claude Local (claude_local)", "今日 Token 用量", "input"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("output missing %q:\n%s", expected, text)
		}
	}
}

func TestPrintLocalUsageJSON(t *testing.T) {
	claudeDir := t.TempDir()
	writeClaudeJSONL(t, filepath.Join(claudeDir, "session.jsonl"), 2)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var out bytes.Buffer
	if err := PrintLocalUsage(context.Background(), newPrintTestConfig(t, claudeDir), logger, &out, true); err != nil {
		t.Fatalf("PrintLocalUsage: %v", err)
	}

	var decoded map[string][]map[string]any
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("output is not valid json: %v\n%s", err, out.String())
	}
	if len(decoded["claude_local"]) == 0 {
		t.Fatalf("expected claude_local items in json output: %s", out.String())
	}
}

func TestPrintLocalUsageNoData(t *testing.T) {
	cfg := newPrintTestConfig(t, filepath.Join(t.TempDir(), "missing"))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var out bytes.Buffer
	if err := PrintLocalUsage(context.Background(), cfg, logger, &out, false); err == nil {
		t.Fatal("expected error when no sources produce data")
	}
}

func TestGroupDigits(t *testing.T) {
	for input, want := range map[string]string{
		"0":          "0",
		"914":        "914",
		"1054603":    "1,054,603",
		"131747282":  "131,747,282",
		"12.5":       "12.5",
		"1234567.89": "1,234,567.89",
		"abc":        "abc",
		"":           "",
	} {
		if got := groupDigits(input); got != want {
			t.Fatalf("groupDigits(%q) = %q, want %q", input, got, want)
		}
	}
}
