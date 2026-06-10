package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"infohub/internal/collector"
	"infohub/internal/store"
)

func newIngestTestRouter(t *testing.T) (http.Handler, *store.SQLiteStore) {
	t.Helper()
	dataStore, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "ingest.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = dataStore.Close() })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := NewRouter(dataStore, collector.NewRegistry(), nil, logger, "api-token", "", "ingest-token", false, DashboardSources{})
	return router, dataStore
}

func ingestPayload(records int) []byte {
	recs := make([]map[string]any, 0, records)
	for i := range records {
		recs = append(recs, map[string]any{
			"file_path":   "/home/dev/.claude/projects/p/s.jsonl",
			"byte_offset": i * 100,
			"at":          time.Unix(1700000000+int64(i), 0).UTC().Format(time.RFC3339),
			"model":       "claude-x",
			"input":       10,
			"output":      5,
		})
	}
	payload, _ := json.Marshal(map[string]any{
		"machine_id":    "mbp",
		"agent_version": "0.1.0",
		"source":        "claude_local",
		"records":       recs,
		"quota": map[string]any{
			"five_hour": map[string]any{"used_percent": 12.5, "reset_at": "2026-06-10T18:00:00Z"},
			"week":      map[string]any{"used_percent": 40.0, "reset_at": "2026-06-15T00:00:00Z"},
		},
	})
	return payload
}

func TestIngestLocalUsagePersistsRecordsAndQuota(t *testing.T) {
	router, dataStore := newIngestTestRouter(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/ingest/local-usage", bytes.NewReader(ingestPayload(3)))
	req.Header.Set("Authorization", "Bearer ingest-token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"accepted":3`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}

	records, err := dataStore.ListLocalUsageRecords("claude_local", time.Unix(0, 0), time.Unix(1800000000, 0))
	if err != nil {
		t.Fatalf("ListLocalUsageRecords: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("expected 3 persisted records, got %d", len(records))
	}
	if records[0].Machine != "mbp" {
		t.Fatalf("record machine = %q", records[0].Machine)
	}
	if records[0].Quota5hUsed != -1 {
		t.Fatalf("missing quota field should default to -1, got %v", records[0].Quota5hUsed)
	}

	obs, ok, err := dataStore.LatestAgentQuotaObservation("claude_local")
	if err != nil || !ok {
		t.Fatalf("LatestAgentQuotaObservation: ok=%v err=%v", ok, err)
	}
	if obs.Quota5hUsed != 12.5 || obs.QuotaWeekUsed != 40.0 {
		t.Fatalf("unexpected observation: %+v", obs)
	}
}

func TestIngestAuthTokens(t *testing.T) {
	router, _ := newIngestTestRouter(t)

	for _, tc := range []struct {
		name   string
		header string
		status int
	}{
		{"accepts ingest token", "Bearer ingest-token", http.StatusOK},
		{"rejects api token on ingest route", "Bearer api-token", http.StatusUnauthorized},
		{"rejects missing token", "", http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/ingest/local-usage", bytes.NewReader(ingestPayload(1)))
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.status, rec.Body.String())
			}
		})
	}
}

func TestIngestValidation(t *testing.T) {
	router, _ := newIngestTestRouter(t)

	post := func(body []byte) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/ingest/local-usage", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer ingest-token")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	if rec := post([]byte(`{"machine_id":"","source":"claude_local","records":[]}`)); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty machine_id: status = %d", rec.Code)
	}
	if rec := post([]byte(`{"machine_id":"mbp","source":"bogus","records":[]}`)); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad source: status = %d", rec.Code)
	}
	if rec := post([]byte(`{"machine_id":"mbp","source":"claude_local","records":[{"file_path":"/a.jsonl","byte_offset":0,"at":"not-a-time"}]}`)); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad timestamp: status = %d", rec.Code)
	}
	if rec := post(ingestPayload(5001)); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize batch: status = %d", rec.Code)
	}
}

func TestIngestRequiresSQLiteStore(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := NewRouter(store.NewMemoryStore(), collector.NewRegistry(), nil, logger, "api-token", "", "ingest-token", false, DashboardSources{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/ingest/local-usage", bytes.NewReader(ingestPayload(1)))
	req.Header.Set("Authorization", "Bearer ingest-token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}
