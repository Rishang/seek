package provider

import (
	"testing"
	"time"

	"github.com/rishang/seek/config"
)

func TestGoogleTBS(t *testing.T) {
	start := time.Date(2000, 12, 30, 0, 0, 0, 0, time.UTC)
	end := time.Date(2001, 1, 5, 0, 0, 0, 0, time.UTC)

	if got := googleTBS(config.TimeRange{}); got != "" {
		t.Fatalf("empty range: want \"\", got %q", got)
	}
	if got := googleTBS(config.TimeRange{Start: start, End: end}); got != "cdr:1,cd_min:12/30/2000,cd_max:01/05/2001" {
		t.Fatalf("full range: got %q", got)
	}
	if got := googleTBS(config.TimeRange{Start: start}); got != "cdr:1,cd_min:12/30/2000" {
		t.Fatalf("open end: got %q", got)
	}
}

func TestBraveFreshness(t *testing.T) {
	start := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 3, 4, 0, 0, 0, 0, time.UTC)
	if got := braveFreshness(config.TimeRange{Start: start, End: end}); got != "2024-01-02to2024-03-04" {
		t.Fatalf("got %q", got)
	}
	if got := braveFreshness(config.TimeRange{}); got != "" {
		t.Fatalf("empty: got %q", got)
	}
}

func TestYMDAndISO(t *testing.T) {
	d := time.Date(2024, 1, 2, 15, 4, 5, 0, time.UTC)
	if got := ymd(d); got != "2024-01-02" {
		t.Fatalf("ymd: got %q", got)
	}
	if got := iso8601(d); got != "2024-01-02T15:04:05.000Z" {
		t.Fatalf("iso8601: got %q", got)
	}
	if ymd(time.Time{}) != "" || iso8601(time.Time{}) != "" {
		t.Fatal("zero time should format to empty string")
	}
}
