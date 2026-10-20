package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"infohub/internal/localscan"
	"infohub/internal/model"
	"infohub/internal/store"
)

const (
	antigravitySource       = "antigravity"
	antigravityUserAgent    = "antigravity/hub/2.23.0 darwin/arm64"
	antigravityTokenURL     = "https://oauth2.googleapis.com/token"
	antigravityTokenRefresh = 2 * time.Minute
	antigravitySwitchMaxAge = 30 * time.Minute
)

// The daily host serves Antigravity traffic and reports live quota; the prod
// host is a fallback that may return a static 100%.
var defaultAntigravityHosts = []string{
	"https://daily-cloudcode-pa.googleapis.com",
	"https://cloudcode-pa.googleapis.com",
}

// QuotaGroup is one independently limited model pool.
type QuotaGroup struct {
	Name     string
	FiveHour localscan.QuotaObservation
	Week     localscan.QuotaObservation
}

// AntigravityQuotaClient reads the Antigravity OAuth token file and queries
// Cloud Code's quota summary. A refreshed access token is kept in memory
// only; the token file belongs to Antigravity and is never rewritten.
type AntigravityQuotaClient struct {
	cfg        AntigravityConfig
	httpClient *http.Client
	hosts      []string
	tokenURL   string
	now        func() time.Time
	runSwitch  func(ctx context.Context, args ...string) ([]byte, error)

	mu          sync.Mutex
	accessToken string
	expiresAt   time.Time
	project     string
}

type antigravityTokenFile struct {
	Token struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		Expiry       string `json:"expiry"`
	} `json:"token"`
}

func NewAntigravityQuotaClient(cfg AntigravityConfig) *AntigravityQuotaClient {
	return &AntigravityQuotaClient{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second},
		hosts:      defaultAntigravityHosts,
		tokenURL:   antigravityTokenURL,
		now:        time.Now,
	}
}

func (c *AntigravityQuotaClient) FetchGroups(ctx context.Context) ([]QuotaGroup, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	token, err := c.token(ctx)
	if err != nil {
		if groups, switchErr := c.fetchSwitchQuotaGroups(ctx); switchErr == nil && len(groups) > 0 {
			return groups, nil
		}
		return nil, err
	}
	if c.project == "" {
		if c.project, err = c.loadProject(ctx, token); err != nil {
			if groups, switchErr := c.fetchSwitchQuotaGroups(ctx); switchErr == nil && len(groups) > 0 {
				return groups, nil
			}
			return nil, err
		}
	}
	var lastErr error
	for _, host := range c.hosts {
		var summary struct {
			Groups []struct {
				DisplayName string `json:"displayName"`
				Buckets     []struct {
					Window            string   `json:"window"`
					RemainingFraction *float64 `json:"remainingFraction"`
					ResetTime         string   `json:"resetTime"`
					IsExhausted       bool     `json:"isExhausted"`
				} `json:"buckets"`
			} `json:"groups"`
		}
		if lastErr = c.call(ctx, token, host+"/v1internal:retrieveUserQuotaSummary", map[string]string{"project": c.project}, &summary); lastErr != nil {
			continue
		}
		var groups []QuotaGroup
		for _, raw := range summary.Groups {
			name := strings.TrimSpace(raw.DisplayName)
			if !c.cfg.includes(name) {
				continue
			}
			group := QuotaGroup{Name: name}
			for _, bucket := range raw.Buckets {
				// Proto JSON omits a zero fraction: a bucket without one is spent.
				remaining := 0.0
				if bucket.RemainingFraction != nil && !bucket.IsExhausted {
					remaining = *bucket.RemainingFraction
				}
				observation := localscan.QuotaObservation{OK: true, UsedPercent: clampUsed((1 - remaining) * 100), ResetAt: bucket.ResetTime}
				switch strings.ToLower(bucket.Window) {
				case "5h":
					group.FiveHour = observation
				case "weekly":
					group.Week = observation
				}
			}
			if group.FiveHour.OK || group.Week.OK {
				groups = append(groups, group)
			}
		}
		return groups, nil
	}
	if groups, switchErr := c.fetchSwitchQuotaGroups(ctx); switchErr == nil && len(groups) > 0 {
		return groups, nil
	}
	return nil, lastErr
}

type agySwitchTokenBucket struct {
	InputTokens  float64 `json:"input_tokens"`
	OutputTokens float64 `json:"output_tokens"`
	CachedTokens float64 `json:"cached_tokens"`
	TotalTokens  float64 `json:"total_tokens"`
}

func (b agySwitchTokenBucket) total() float64 {
	if b.TotalTokens > 0 {
		return b.TotalTokens
	}
	return b.InputTokens + b.OutputTokens + b.CachedTokens
}

// FetchUsageRecords reads per-day token totals from AntiGravity Switch's
// read-only CLI (`agy-switch stats --json`). When the CLI is not installed, it
// returns nil, nil so OAuth-only setups keep working without warnings.
func (c *AntigravityQuotaClient) FetchUsageRecords(ctx context.Context) ([]store.LocalUsageRecord, error) {
	output, err := c.execSwitch(ctx, "stats", "--json")
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var payload struct {
		SchemaVersion int                  `json:"schema_version"`
		Today         agySwitchTokenBucket `json:"today"`
		Daily         []struct {
			Date string `json:"date"`
			agySwitchTokenBucket
		} `json:"daily"`
	}
	if err := json.Unmarshal(output, &payload); err != nil {
		return nil, fmt.Errorf("decode agy-switch stats: %w", err)
	}
	if payload.SchemaVersion != 0 && payload.SchemaVersion != 1 {
		return nil, fmt.Errorf("unsupported agy-switch stats schema_version %d", payload.SchemaVersion)
	}

	now := c.now().In(model.ShanghaiLocation)
	today, weekStart := model.UsageDates(now)
	weekBegin, _ := time.ParseInLocation("2006-01-02", weekStart, model.ShanghaiLocation)
	cutoff := weekBegin.AddDate(0, 0, -7)

	var records []store.LocalUsageRecord
	seenToday := false
	for _, day := range payload.Daily {
		date := strings.TrimSpace(day.Date)
		dayAt, err := time.ParseInLocation("2006-01-02", date, model.ShanghaiLocation)
		if err != nil || dayAt.Before(cutoff) {
			continue
		}
		bucket := day.agySwitchTokenBucket
		if date == today {
			seenToday = true
			if payload.Today.total() > bucket.total() {
				bucket = payload.Today
			}
		}
		records = append(records, switchUsageRecord(date, dayAt, bucket))
	}
	if !seenToday && payload.Today.total() > 0 {
		if todayAt, err := time.ParseInLocation("2006-01-02", today, model.ShanghaiLocation); err == nil {
			records = append(records, switchUsageRecord(today, todayAt, payload.Today))
		}
	}
	return records, nil
}

func switchUsageRecord(date string, at time.Time, bucket agySwitchTokenBucket) store.LocalUsageRecord {
	return store.LocalUsageRecord{
		FilePath:      "agy-switch:" + date,
		ByteOffset:    0,
		At:            at,
		Input:         bucket.InputTokens,
		Output:        bucket.OutputTokens,
		CacheRead:     bucket.CachedTokens,
		Total:         bucket.total(),
		Quota5hUsed:   -1,
		QuotaWeekUsed: -1,
	}
}

func (c *AntigravityQuotaClient) fetchSwitchQuotaGroups(ctx context.Context) ([]QuotaGroup, error) {
	output, err := c.execSwitch(ctx, "quota", "--json")
	if err != nil {
		return nil, err
	}
	var payload struct {
		SchemaVersion int `json:"schema_version"`
		Quota         struct {
			LastUpdated int64 `json:"last_updated"`
			IsForbidden bool  `json:"is_forbidden"`
			QuotaGroups []struct {
				DisplayName string `json:"display_name"`
				Buckets     []struct {
					Window                 string  `json:"window"`
					RemainingFraction      float64 `json:"remaining_fraction"`
					RemainingFractionKnown bool    `json:"remaining_fraction_known"`
					ResetTime              string  `json:"reset_time"`
				} `json:"buckets"`
			} `json:"quota_groups"`
		} `json:"quota"`
	}
	if err := json.Unmarshal(output, &payload); err != nil {
		return nil, fmt.Errorf("decode agy-switch quota: %w", err)
	}
	if payload.SchemaVersion != 0 && payload.SchemaVersion != 1 {
		return nil, fmt.Errorf("unsupported agy-switch quota schema_version %d", payload.SchemaVersion)
	}
	if payload.Quota.IsForbidden {
		return nil, fmt.Errorf("agy-switch quota is forbidden")
	}
	if payload.Quota.LastUpdated > 0 && c.now().Sub(time.Unix(payload.Quota.LastUpdated, 0)) > antigravitySwitchMaxAge {
		return nil, fmt.Errorf("agy-switch quota is stale")
	}
	var groups []QuotaGroup
	for _, raw := range payload.Quota.QuotaGroups {
		name := strings.TrimSpace(raw.DisplayName)
		if !c.cfg.includes(name) {
			continue
		}
		group := QuotaGroup{Name: name}
		for _, bucket := range raw.Buckets {
			if !bucket.RemainingFractionKnown {
				continue
			}
			observation := localscan.QuotaObservation{OK: true, UsedPercent: clampUsed((1 - bucket.RemainingFraction) * 100), ResetAt: bucket.ResetTime}
			switch strings.ToLower(bucket.Window) {
			case "5h":
				group.FiveHour = observation
			case "weekly":
				group.Week = observation
			}
		}
		if group.FiveHour.OK || group.Week.OK {
			groups = append(groups, group)
		}
	}
	return groups, nil
}

func (c *AntigravityQuotaClient) execSwitch(ctx context.Context, args ...string) ([]byte, error) {
	if c.runSwitch != nil {
		return c.runSwitch(ctx, args...)
	}
	cli := strings.TrimSpace(expandHome(c.cfg.SwitchCLIPath))
	if cli == "" {
		return nil, os.ErrNotExist
	}
	if _, err := os.Stat(cli); err != nil {
		return nil, err
	}
	timeout := time.Duration(c.cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	output, err := exec.CommandContext(cmdCtx, cli, args...).Output()
	if err != nil {
		return nil, fmt.Errorf("run agy-switch %s: %w", strings.Join(args, " "), err)
	}
	return output, nil
}

func (c *AntigravityQuotaClient) token(ctx context.Context) (string, error) {
	now := c.now()
	if c.accessToken != "" && now.Add(antigravityTokenRefresh).Before(c.expiresAt) {
		return c.accessToken, nil
	}
	content, err := os.ReadFile(expandHome(c.cfg.TokenPath))
	if err != nil {
		return "", fmt.Errorf("read antigravity token: %w", err)
	}
	var file antigravityTokenFile
	if err := json.Unmarshal(content, &file); err != nil {
		return "", fmt.Errorf("decode antigravity token: %w", err)
	}
	if expiry, err := time.Parse(time.RFC3339Nano, file.Token.Expiry); err == nil && file.Token.AccessToken != "" && now.Add(antigravityTokenRefresh).Before(expiry) {
		c.accessToken, c.expiresAt = file.Token.AccessToken, expiry
		return c.accessToken, nil
	}
	if file.Token.RefreshToken == "" {
		return "", fmt.Errorf("antigravity token expired and has no refresh_token; sign in to Antigravity")
	}
	if c.cfg.ClientID == "" || c.cfg.ClientSecret == "" {
		return "", fmt.Errorf("antigravity token expired; set antigravity.client_id and client_secret to refresh it")
	}

	form := url.Values{
		"client_id":     {c.cfg.ClientID},
		"client_secret": {c.cfg.ClientSecret},
		"refresh_token": {file.Token.RefreshToken},
		"grant_type":    {"refresh_token"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var refreshed struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := c.do(req, &refreshed); err != nil {
		return "", fmt.Errorf("refresh antigravity token: %w", err)
	}
	if refreshed.AccessToken == "" {
		return "", fmt.Errorf("refresh antigravity token: empty access_token")
	}
	c.accessToken = refreshed.AccessToken
	c.expiresAt = now.Add(time.Duration(refreshed.ExpiresIn) * time.Second)
	return c.accessToken, nil
}

func (c *AntigravityQuotaClient) loadProject(ctx context.Context, token string) (string, error) {
	var payload struct {
		Project json.RawMessage `json:"cloudaicompanionProject"`
	}
	if err := c.call(ctx, token, c.hosts[0]+"/v1internal:loadCodeAssist", map[string]any{"metadata": map[string]string{"ideType": "ANTIGRAVITY"}}, &payload); err != nil {
		return "", fmt.Errorf("load antigravity project: %w", err)
	}
	var project string
	if json.Unmarshal(payload.Project, &project) != nil {
		var object struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(payload.Project, &object)
		project = object.ID
	}
	if project == "" {
		return "", fmt.Errorf("load antigravity project: response has no cloudaicompanionProject")
	}
	return project, nil
}

func (c *AntigravityQuotaClient) call(ctx context.Context, token, endpoint string, body any, target any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	// Upstream filters quota groups by client; this UA returns the Gemini groups.
	req.Header.Set("User-Agent", antigravityUserAgent)
	err = c.do(req, target)
	if err != nil && strings.Contains(err.Error(), "status 401") {
		c.accessToken = ""
	}
	return err
}

func (c *AntigravityQuotaClient) do(req *http.Request, target any) error {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Google error bodies carry no secrets; keep them short in logs.
		message := strings.TrimSpace(string(payload))
		if len(message) > 200 {
			message = message[:200]
		}
		return fmt.Errorf("%s status %d: %s", req.URL.Host, resp.StatusCode, message)
	}
	return json.Unmarshal(payload, target)
}

func clampUsed(value float64) float64 {
	return min(max(value, 0), 100)
}

func expandHome(path string) string {
	path = os.ExpandEnv(path)
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + path[1:]
		}
	}
	return path
}
