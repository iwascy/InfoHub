package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"infohub/internal/collector"
	"infohub/internal/localscan"
	"infohub/internal/store"
)

const pushChunkSize = 1000

type quotaFetcher interface {
	FetchRateLimits(context.Context) (localscan.RateLimits, bool, error)
}

type Runner struct {
	cfg           Config
	logger        *slog.Logger
	client        *Client
	quotaFetchers map[string]quotaFetcher
	now           func() time.Time
}

func NewRunner(cfg Config, version string, logger *slog.Logger) *Runner {
	runner := &Runner{
		cfg:           cfg,
		logger:        logger,
		client:        NewClient(cfg.Server, cfg.MachineID, version),
		quotaFetchers: map[string]quotaFetcher{},
		now:           time.Now,
	}
	if cfg.ClaudeQuota.Enabled {
		runner.quotaFetchers["claude_local"] = collector.NewClaudeOnlineQuotaClient(cfg.ClaudeQuota, logger)
	}
	if cfg.CodexQuota.Enabled {
		runner.quotaFetchers["codex_local"] = collector.NewCodexOnlineQuotaClient(cfg.CodexQuota, logger)
	}
	return runner
}

// RunOnce scans every enabled source incrementally and pushes the new
// records. Parse state is only persisted for sources whose pushes all
// succeeded, so failed uploads are retried on the next run.
func (r *Runner) RunOnce(ctx context.Context) error {
	state, err := loadState(r.cfg.StatePath)
	if err != nil {
		return err
	}

	var (
		dirty    bool
		failures []string
	)
	for _, source := range []string{"claude_local", "codex_local"} {
		sourceCfg, ok := r.cfg.Sources[source]
		if !ok || !sourceCfg.Enabled {
			continue
		}
		if err := r.runSource(ctx, state, source, sourceCfg); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", source, err))
			r.logger.Warn("agent source push failed", "source", source, "error", err)
			continue
		}
		dirty = true
	}

	if dirty {
		if err := state.save(r.cfg.StatePath); err != nil {
			return err
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("agent run finished with errors: %s", strings.Join(failures, "; "))
	}
	return nil
}

func (r *Runner) runSource(ctx context.Context, state *stateFile, source string, sourceCfg SourceConfig) error {
	scanner := &localscan.Scanner{
		Source: source,
		Paths:  sourceCfg.Paths,
		Logger: r.logger,
		Now:    r.now,
	}

	nextStates, records, err := scanner.ScanIncremental(ctx, state.parseStates(source))
	if err != nil {
		return err
	}

	var resetFiles []string
	for _, next := range nextStates {
		if next.Reset {
			resetFiles = append(resetFiles, next.FilePath)
		}
	}

	quota := r.fetchQuota(ctx, source)

	if len(records) == 0 && len(resetFiles) == 0 && quota == nil {
		r.logger.Debug("agent source up to date", "source", source)
		return nil
	}

	// First chunk carries reset_files and the quota observation; an empty
	// records push still refreshes the quota when the machine is idle.
	chunks := chunkRecords(records, pushChunkSize)
	if len(chunks) == 0 {
		chunks = [][]store.LocalUsageRecord{nil}
	}
	for index, chunk := range chunks {
		var (
			chunkResets []string
			chunkQuota  *localscan.RateLimits
		)
		if index == 0 {
			chunkResets = resetFiles
			chunkQuota = quota
		}
		if err := r.client.PushUsage(ctx, source, chunkResets, chunk, chunkQuota); err != nil {
			return err
		}
	}

	state.apply(source, nextStates)
	r.logger.Info("agent push complete", "source", source, "records", len(records), "reset_files", len(resetFiles), "quota_attached", quota != nil)
	return nil
}

func (r *Runner) fetchQuota(ctx context.Context, source string) *localscan.RateLimits {
	fetcher := r.quotaFetchers[source]
	if fetcher == nil {
		return nil
	}
	limits, ok, err := fetcher.FetchRateLimits(ctx)
	if err != nil {
		r.logger.Warn("online quota fetch failed", "source", source, "error", err)
		return nil
	}
	if !ok || !limits.HasAny() {
		return nil
	}
	return &limits
}

func chunkRecords(records []store.LocalUsageRecord, size int) [][]store.LocalUsageRecord {
	if len(records) == 0 {
		return nil
	}
	chunks := make([][]store.LocalUsageRecord, 0, (len(records)+size-1)/size)
	for start := 0; start < len(records); start += size {
		end := min(start+size, len(records))
		chunks = append(chunks, records[start:end])
	}
	return chunks
}
