package localscan

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"infohub/internal/store"
)

// ExpandPaths expands env vars and "~/" in raw paths, dropping blanks and
// duplicates while preserving order.
func ExpandPaths(rawPaths []string) []string {
	seen := map[string]struct{}{}
	paths := make([]string, 0, len(rawPaths))
	for _, raw := range rawPaths {
		path := strings.TrimSpace(os.ExpandEnv(raw))
		if strings.HasPrefix(path, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
			}
		}
		if path == "" {
			continue
		}
		cleaned := filepath.Clean(path)
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		paths = append(paths, cleaned)
	}
	return paths
}

// Scanner walks JSONL files for one local usage source. It performs no
// persistence itself; callers own parse-state storage.
type Scanner struct {
	Source string
	Paths  []string
	Logger *slog.Logger
	Now    func() time.Time
}

func (s *Scanner) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Scanner) missingPathError() error {
	return fmt.Errorf("%s path missing", strings.TrimSuffix(s.Source, "_local"))
}

// ScanFull re-parses every JSONL file from the beginning and returns events.
func (s *Scanner) ScanFull(ctx context.Context) ([]Event, error) {
	paths := ExpandPaths(s.Paths)
	if len(paths) == 0 {
		return nil, s.missingPathError()
	}

	var (
		events   []Event
		foundDir bool
	)
	for _, root := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		info, err := os.Stat(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("stat %s: %w", root, err)
		}
		if !info.IsDir() {
			continue
		}
		foundDir = true

		walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if entry.IsDir() || filepath.Ext(path) != ".jsonl" {
				return nil
			}

			fileEvents, err := s.parseJSONL(path)
			if err != nil && s.Logger != nil {
				s.Logger.Debug("skip local usage file", "source", s.Source, "path", path, "error", err)
			}
			events = append(events, fileEvents...)
			return nil
		})
		if walkErr != nil {
			return nil, walkErr
		}
	}

	if !foundDir {
		return nil, s.missingPathError()
	}

	return events, nil
}

// ScanIncremental walks JSONL files and parses only content appended since
// the given parse states. It returns updated states for changed files plus
// the newly parsed records; truncated or re-versioned files are re-parsed
// from the start with State.Reset set.
func (s *Scanner) ScanIncremental(ctx context.Context, states map[string]store.LocalParseState) ([]store.LocalParseState, []store.LocalUsageRecord, error) {
	paths := ExpandPaths(s.Paths)
	if len(paths) == 0 {
		return nil, nil, s.missingPathError()
	}

	parserVersion := ParserVersion(s.Source)

	var (
		nextStates []store.LocalParseState
		records    []store.LocalUsageRecord
		foundDir   bool
	)
	for _, root := range paths {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}

		info, err := os.Stat(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, nil, fmt.Errorf("stat %s: %w", root, err)
		}
		if !info.IsDir() {
			continue
		}
		foundDir = true

		walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if entry.IsDir() || filepath.Ext(path) != ".jsonl" {
				return nil
			}

			info, err := entry.Info()
			if err != nil {
				return nil
			}
			offset := int64(0)
			reset := false
			if state, ok := states[path]; ok {
				offset = state.ByteOffset
				if info.Size() < offset || info.ModTime().UnixNano() < state.MTimeUnix || state.ParserVersion != parserVersion {
					offset = 0
					reset = true
				}
			}
			if info.Size() == offset && !reset {
				return nil
			}

			fileRecords, nextOffset, err := s.parseJSONLRecords(path, offset)
			if err != nil {
				if s.Logger != nil {
					s.Logger.Debug("skip local usage file", "source", s.Source, "path", path, "error", err)
				}
				return nil
			}
			records = append(records, fileRecords...)
			nextStates = append(nextStates, store.LocalParseState{
				Source:        s.Source,
				FilePath:      path,
				ByteOffset:    nextOffset,
				MTimeUnix:     info.ModTime().UnixNano(),
				UpdatedAt:     s.now().Unix(),
				ParserVersion: parserVersion,
				Reset:         reset,
			})
			return nil
		})
		if walkErr != nil {
			return nil, nil, walkErr
		}
	}

	if !foundDir {
		return nil, nil, s.missingPathError()
	}

	return nextStates, records, nil
}

func (s *Scanner) parseJSONL(path string) ([]Event, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var events []Event
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var payload any
		decoder := json.NewDecoder(strings.NewReader(line))
		decoder.UseNumber()
		if err := decoder.Decode(&payload); err != nil {
			continue
		}

		event, ok := ExtractEvent(s.Source, payload)
		if ok {
			events = append(events, event)
		}
	}
	if err := scanner.Err(); err != nil {
		return events, err
	}
	return events, nil
}

func (s *Scanner) parseJSONLRecords(path string, offset int64) ([]store.LocalUsageRecord, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, offset, err
	}
	defer file.Close()

	if offset > 0 {
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			return nil, offset, err
		}
	}

	reader := bufio.NewReaderSize(file, 256*1024)
	currentOffset := offset
	var records []store.LocalUsageRecord
	for {
		lineOffset := currentOffset
		line, err := reader.ReadString('\n')
		currentOffset += int64(len(line))
		if len(strings.TrimSpace(line)) > 0 {
			var payload any
			decoder := json.NewDecoder(strings.NewReader(line))
			decoder.UseNumber()
			if err := decoder.Decode(&payload); err == nil {
				if event, ok := ExtractEvent(s.Source, payload); ok {
					records = append(records, RecordFromEvent(s.Source, path, lineOffset, event))
				}
			}
		}
		if err == nil {
			continue
		}
		if err == io.EOF {
			break
		}
		return records, currentOffset, err
	}
	return records, currentOffset, nil
}
