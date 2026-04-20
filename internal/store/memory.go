package store

import (
	"slices"
	"sync"
	"time"

	"infohub/internal/model"
)

type MemoryStore struct {
	mu      sync.RWMutex
	sources map[string]model.SourceSnapshot
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		sources: make(map[string]model.SourceSnapshot),
	}
}

func (s *MemoryStore) Save(source string, items []model.DataItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	snapshot := s.sources[source]
	snapshot.Status = "ok"
	snapshot.Error = ""
	snapshot.Items = cloneItems(items)
	snapshot.LastFetch = resolveLastFetch(items)
	if snapshot.LastFetch == 0 {
		snapshot.LastFetch = time.Now().Unix()
	}

	s.sources[source] = snapshot
	return nil
}

func (s *MemoryStore) SaveFailure(source string, err error, fetchedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	snapshot := s.sources[source]
	snapshot.Status = "error"
	if err != nil {
		snapshot.Error = err.Error()
	}
	if fetchedAt.IsZero() {
		fetchedAt = time.Now()
	}
	snapshot.LastFetch = fetchedAt.Unix()
	s.sources[source] = snapshot
	return nil
}

func (s *MemoryStore) GetBySource(source string) (model.SourceSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshot, ok := s.sources[source]
	if !ok {
		return model.SourceSnapshot{}, ErrSourceNotFound
	}
	return cloneSnapshot(snapshot), nil
}

func (s *MemoryStore) GetAll() (map[string]model.SourceSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]model.SourceSnapshot, len(s.sources))
	for source, snapshot := range s.sources {
		result[source] = cloneSnapshot(snapshot)
	}
	return result, nil
}

func (s *MemoryStore) Close() error {
	return nil
}

func resolveLastFetch(items []model.DataItem) int64 {
	var lastFetch int64
	for _, item := range items {
		if item.FetchedAt > lastFetch {
			lastFetch = item.FetchedAt
		}
	}
	return lastFetch
}

func cloneSnapshot(snapshot model.SourceSnapshot) model.SourceSnapshot {
	cloned := snapshot
	cloned.Items = cloneItems(snapshot.Items)
	return cloned
}

func cloneItems(items []model.DataItem) []model.DataItem {
	cloned := slices.Clone(items)
	for index := range cloned {
		if cloned[index].Extra == nil {
			continue
		}
		extra := make(map[string]any, len(cloned[index].Extra))
		for key, value := range cloned[index].Extra {
			extra[key] = value
		}
		cloned[index].Extra = extra
	}
	return cloned
}
