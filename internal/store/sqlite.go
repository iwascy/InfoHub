package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"infohub/internal/model"

	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db     *sql.DB
	memory *MemoryStore
}

func NewSQLiteStore(path string) (*SQLiteStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create sqlite dir: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}

	store := &SQLiteStore{
		db:     db,
		memory: NewMemoryStore(),
	}

	if err := store.initSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.loadSnapshots(); err != nil {
		_ = db.Close()
		return nil, err
	}

	return store, nil
}

func (s *SQLiteStore) Save(source string, items []model.DataItem) error {
	lastFetch := resolveLastFetch(items)
	if lastFetch == 0 {
		lastFetch = time.Now().Unix()
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin sqlite tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		INSERT INTO source_state(source, status, last_fetch, error)
		VALUES (?, 'ok', ?, '')
		ON CONFLICT(source) DO UPDATE SET status='ok', last_fetch=excluded.last_fetch, error=''
	`, source, lastFetch); err != nil {
		return fmt.Errorf("upsert source state: %w", err)
	}

	if _, err := tx.Exec(`DELETE FROM source_items WHERE source = ?`, source); err != nil {
		return fmt.Errorf("delete old items: %w", err)
	}

	for _, item := range items {
		extraJSON, err := json.Marshal(item.Extra)
		if err != nil {
			return fmt.Errorf("marshal extra: %w", err)
		}
		if _, err := tx.Exec(`
			INSERT INTO source_items(source, category, title, value, extra_json, fetched_at)
			VALUES (?, ?, ?, ?, ?, ?)
		`, source, item.Category, item.Title, item.Value, string(extraJSON), item.FetchedAt); err != nil {
			return fmt.Errorf("insert source item: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit sqlite tx: %w", err)
	}

	return s.memory.Save(source, items)
}

func (s *SQLiteStore) SaveFailure(source string, err error, fetchedAt time.Time) error {
	if fetchedAt.IsZero() {
		fetchedAt = time.Now()
	}
	message := ""
	if err != nil {
		message = err.Error()
	}

	if _, execErr := s.db.Exec(`
		INSERT INTO source_state(source, status, last_fetch, error)
		VALUES (?, 'error', ?, ?)
		ON CONFLICT(source) DO UPDATE SET status='error', last_fetch=excluded.last_fetch, error=excluded.error
	`, source, fetchedAt.Unix(), message); execErr != nil {
		return fmt.Errorf("persist source failure: %w", execErr)
	}

	return s.memory.SaveFailure(source, err, fetchedAt)
}

func (s *SQLiteStore) GetBySource(source string) (model.SourceSnapshot, error) {
	return s.memory.GetBySource(source)
}

func (s *SQLiteStore) GetAll() (map[string]model.SourceSnapshot, error) {
	return s.memory.GetAll()
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteStore) initSchema() error {
	const schema = `
CREATE TABLE IF NOT EXISTS source_state (
	source TEXT PRIMARY KEY,
	status TEXT NOT NULL,
	last_fetch INTEGER NOT NULL,
	error TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS source_items (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	source TEXT NOT NULL,
	category TEXT NOT NULL,
	title TEXT NOT NULL,
	value TEXT NOT NULL,
	extra_json TEXT NOT NULL DEFAULT '{}',
	fetched_at INTEGER NOT NULL
);
`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("init sqlite schema: %w", err)
	}
	return nil
}

func (s *SQLiteStore) loadSnapshots() error {
	states, err := s.loadStates()
	if err != nil {
		return err
	}

	rows, err := s.db.Query(`
		SELECT source, category, title, value, extra_json, fetched_at
		FROM source_items
		ORDER BY id ASC
	`)
	if err != nil {
		return fmt.Errorf("query source items: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			source    string
			category  string
			title     string
			value     string
			extraJSON string
			fetchedAt int64
		)
		if err := rows.Scan(&source, &category, &title, &value, &extraJSON, &fetchedAt); err != nil {
			return fmt.Errorf("scan source item: %w", err)
		}

		item := model.DataItem{
			Source:    source,
			Category:  category,
			Title:     title,
			Value:     value,
			FetchedAt: fetchedAt,
		}
		if extraJSON != "" && extraJSON != "null" {
			var extra map[string]any
			if err := json.Unmarshal([]byte(extraJSON), &extra); err == nil && len(extra) > 0 {
				item.Extra = extra
			}
		}

		snapshot := states[source]
		snapshot.Items = append(snapshot.Items, item)
		states[source] = snapshot
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate source items: %w", err)
	}

	s.memory.mu.Lock()
	defer s.memory.mu.Unlock()
	s.memory.sources = states
	return nil
}

func (s *SQLiteStore) loadStates() (map[string]model.SourceSnapshot, error) {
	rows, err := s.db.Query(`SELECT source, status, last_fetch, error FROM source_state`)
	if err != nil {
		return nil, fmt.Errorf("query source states: %w", err)
	}
	defer rows.Close()

	states := make(map[string]model.SourceSnapshot)
	for rows.Next() {
		var (
			source    string
			status    string
			lastFetch int64
			errorText string
		)
		if err := rows.Scan(&source, &status, &lastFetch, &errorText); err != nil {
			return nil, fmt.Errorf("scan source state: %w", err)
		}

		states[source] = model.SourceSnapshot{
			Status:    status,
			LastFetch: lastFetch,
			Error:     errorText,
			Items:     []model.DataItem{},
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate source states: %w", err)
	}

	return states, nil
}
