package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"infohub/internal/store"
)

const (
	maxIngestRecords  = 5000
	maxIngestBodySize = 16 << 20
)

type ingestRecord struct {
	FilePath       string   `json:"file_path"`
	ByteOffset     int64    `json:"byte_offset"`
	At             string   `json:"at"`
	Model          string   `json:"model"`
	Input          float64  `json:"input"`
	Output         float64  `json:"output"`
	CacheRead      float64  `json:"cache_read"`
	CacheCreation  float64  `json:"cache_creation"`
	Reasoning      float64  `json:"reasoning"`
	Total          float64  `json:"total"`
	Quota5hUsed    *float64 `json:"quota_5h_used"`
	Quota5hReset   string   `json:"quota_5h_reset"`
	QuotaWeekUsed  *float64 `json:"quota_week_used"`
	QuotaWeekReset string   `json:"quota_week_reset"`
}

type ingestQuotaWindow struct {
	UsedPercent float64 `json:"used_percent"`
	ResetAt     string  `json:"reset_at"`
}

type ingestQuota struct {
	FiveHour *ingestQuotaWindow `json:"five_hour"`
	Week     *ingestQuotaWindow `json:"week"`
}

type ingestRequest struct {
	MachineID    string         `json:"machine_id"`
	AgentVersion string         `json:"agent_version"`
	Source       string         `json:"source"`
	ResetFiles   []string       `json:"reset_files"`
	Records      []ingestRecord `json:"records"`
	Quota        *ingestQuota   `json:"quota"`
}

func (h *Handler) IngestLocalUsage(w http.ResponseWriter, r *http.Request) {
	ingestStore, ok := h.store.(store.IngestStore)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store does not support ingest (sqlite required)"})
		return
	}

	var req ingestRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxIngestBodySize))
	if err := decoder.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return
	}

	machine := strings.TrimSpace(req.MachineID)
	if machine == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "machine_id is required"})
		return
	}
	source := strings.TrimSpace(req.Source)
	if source != "claude_local" && source != "codex_local" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "source must be claude_local or codex_local"})
		return
	}
	if len(req.Records) > maxIngestRecords {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "too many records in one request"})
		return
	}

	records := make([]store.LocalUsageRecord, 0, len(req.Records))
	for index, raw := range req.Records {
		record, err := raw.toStoreRecord()
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "record_index": index})
			return
		}
		records = append(records, record)
	}

	if err := ingestStore.SaveIngestedUsage(machine, source, req.ResetFiles, records); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	if req.Quota != nil && (req.Quota.FiveHour != nil || req.Quota.Week != nil) {
		obs := store.AgentQuotaObservation{
			Machine:       machine,
			Source:        source,
			Quota5hUsed:   -1,
			QuotaWeekUsed: -1,
			ObservedAt:    time.Now(),
			AgentVersion:  strings.TrimSpace(req.AgentVersion),
		}
		if window := req.Quota.FiveHour; window != nil {
			obs.Quota5hUsed = window.UsedPercent
			obs.Quota5hReset = strings.TrimSpace(window.ResetAt)
		}
		if window := req.Quota.Week; window != nil {
			obs.QuotaWeekUsed = window.UsedPercent
			obs.QuotaWeekReset = strings.TrimSpace(window.ResetAt)
		}
		if err := ingestStore.SaveAgentQuotaObservation(obs); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}

	// Best-effort snapshot refresh so the dashboard reflects the push without
	// waiting for the next cron tick; failures (e.g. collector disabled) are
	// reported in the response but do not fail the ingest.
	refreshed := false
	if h.scheduler != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		refreshed = h.scheduler.TriggerNow(ctx, source) == nil
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "ok",
		"accepted":  len(records),
		"refreshed": refreshed,
	})
}

func (raw ingestRecord) toStoreRecord() (store.LocalUsageRecord, error) {
	filePath := strings.TrimSpace(raw.FilePath)
	if filePath == "" {
		return store.LocalUsageRecord{}, errInvalidRecord("file_path is required")
	}
	if raw.ByteOffset < 0 {
		return store.LocalUsageRecord{}, errInvalidRecord("byte_offset must be >= 0")
	}
	at, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw.At))
	if err != nil {
		return store.LocalUsageRecord{}, errInvalidRecord("at must be RFC3339: " + err.Error())
	}

	record := store.LocalUsageRecord{
		FilePath:       filePath,
		ByteOffset:     raw.ByteOffset,
		At:             at,
		Model:          strings.TrimSpace(raw.Model),
		Input:          raw.Input,
		Output:         raw.Output,
		CacheRead:      raw.CacheRead,
		CacheCreation:  raw.CacheCreation,
		Reasoning:      raw.Reasoning,
		Total:          raw.Total,
		Quota5hUsed:    -1,
		Quota5hReset:   strings.TrimSpace(raw.Quota5hReset),
		QuotaWeekUsed:  -1,
		QuotaWeekReset: strings.TrimSpace(raw.QuotaWeekReset),
	}
	if raw.Quota5hUsed != nil {
		record.Quota5hUsed = *raw.Quota5hUsed
	}
	if raw.QuotaWeekUsed != nil {
		record.QuotaWeekUsed = *raw.QuotaWeekUsed
	}
	return record, nil
}

type errInvalidRecord string

func (e errInvalidRecord) Error() string {
	return string(e)
}
