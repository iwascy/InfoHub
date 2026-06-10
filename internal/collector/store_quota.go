package collector

import (
	"context"
	"time"

	"infohub/internal/localscan"
	"infohub/internal/store"
)

// StoreQuotaFetcher serves rate limits from the latest agent-reported quota
// observation instead of calling an online API. It backs remote-mode local
// collectors on servers that hold no Claude/Codex credentials themselves.
type StoreQuotaFetcher struct {
	store      store.IngestStore
	source     string
	staleAfter time.Duration
	now        func() time.Time
	lastStatus string
}

func NewStoreQuotaFetcher(ingestStore store.IngestStore, source string, staleAfter time.Duration) *StoreQuotaFetcher {
	return &StoreQuotaFetcher{
		store:      ingestStore,
		source:     source,
		staleAfter: staleAfter,
		now:        time.Now,
		lastStatus: "no_observation",
	}
}

func (f *StoreQuotaFetcher) FetchRateLimits(ctx context.Context) (localRateLimits, bool, error) {
	obs, ok, err := f.store.LatestAgentQuotaObservation(f.source)
	if err != nil {
		f.lastStatus = "store_error"
		return localRateLimits{}, false, err
	}
	if !ok {
		f.lastStatus = "no_observation"
		return localRateLimits{}, false, nil
	}
	if f.staleAfter > 0 && f.now().Sub(obs.ObservedAt) > f.staleAfter {
		f.lastStatus = "stale"
		return localRateLimits{}, false, nil
	}

	limits := localRateLimits{
		FiveHour: localQuotaObservation{
			OK:          obs.Quota5hUsed >= 0,
			UsedPercent: obs.Quota5hUsed,
			ResetAt:     obs.Quota5hReset,
		},
		Week: localQuotaObservation{
			OK:          obs.QuotaWeekUsed >= 0,
			UsedPercent: obs.QuotaWeekUsed,
			ResetAt:     obs.QuotaWeekReset,
		},
	}
	limits = localscan.DiscardExpired(limits, f.now())
	if !limits.HasAny() {
		f.lastStatus = "no_observation"
		return localRateLimits{}, false, nil
	}
	f.lastStatus = "ok"
	return limits, true, nil
}

func (f *StoreQuotaFetcher) LastStatus() string {
	return f.lastStatus
}
