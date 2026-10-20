package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAntigravityQuotaClientRefreshesAndFilters(t *testing.T) {
	now := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	var refreshes, summaries int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			_ = r.ParseForm()
			if r.Form.Get("refresh_token") != "rt" || r.Form.Get("grant_type") != "refresh_token" {
				t.Errorf("bad refresh form: %v", r.Form)
			}
			refreshes++
			_, _ = w.Write([]byte(`{"access_token":"fresh","expires_in":3600}`))
		case r.Header.Get("Authorization") != "Bearer fresh":
			w.WriteHeader(http.StatusUnauthorized)
		case strings.HasSuffix(r.URL.Path, ":loadCodeAssist"):
			_, _ = w.Write([]byte(`{"cloudaicompanionProject":"proj-1"}`))
		case strings.HasPrefix(r.URL.Path, "/daily/") && strings.HasSuffix(r.URL.Path, ":retrieveUserQuotaSummary"):
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["project"] != "proj-1" {
				t.Errorf("project = %q", body["project"])
			}
			summaries++
			_, _ = w.Write([]byte(`{"groups":[
				{"displayName":"Gemini Models","buckets":[{"window":"weekly","remainingFraction":0.75,"resetTime":"2026-10-26T08:58:54Z"},{"window":"5h","resetTime":"2026-10-20T13:31:08Z"}]},
				{"displayName":"Claude and GPT models","buckets":[{"window":"5h","remainingFraction":1}]},
				{"displayName":"Gemini 4 Argon","buckets":[{"window":"weekly","remainingFraction":0.5}]}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	tokenPath := filepath.Join(t.TempDir(), "token.json")
	original := `{"token":{"access_token":"old","refresh_token":"rt","expiry":"2026-10-20T07:00:00Z"}}`
	if err := os.WriteFile(tokenPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	filter := "gemini"
	client := NewAntigravityQuotaClient(AntigravityConfig{TokenPath: tokenPath, TimeoutSeconds: 5, GroupFilter: &filter, ClientID: "cid", ClientSecret: "csecret"})
	client.hosts = []string{server.URL + "/daily", server.URL + "/prod"}
	client.tokenURL = server.URL + "/token"
	client.now = func() time.Time { return now }

	groups, err := client.FetchGroups(context.Background())
	if err != nil {
		t.Fatalf("FetchGroups: %v", err)
	}
	if len(groups) != 2 || groups[0].Name != "Gemini Models" || groups[1].Name != "Gemini 4 Argon" {
		t.Fatalf("groups = %+v", groups)
	}
	if groups[0].Week.UsedPercent != 25 || !groups[0].FiveHour.OK || groups[0].FiveHour.UsedPercent != 100 || groups[1].FiveHour.OK {
		t.Fatalf("windows = %+v", groups)
	}
	if _, err := client.FetchGroups(context.Background()); err != nil {
		t.Fatalf("second FetchGroups: %v", err)
	}
	if refreshes != 1 || summaries != 2 {
		t.Fatalf("refreshes=%d summaries=%d", refreshes, summaries)
	}
	if content, _ := os.ReadFile(tokenPath); string(content) != original {
		t.Fatal("token file was rewritten")
	}
}

func TestAntigravitySwitchStatsAndQuotaFallback(t *testing.T) {
	now := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	filter := "gemini"
	client := NewAntigravityQuotaClient(AntigravityConfig{
		TokenPath:   filepath.Join(t.TempDir(), "missing-token.json"),
		GroupFilter: &filter,
	})
	client.now = func() time.Time { return now }
	client.runSwitch = func(ctx context.Context, args ...string) ([]byte, error) {
		switch args[0] {
		case "stats":
			return []byte(`{
				"today":{"input_tokens":900,"output_tokens":100,"cached_tokens":2000,"total_tokens":3000},
				"daily":[
					{"date":"2026-09-01","total_tokens":999999},
					{"date":"2026-10-19","input_tokens":400,"output_tokens":50,"cached_tokens":550,"total_tokens":1000},
					{"date":"2026-10-20","input_tokens":800,"output_tokens":100,"cached_tokens":1600,"total_tokens":2500}
				]
			}`), nil
		case "quota":
			return []byte(`{
				"schema_version":1,
				"quota":{
					"last_updated":1792490000,
					"is_forbidden":false,
					"quota_groups":[
						{"display_name":"Gemini Models","buckets":[
							{"window":"5h","remaining_fraction":0.9,"remaining_fraction_known":true,"reset_time":"2026-10-20T15:30:16Z"},
							{"window":"weekly","remaining_fraction":0.7,"remaining_fraction_known":true,"reset_time":"2026-10-27T10:30:16Z"}
						]},
						{"display_name":"Claude and GPT models","buckets":[
							{"window":"5h","remaining_fraction":1.0,"remaining_fraction_known":true}
						]}
					]
				}
			}`), nil
		default:
			t.Fatalf("unexpected args: %v", args)
			return nil, nil
		}
	}

	records, err := client.FetchUsageRecords(context.Background())
	if err != nil || len(records) != 2 {
		t.Fatalf("records = %+v, err = %v", records, err)
	}
	if records[0].FilePath != "agy-switch:2026-10-19" || records[0].Total != 1000 || records[1].FilePath != "agy-switch:2026-10-20" || records[1].Total != 3000 {
		t.Fatalf("unexpected records: %+v", records)
	}

	groups, err := client.FetchGroups(context.Background())
	if err != nil || len(groups) != 1 || groups[0].Name != "Gemini Models" || int(groups[0].FiveHour.UsedPercent+0.5) != 10 || int(groups[0].Week.UsedPercent+0.5) != 30 {
		t.Fatalf("fallback groups = %+v, err = %v", groups, err)
	}
}
