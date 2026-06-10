package localscan

import (
	"strings"
	"time"

	"infohub/internal/store"
)

const (
	SourceClaude = "claude_local"
	SourceCodex  = "codex_local"

	claudeParserVersion = 1
	codexParserVersion  = 1
)

// ParserVersion reports the JSONL parser version for a local usage source.
// Bump the per-source constant to force a full re-parse of tracked files.
func ParserVersion(source string) int {
	if source == SourceCodex {
		return codexParserVersion
	}
	return claudeParserVersion
}

type Event struct {
	At            time.Time
	Model         string
	Input         float64
	Output        float64
	CacheRead     float64
	CacheCreation float64
	Reasoning     float64
	Total         float64
	Quota         RateLimits
}

func (e Event) TotalTokens() float64 {
	if e.Total > 0 {
		return e.Total
	}
	return e.Input + e.Output + e.CacheRead + e.CacheCreation + e.Reasoning
}

type QuotaObservation struct {
	OK          bool
	UsedPercent float64
	ResetAt     string
}

type RateLimits struct {
	FiveHour QuotaObservation
	Week     QuotaObservation
}

func (q RateLimits) HasAny() bool {
	return q.FiveHour.OK || q.Week.OK
}

func (q RateLimits) ForWindow(window string) QuotaObservation {
	switch window {
	case "5H":
		return q.FiveHour
	case "Week":
		return q.Week
	default:
		return QuotaObservation{}
	}
}

func DiscardExpired(limits RateLimits, now time.Time) RateLimits {
	if quotaObservationExpired(limits.FiveHour, now) {
		limits.FiveHour = QuotaObservation{}
	}
	if quotaObservationExpired(limits.Week, now) {
		limits.Week = QuotaObservation{}
	}
	return limits
}

func quotaObservationExpired(observation QuotaObservation, now time.Time) bool {
	if !observation.OK || strings.TrimSpace(observation.ResetAt) == "" {
		return false
	}
	resetAt, ok := ParseEventTime(observation.ResetAt)
	return ok && !resetAt.After(now)
}

func QuotaUsedOrMissing(observation QuotaObservation) float64 {
	if !observation.OK {
		return -1
	}
	return observation.UsedPercent
}

// EventFromRecord rebuilds an Event from a persisted usage record.
func EventFromRecord(record store.LocalUsageRecord) Event {
	return Event{
		At:            record.At,
		Model:         record.Model,
		Input:         record.Input,
		Output:        record.Output,
		CacheRead:     record.CacheRead,
		CacheCreation: record.CacheCreation,
		Reasoning:     record.Reasoning,
		Total:         record.Total,
		Quota: RateLimits{
			FiveHour: QuotaObservation{
				OK:          record.Quota5hUsed >= 0,
				UsedPercent: record.Quota5hUsed,
				ResetAt:     record.Quota5hReset,
			},
			Week: QuotaObservation{
				OK:          record.QuotaWeekUsed >= 0,
				UsedPercent: record.QuotaWeekUsed,
				ResetAt:     record.QuotaWeekReset,
			},
		},
	}
}

// RecordFromEvent flattens an Event into a persisted usage record keyed by
// its position in the source JSONL file.
func RecordFromEvent(source, filePath string, byteOffset int64, event Event) store.LocalUsageRecord {
	return store.LocalUsageRecord{
		Source:         source,
		FilePath:       filePath,
		ByteOffset:     byteOffset,
		At:             event.At,
		Model:          event.Model,
		Input:          event.Input,
		Output:         event.Output,
		CacheRead:      event.CacheRead,
		CacheCreation:  event.CacheCreation,
		Reasoning:      event.Reasoning,
		Total:          event.Total,
		Quota5hUsed:    QuotaUsedOrMissing(event.Quota.FiveHour),
		Quota5hReset:   event.Quota.FiveHour.ResetAt,
		QuotaWeekUsed:  QuotaUsedOrMissing(event.Quota.Week),
		QuotaWeekReset: event.Quota.Week.ResetAt,
	}
}
