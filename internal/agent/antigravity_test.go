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
