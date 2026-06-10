package agent

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"infohub/internal/localscan"
	"infohub/internal/store"
)

type capturedPush struct {
	Body   pushRequest
	Header http.Header
}

type fakeIngestServer struct {
	mu       sync.Mutex
	pushes   []capturedPush
	failNext int
}

func (f *fakeIngestServer) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		payload, _ := io.ReadAll(r.Body)
		var body pushRequest
		_ = json.Unmarshal(payload, &body)

		f.mu.Lock()
		defer f.mu.Unlock()
		if f.failNext > 0 {
			f.failNext--
			http.Error(w, `{"error":"boom"}`, http.StatusInternalServerError)
			return
		}
		f.pushes = append(f.pushes, capturedPush{Body: body, Header: r.Header.Clone()})
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}
}

func (f *fakeIngestServer) pushCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.pushes)
}

func (f *fakeIngestServer) push(index int) capturedPush {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pushes[index]
}

func writeClaudeJSONL(t *testing.T, path string, count int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create jsonl: %v", err)
	}
	defer file.Close()
	for i := range count {
		line := map[string]any{
			"type":      "assistant",
			"timestamp": time.Unix(1700000000+int64(i*60), 0).UTC().Format(time.RFC3339),
			"message": map[string]any{
				"model": "claude-sonnet-4-6",
				"usage": map[string]any{"input_tokens": 10, "output_tokens": 5},
			},
		}
		payload, _ := json.Marshal(line)
		if _, err := file.Write(append(payload, '\n')); err != nil {
			t.Fatalf("write jsonl: %v", err)
		}
	}
}

func newTestRunner(t *testing.T, serverURL string, claudeDir string) *Runner {
	t.Helper()
	cfg := Config{
		Server:    ServerConfig{BaseURL: serverURL, IngestToken: "ingest-token", TimeoutSeconds: 5},
		MachineID: "test-machine",
		StatePath: filepath.Join(t.TempDir(), "state.json"),
		Sources: map[string]SourceConfig{
			"claude_local": {Enabled: true, Paths: []string{claudeDir}},
			"codex_local":  {Enabled: false},
		},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewRunner(cfg, "test", logger)
}

func TestRunnerPushesAndAdvancesState(t *testing.T) {
	server := &fakeIngestServer{}
	httpServer := httptest.NewServer(server.handler())
	defer httpServer.Close()

	claudeDir := t.TempDir()
	writeClaudeJSONL(t, filepath.Join(claudeDir, "session.jsonl"), 3)

	runner := newTestRunner(t, httpServer.URL, claudeDir)
	if err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if server.pushCount() != 1 {
		t.Fatalf("expected 1 push, got %d", server.pushCount())
	}
	push := server.push(0)
	if got := push.Header.Get("Authorization"); got != "Bearer ingest-token" {
		t.Fatalf("authorization header = %q", got)
	}
	if push.Body.MachineID != "test-machine" || push.Body.Source != "claude_local" {
		t.Fatalf("unexpected push body: %+v", push.Body)
	}
	if len(push.Body.Records) != 3 {
		t.Fatalf("expected 3 records, got %d", len(push.Body.Records))
	}

	// Second run with no new data must not push anything.
	if err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce second: %v", err)
	}
	if server.pushCount() != 1 {
		t.Fatalf("expected no new pushes, got %d", server.pushCount())
	}

	// Appended lines are pushed incrementally.
	writeClaudeJSONLAppend(t, filepath.Join(claudeDir, "session.jsonl"))
	if err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce third: %v", err)
	}
	if server.pushCount() != 2 {
		t.Fatalf("expected incremental push, got %d pushes", server.pushCount())
	}
	if len(server.push(1).Body.Records) != 1 {
		t.Fatalf("expected 1 incremental record, got %d", len(server.push(1).Body.Records))
	}
}

func writeClaudeJSONLAppend(t *testing.T, path string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open append: %v", err)
	}
	defer file.Close()
	line := map[string]any{
		"type":      "assistant",
		"timestamp": time.Unix(1700009000, 0).UTC().Format(time.RFC3339),
		"message": map[string]any{
			"model": "claude-sonnet-4-6",
			"usage": map[string]any{"input_tokens": 7, "output_tokens": 3},
		},
	}
	payload, _ := json.Marshal(line)
	if _, err := file.Write(append(payload, '\n')); err != nil {
		t.Fatalf("append jsonl: %v", err)
	}
}

func TestRunnerKeepsStateOnPushFailure(t *testing.T) {
	server := &fakeIngestServer{failNext: 1}
	httpServer := httptest.NewServer(server.handler())
	defer httpServer.Close()

	claudeDir := t.TempDir()
	writeClaudeJSONL(t, filepath.Join(claudeDir, "session.jsonl"), 2)

	runner := newTestRunner(t, httpServer.URL, claudeDir)
	if err := runner.RunOnce(context.Background()); err == nil {
		t.Fatal("expected error from failed push")
	}
	if server.pushCount() != 0 {
		t.Fatalf("expected no accepted pushes, got %d", server.pushCount())
	}

	// Retry succeeds and re-pushes the same records (server is idempotent).
	if err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce retry: %v", err)
	}
	if server.pushCount() != 1 {
		t.Fatalf("expected 1 push after retry, got %d", server.pushCount())
	}
	if len(server.push(0).Body.Records) != 2 {
		t.Fatalf("expected full re-push of 2 records, got %d", len(server.push(0).Body.Records))
	}
}

type fakeQuotaFetcher struct {
	limits localscan.RateLimits
}

func (f *fakeQuotaFetcher) FetchRateLimits(context.Context) (localscan.RateLimits, bool, error) {
	return f.limits, f.limits.HasAny(), nil
}

func TestRunnerAttachesClaudeQuota(t *testing.T) {
	server := &fakeIngestServer{}
	httpServer := httptest.NewServer(server.handler())
	defer httpServer.Close()

	claudeDir := t.TempDir()
	writeClaudeJSONL(t, filepath.Join(claudeDir, "session.jsonl"), 1)

	runner := newTestRunner(t, httpServer.URL, claudeDir)
	runner.quotaFetchers["claude_local"] = &fakeQuotaFetcher{limits: localscan.RateLimits{
		FiveHour: localscan.QuotaObservation{OK: true, UsedPercent: 21, ResetAt: "2026-06-10T18:00:00Z"},
		Week:     localscan.QuotaObservation{OK: true, UsedPercent: 63, ResetAt: "2026-06-15T00:00:00Z"},
	}}

	if err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	push := server.push(0)
	if push.Body.Quota == nil || push.Body.Quota.FiveHour == nil || push.Body.Quota.Week == nil {
		t.Fatalf("expected quota attached, got %+v", push.Body.Quota)
	}
	if push.Body.Quota.FiveHour.UsedPercent != 21 {
		t.Fatalf("unexpected quota: %+v", push.Body.Quota.FiveHour)
	}

	// With no new records, the quota-only push still goes out.
	if err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce quota-only: %v", err)
	}
	quotaOnly := server.push(1)
	if len(quotaOnly.Body.Records) != 0 || quotaOnly.Body.Quota == nil {
		t.Fatalf("expected quota-only push, got records=%d quota=%v", len(quotaOnly.Body.Records), quotaOnly.Body.Quota)
	}
}

func TestChunkRecords(t *testing.T) {
	records := make([]store.LocalUsageRecord, 2500)
	chunks := chunkRecords(records, 1000)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}
	if len(chunks[0]) != 1000 || len(chunks[2]) != 500 {
		t.Fatalf("unexpected chunk sizes: %d, %d", len(chunks[0]), len(chunks[2]))
	}
	if chunkRecords(nil, 1000) != nil {
		t.Fatal("expected nil chunks for empty input")
	}
}
