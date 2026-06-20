package provider

import (
	"strings"
	"time"

	"github.com/rishang/seek/config"
)

// Date-format helpers for the various provider time-filter conventions.

// googleTBS builds a Google-style "tbs" custom-date-range value, used by
// Firecrawl and Spider. Bounds use MM/DD/YYYY; an open bound is omitted.
func googleTBS(tr config.TimeRange) string {
	if tr.IsZero() {
		return ""
	}
	parts := []string{"cdr:1"}
	if !tr.Start.IsZero() {
		parts = append(parts, "cd_min:"+tr.Start.Format("01/02/2006"))
	}
	if !tr.End.IsZero() {
		parts = append(parts, "cd_max:"+tr.End.Format("01/02/2006"))
	}
	return strings.Join(parts, ",")
}

// braveFreshness builds Brave's "YYYY-MM-DDtoYYYY-MM-DD" range. Brave requires
// both ends, so an open bound is filled with a sensible default.
func braveFreshness(tr config.TimeRange) string {
	if tr.IsZero() {
		return ""
	}
	start, end := tr.Start, tr.End
	if start.IsZero() {
		start = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	if end.IsZero() {
		end = time.Now()
	}
	return start.Format("2006-01-02") + "to" + end.Format("2006-01-02")
}

// ymd formats a date as YYYY-MM-DD (Tavily), returning "" for a zero time.
func ymd(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}

// iso8601 formats a time as ISO 8601 with millis in UTC (Exa), returning "" for
// a zero time.
func iso8601(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}
