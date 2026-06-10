package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"infohub/internal/localscan"
	"infohub/internal/store"
)

type Client struct {
	baseURL    string
	token      string
	machineID  string
	version    string
	httpClient *http.Client
}

func NewClient(cfg ServerConfig, machineID, version string) *Client {
	return &Client{
		baseURL:    strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"),
		token:      strings.TrimSpace(cfg.IngestToken),
		machineID:  machineID,
		version:    version,
		httpClient: &http.Client{Timeout: cfg.Timeout()},
	}
}

type pushRecord struct {
	FilePath       string  `json:"file_path"`
	ByteOffset     int64   `json:"byte_offset"`
	At             string  `json:"at"`
	Model          string  `json:"model,omitempty"`
	Input          float64 `json:"input"`
	Output         float64 `json:"output"`
	CacheRead      float64 `json:"cache_read"`
	CacheCreation  float64 `json:"cache_creation"`
	Reasoning      float64 `json:"reasoning"`
	Total          float64 `json:"total"`
	Quota5hUsed    float64 `json:"quota_5h_used"`
	Quota5hReset   string  `json:"quota_5h_reset,omitempty"`
	QuotaWeekUsed  float64 `json:"quota_week_used"`
	QuotaWeekReset string  `json:"quota_week_reset,omitempty"`
}

type pushQuotaWindow struct {
	UsedPercent float64 `json:"used_percent"`
	ResetAt     string  `json:"reset_at,omitempty"`
}

type pushQuota struct {
	FiveHour *pushQuotaWindow `json:"five_hour,omitempty"`
	Week     *pushQuotaWindow `json:"week,omitempty"`
}

type pushRequest struct {
	MachineID    string       `json:"machine_id"`
	AgentVersion string       `json:"agent_version"`
	Source       string       `json:"source"`
	ResetFiles   []string     `json:"reset_files,omitempty"`
	Records      []pushRecord `json:"records"`
	Quota        *pushQuota   `json:"quota,omitempty"`
}

// PushUsage uploads one batch. Any non-2xx response is an error so callers
// know not to advance their local parse state.
func (c *Client) PushUsage(ctx context.Context, source string, resetFiles []string, records []store.LocalUsageRecord, quota *localscan.RateLimits) error {
	request := pushRequest{
		MachineID:    c.machineID,
		AgentVersion: c.version,
		Source:       source,
		ResetFiles:   resetFiles,
		Records:      make([]pushRecord, 0, len(records)),
	}
	for _, record := range records {
		request.Records = append(request.Records, pushRecord{
			FilePath:       record.FilePath,
			ByteOffset:     record.ByteOffset,
			At:             record.At.UTC().Format(time.RFC3339Nano),
			Model:          record.Model,
			Input:          record.Input,
			Output:         record.Output,
			CacheRead:      record.CacheRead,
			CacheCreation:  record.CacheCreation,
			Reasoning:      record.Reasoning,
			Total:          record.Total,
			Quota5hUsed:    record.Quota5hUsed,
			Quota5hReset:   record.Quota5hReset,
			QuotaWeekUsed:  record.QuotaWeekUsed,
			QuotaWeekReset: record.QuotaWeekReset,
		})
	}
	if quota != nil {
		converted := pushQuota{}
		if quota.FiveHour.OK {
			converted.FiveHour = &pushQuotaWindow{UsedPercent: quota.FiveHour.UsedPercent, ResetAt: quota.FiveHour.ResetAt}
		}
		if quota.Week.OK {
			converted.Week = &pushQuotaWindow{UsedPercent: quota.Week.UsedPercent, ResetAt: quota.Week.ResetAt}
		}
		if converted.FiveHour != nil || converted.Week != nil {
			request.Quota = &converted
		}
	}

	payload, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode push request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/ingest/local-usage", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create push request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("push usage: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("push usage: server returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}
