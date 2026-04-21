package api

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

func withLogging(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedAt := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(recorder, r)

		logger.Info("http request completed",
			"method", r.Method,
			"path", r.URL.Path,
			"status", recorder.statusCode,
			"duration_ms", time.Since(startedAt).Milliseconds(),
		)
	})
}

func withAuth(next http.Handler, token string) http.Handler {
	if strings.TrimSpace(token) == "" {
		return next
	}

	expected := "Bearer " + strings.TrimSpace(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/dashboard/eink" {
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("Authorization") != expected {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func withDashboardAccess(next http.Handler, authToken string, dashboardToken string) http.Handler {
	authToken = strings.TrimSpace(authToken)
	dashboardToken = strings.TrimSpace(dashboardToken)
	expectedAuth := "Bearer " + authToken

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authToken == "" && dashboardToken == "" {
			next.ServeHTTP(w, r)
			return
		}

		if authToken != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte(expectedAuth)) == 1 {
			next.ServeHTTP(w, r)
			return
		}

		if dashboardToken != "" && subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("token")), []byte(dashboardToken)) == 1 {
			next.ServeHTTP(w, r)
			return
		}

		writeDashboardUnauthorized(w)
	})
}

func writeDashboardUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>Unauthorized</title><body style="font-family:sans-serif;padding:24px">dashboard token required</body></html>`))
}

type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (r *statusRecorder) WriteHeader(statusCode int) {
	r.statusCode = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}
