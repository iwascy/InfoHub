package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"infohub/internal/collector"
	"infohub/internal/model"
	"infohub/internal/store"
)

type fakeCollector struct {
	name string
}

func (f fakeCollector) Name() string { return f.name }

func (f fakeCollector) Collect(_ context.Context) ([]model.DataItem, error) {
	return nil, nil
}

func TestSummaryHandler(t *testing.T) {
	dataStore := store.NewMemoryStore()
	if err := dataStore.Save("claude_relay", []model.DataItem{{
		Source:    "claude_relay",
		Category:  "token_usage",
		Title:     "今日 Token 用量",
		Value:     "123",
		FetchedAt: 1713600000,
	}}); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	registry := collector.NewRegistry()
	handler := NewHandler(dataStore, registry, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/summary", nil)
	rec := httptest.NewRecorder()
	handler.Summary(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", rec.Code)
	}

	var payload model.SummaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if payload.UpdatedAt != 1713600000 {
		t.Fatalf("unexpected updated_at: %d", payload.UpdatedAt)
	}
	if got := payload.Sources["claude_relay"].Items[0].Value; got != "123" {
		t.Fatalf("unexpected source item value: %s", got)
	}
}

func TestHealthHandlerIncludesUnknownRegisteredCollector(t *testing.T) {
	dataStore := store.NewMemoryStore()
	registry := collector.NewRegistry()
	registry.Register(fakeCollector{name: "feishu"})

	handler := NewHandler(dataStore, registry, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	handler.Health(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", rec.Code)
	}

	var payload model.HealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if payload.Collectors["feishu"].Status != "unknown" {
		t.Fatalf("unexpected collector status: %s", payload.Collectors["feishu"].Status)
	}
}
