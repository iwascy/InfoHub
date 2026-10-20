package model

import "time"

var ShanghaiLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

// UsageDates uses calendar weeks, not a rolling seven-day quota window.
func UsageDates(now time.Time) (today, weekStart string) {
	now = now.In(ShanghaiLocation)
	daysSinceMonday := (int(now.Weekday()) + 6) % 7
	return now.Format("2006-01-02"), now.AddDate(0, 0, -daysSinceMonday).Format("2006-01-02")
}
