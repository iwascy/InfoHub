package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func newTestSQLiteStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestSQLiteMachineColumnMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	const legacySchema = `
CREATE TABLE local_usage_events (
	source TEXT NOT NULL,
	file_path TEXT NOT NULL,
	byte_offset INTEGER NOT NULL,
	at_unix INTEGER NOT NULL,
	model TEXT NOT NULL,
	input REAL NOT NULL,
	output REAL NOT NULL,
	cache_read REAL NOT NULL,
	cache_creation REAL NOT NULL,
	reasoning REAL NOT NULL,
	PRIMARY KEY (source, file_path, byte_offset)
);
INSERT INTO local_usage_events VALUES
	('claude_local', '/tmp/a.jsonl', 0, 1700000000, 'claude-x', 10, 20, 0, 0, 0),
	('claude_local', '/tmp/a.jsonl', 100, 1700000100, 'claude-x', 1, 2, 0, 0, 0);
`
	if _, err := db.Exec(legacySchema); err != nil {
		t.Fatalf("seed legacy schema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore on legacy db: %v", err)
	}
	defer store.Close()

	columns, err := store.localUsageColumns()
	if err != nil {
		t.Fatalf("localUsageColumns: %v", err)
	}
	if !columns["machine"] {
		t.Fatal("machine column missing after migration")
	}

	records, err := store.ListLocalUsageRecords("claude_local", time.Unix(0, 0), time.Unix(1800000000, 0))
	if err != nil {
		t.Fatalf("ListLocalUsageRecords: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 migrated records, got %d", len(records))
	}
	for _, record := range records {
		if record.Machine != "" {
			t.Fatalf("migrated record machine = %q, want empty", record.Machine)
		}
	}

	// Re-opening must be a no-op (idempotent migration).
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	store2, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("reopen migrated db: %v", err)
	}
	defer store2.Close()
}

func TestSaveIngestedUsageIdempotentAndResetFiles(t *testing.T) {
	store := newTestSQLiteStore(t)

	records := []LocalUsageRecord{
		{FilePath: "/home/dev/.claude/projects/p/s.jsonl", ByteOffset: 0, At: time.Unix(1700000000, 0), Model: "claude-x", Input: 100, Output: 50, Quota5hUsed: -1, QuotaWeekUsed: -1},
		{FilePath: "/home/dev/.claude/projects/p/s.jsonl", ByteOffset: 200, At: time.Unix(1700000100, 0), Model: "claude-x", Input: 10, Output: 5, Quota5hUsed: -1, QuotaWeekUsed: -1},
	}

	if err := store.SaveIngestedUsage("mbp", "claude_local", nil, records); err != nil {
		t.Fatalf("SaveIngestedUsage: %v", err)
	}
	// Re-push the same batch: upsert must keep row count stable.
	if err := store.SaveIngestedUsage("mbp", "claude_local", nil, records); err != nil {
		t.Fatalf("SaveIngestedUsage repeat: %v", err)
	}

	got, err := store.ListLocalUsageRecords("claude_local", time.Unix(0, 0), time.Unix(1800000000, 0))
	if err != nil {
		t.Fatalf("ListLocalUsageRecords: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 records after duplicate push, got %d", len(got))
	}
	if got[0].Machine != "mbp" {
		t.Fatalf("record machine = %q, want mbp", got[0].Machine)
	}

	// Same path on a different machine must not collide.
	if err := store.SaveIngestedUsage("desk", "claude_local", nil, records[:1]); err != nil {
		t.Fatalf("SaveIngestedUsage other machine: %v", err)
	}
	got, err = store.ListLocalUsageRecords("claude_local", time.Unix(0, 0), time.Unix(1800000000, 0))
	if err != nil {
		t.Fatalf("ListLocalUsageRecords: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 records across machines, got %d", len(got))
	}

	// reset_files purges only the named file for that machine.
	if err := store.SaveIngestedUsage("mbp", "claude_local", []string{"/home/dev/.claude/projects/p/s.jsonl"}, records[:1]); err != nil {
		t.Fatalf("SaveIngestedUsage reset: %v", err)
	}
	got, err = store.ListLocalUsageRecords("claude_local", time.Unix(0, 0), time.Unix(1800000000, 0))
	if err != nil {
		t.Fatalf("ListLocalUsageRecords: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 records after reset (1 mbp + 1 desk), got %d", len(got))
	}
}

func TestAgentQuotaObservationLatestWins(t *testing.T) {
	store := newTestSQLiteStore(t)

	if _, ok, err := store.LatestAgentQuotaObservation("claude_local"); err != nil || ok {
		t.Fatalf("expected no observation, got ok=%v err=%v", ok, err)
	}

	older := AgentQuotaObservation{
		Machine: "desk", Source: "claude_local",
		Quota5hUsed: 30, Quota5hReset: "2026-06-10T12:00:00Z",
		QuotaWeekUsed: 50, QuotaWeekReset: "2026-06-15T00:00:00Z",
		ObservedAt: time.Unix(1700000000, 0), AgentVersion: "0.1.0",
	}
	newer := older
	newer.Machine = "mbp"
	newer.Quota5hUsed = 42
	newer.ObservedAt = time.Unix(1700000500, 0)

	if err := store.SaveAgentQuotaObservation(older); err != nil {
		t.Fatalf("SaveAgentQuotaObservation older: %v", err)
	}
	if err := store.SaveAgentQuotaObservation(newer); err != nil {
		t.Fatalf("SaveAgentQuotaObservation newer: %v", err)
	}

	got, ok, err := store.LatestAgentQuotaObservation("claude_local")
	if err != nil || !ok {
		t.Fatalf("LatestAgentQuotaObservation: ok=%v err=%v", ok, err)
	}
	if got.Machine != "mbp" || got.Quota5hUsed != 42 {
		t.Fatalf("latest observation = %+v, want machine=mbp used=42", got)
	}

	// Upsert for the same machine replaces in place.
	newest := newer
	newest.Quota5hUsed = 55
	newest.ObservedAt = time.Unix(1700000900, 0)
	if err := store.SaveAgentQuotaObservation(newest); err != nil {
		t.Fatalf("SaveAgentQuotaObservation newest: %v", err)
	}
	got, ok, err = store.LatestAgentQuotaObservation("claude_local")
	if err != nil || !ok {
		t.Fatalf("LatestAgentQuotaObservation: ok=%v err=%v", ok, err)
	}
	if got.Quota5hUsed != 55 {
		t.Fatalf("latest 5h used = %v, want 55", got.Quota5hUsed)
	}
}
