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
	"strings"
	"sync"
	"time"

	"infohub/internal/localscan"
)

const (
	antigravitySource       = "antigravity"
	antigravityUserAgent    = "antigravity/hub/2.23.0 darwin/arm64"
	antigravityTokenURL     = "https://oauth2.googleapis.com/token"
	antigravityTokenRefresh = 2 * time.Minute
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
		return nil, err
	}
	if c.project == "" {
		if c.project, err = c.loadProject(ctx, token); err != nil {
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
	return nil, lastErr
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
