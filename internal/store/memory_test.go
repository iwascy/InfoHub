package store

import (
	"errors"
	"testing"
	"time"

	"infohub/internal/model"
)

func TestMemoryStoreSaveReturnsClonedSnapshot(t *testing.T) {
	s := NewMemoryStore()

	err := s.Save("claude_relay", []model.DataItem{{
		Source:    "claude_relay",
		Category:  "token_usage",
		Title:     "今日 Token 用量",
		Value:     "100",
		Extra:     map[string]any{"limit": "500"},
		FetchedAt: 123,
	}})
	if err != nil {
		t.Fatalf("save failed: %v", err)
	}

	snapshot, err := s.GetBySource("claude_relay")
	if err != nil {
		t.Fatalf("get by source failed: %v", err)
	}
	if snapshot.Status != "ok" {
		t.Fatalf("unexpected status: %s", snapshot.Status)
	}
	if snapshot.LastFetch != 123 {
		t.Fatalf("unexpected last fetch: %d", snapshot.LastFetch)
	}

	snapshot.Items[0].Extra["limit"] = "999"

	again, err := s.GetBySource("claude_relay")
	if err != nil {
		t.Fatalf("get by source failed: %v", err)
	}
	if got := again.Items[0].Extra["limit"]; got != "500" {
		t.Fatalf("expected cloned extra map, got %v", got)
	}
}

func TestMemoryStoreSaveFailurePreservesItemsAndUpdatesStatus(t *testing.T) {
	s := NewMemoryStore()

	if err := s.Save("sub2api", []model.DataItem{{
		Source:    "sub2api",
		Category:  "balance",
		Title:     "账户余额",
		Value:     "42",
		FetchedAt: 111,
	}}); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	failedAt := time.Unix(222, 0)
	if err := s.SaveFailure("sub2api", errors.New("upstream timeout"), failedAt); err != nil {
		t.Fatalf("save failure failed: %v", err)
	}

	snapshot, err := s.GetBySource("sub2api")
	if err != nil {
		t.Fatalf("get by source failed: %v", err)
	}
	if snapshot.Status != "error" {
		t.Fatalf("unexpected status: %s", snapshot.Status)
	}
	if snapshot.Error != "upstream timeout" {
		t.Fatalf("unexpected error: %s", snapshot.Error)
	}
	if snapshot.LastFetch != 222 {
		t.Fatalf("unexpected last fetch: %d", snapshot.LastFetch)
	}
	if len(snapshot.Items) != 1 || snapshot.Items[0].Value != "42" {
		t.Fatalf("expected previous items to remain, got %#v", snapshot.Items)
	}
}
