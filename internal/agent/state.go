package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"infohub/internal/store"
)

const stateVersion = 1

type fileState struct {
	ByteOffset    int64 `json:"byte_offset"`
	MTimeUnix     int64 `json:"mtime_unix"`
	UpdatedAt     int64 `json:"updated_at"`
	ParserVersion int   `json:"parser_version"`
}

type stateFile struct {
	Version int                             `json:"version"`
	Sources map[string]map[string]fileState `json:"sources"`
}

func loadState(path string) (*stateFile, error) {
	state := &stateFile{Version: stateVersion, Sources: map[string]map[string]fileState{}}

	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return nil, fmt.Errorf("read agent state: %w", err)
	}
	if err := json.Unmarshal(content, state); err != nil {
		// A corrupt state file only costs a full re-scan; the server upsert
		// keeps the re-pushed records idempotent.
		return &stateFile{Version: stateVersion, Sources: map[string]map[string]fileState{}}, nil
	}
	if state.Sources == nil {
		state.Sources = map[string]map[string]fileState{}
	}
	state.Version = stateVersion
	return state, nil
}

func (s *stateFile) save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create agent state dir: %w", err)
	}
	payload, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encode agent state: %w", err)
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, payload, 0o600); err != nil {
		return fmt.Errorf("write agent state: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace agent state: %w", err)
	}
	return nil
}

func (s *stateFile) parseStates(source string) map[string]store.LocalParseState {
	states := map[string]store.LocalParseState{}
	for path, file := range s.Sources[source] {
		states[path] = store.LocalParseState{
			Source:        source,
			FilePath:      path,
			ByteOffset:    file.ByteOffset,
			MTimeUnix:     file.MTimeUnix,
			UpdatedAt:     file.UpdatedAt,
			ParserVersion: file.ParserVersion,
		}
	}
	return states
}

func (s *stateFile) apply(source string, nextStates []store.LocalParseState) {
	if len(nextStates) == 0 {
		return
	}
	files := s.Sources[source]
	if files == nil {
		files = map[string]fileState{}
		s.Sources[source] = files
	}
	for _, state := range nextStates {
		files[state.FilePath] = fileState{
			ByteOffset:    state.ByteOffset,
			MTimeUnix:     state.MTimeUnix,
			UpdatedAt:     state.UpdatedAt,
			ParserVersion: state.ParserVersion,
		}
	}
}
