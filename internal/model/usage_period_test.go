package model

import (
	"testing"
	"time"
)

func TestUsageDates(t *testing.T) {
	for _, tc := range []struct{ now, today, monday string }{
		{"2026-09-06T16:00:00Z", "2026-09-07", "2026-09-07"},
		{"2026-09-06T15:59:59Z", "2026-09-06", "2026-08-31"},
		{"2026-01-01T12:00:00Z", "2026-01-01", "2025-12-29"},
	} {
		now, _ := time.Parse(time.RFC3339, tc.now)
		today, monday := UsageDates(now)
		if today != tc.today || monday != tc.monday {
			t.Fatalf("%s: %s %s", tc.now, today, monday)
		}
	}
}
