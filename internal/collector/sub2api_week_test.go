package collector

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"infohub/internal/config"
)

func TestSub2APIWeeklyUsage(t *testing.T) {
	for _, tc := range []struct {
		name, scope, payload, want string
		status                     int
	}{
		{"user products", "user_id", `{"data":{"upstream_endpoints":[{"endpoint":"/v1/messages","total_tokens":90000},{"endpoint":"/v1/responses","total_tokens":12000}]}}`, "12000", 200},
		{"user zero", "user_id", `{"upstream_endpoints":[]}`, "0", 200},
		{"user missing breakdown", "user_id", `{"total_tokens":12000}`, "--", 200},
		{"malformed codex", "user_id", `{"upstream_endpoints":[{"endpoint":"/v1/responses"}]}`, "--", 200},
		{"account", "account_id", `{"data":{"total_tokens":42000}}`, "42000", 200},
		{"account zero", "account_id", `{"total_tokens":0}`, "0", 200},
		{"account missing", "account_id", `{}`, "--", 200},
		{"failure", "user_id", `{"error":"unavailable"}`, "--", 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				expectQueryValues(t, r.URL.Query(), map[string]string{tc.scope: "88", "period": "week", "timezone": "Asia/Shanghai", "start_date": "2026-04-20", "end_date": "2026-04-24"})
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.payload))
			}))
			defer server.Close()
			c := NewSub2APICollector(config.HTTPCollectorConfig{Service: config.HTTPServiceConfig{BaseURL: server.URL, Endpoints: map[string]string{"usage_stats": "/stats"}}}, nil)
			session, err := c.service.newSession(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			item := c.fetchCodexWeekUsage(context.Background(), session, tc.scope, "88", "2026-04-20", "2026-04-24")
			if item.Value != tc.want {
				t.Fatalf("got %+v, want %s", item, tc.want)
			}
			if tc.want == "--" && item.Extra["error"] == nil {
				t.Fatal("missing failure context")
			}
		})
	}
}

func TestSub2APIWeeklyFailureKeepsDailyAndQuota(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/accounts":
			writeJSON(t, w, map[string]any{"data": map[string]any{"items": []any{map[string]any{"id": 1, "name": "Pro 20x", "credentials": map[string]any{"email": "cycyzg@gmail.com", "access_token": "never-copy-this"}, "extra": map[string]any{"codex_5h_used_percent": 25, "codex_7d_used_percent": 30}}}}})
		case "/today":
			writeJSON(t, w, map[string]any{"data": map[string]any{"stats": map[string]any{"1": map[string]any{"tokens": 100, "requests": 2}}}})
		case "/stats":
			http.Error(w, "temporarily unavailable", 503)
		}
	}))
	defer server.Close()
	c := NewSub2APICollector(config.HTTPCollectorConfig{Service: config.HTTPServiceConfig{BaseURL: server.URL, Endpoints: map[string]string{"accounts": "/accounts", "today_stats": "/today", "usage_stats": "/stats"}}}, nil)
	items, err := c.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if mustFindItem(t, items, "今日 Token 用量").Value != "100" {
		t.Fatal("daily usage lost")
	}
	if mustFindItem(t, items, "Codex 本周 Token 用量").Value != "--" {
		t.Fatal("weekly failure should be unknown")
	}
	quota := mustFindItem(t, items, "账号 Pro 20x 5H 额度")
	if quota.Extra["email"] != "cycyzg@gmail.com" || quota.Extra["access_token"] != nil {
		t.Fatalf("bad account metadata: %+v", quota)
	}
}

func TestSub2APIAccountTargetMatchesCredentialEmail(t *testing.T) {
	account := newSub2APIAccountQuota(map[string]any{"id": 1, "name": "Pro 20x", "credentials": map[string]any{"email": "cycyzg@gmail.com"}})
	if !sub2apiAccountMatchesTarget(account, config.Sub2APITarget{Type: "account", Email: "CYCYZG@gmail.com"}) {
		t.Fatal("credential email was not matched")
	}
	if sub2apiAccountMatchesTarget(account, config.Sub2APITarget{Type: "account", Email: "cycyz@gmail.com"}) {
		t.Fatal("must use exact email")
	}
}
