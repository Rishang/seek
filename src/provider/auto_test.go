package provider

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rishang/seek/config"
)

// stub search/scrape providers for chain tests.
type fakeSearch struct {
	name    string
	results []config.SearchResult
	err     error
}

func (s fakeSearch) Name() string { return s.name }
func (s fakeSearch) Search(_ context.Context, _ string, _ config.SearchOptions) ([]config.SearchResult, error) {
	return s.results, s.err
}

type fakeScrape struct {
	name    string
	content string
	err     error
}

func (s fakeScrape) Name() string { return s.name }
func (s fakeScrape) Scrape(_ context.Context, url string, _ config.ScrapeOptions) (*config.ScrapeResult, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &config.ScrapeResult{URL: url, Content: s.content}, nil
}

func hit(n int) []config.SearchResult {
	return make([]config.SearchResult, n)
}

func TestAutoSearchFailsOverOnError(t *testing.T) {
	a := newAutoSearch([]autoSearchEntry{
		{"a", fakeSearch{name: "a", err: errors.New("boom")}},
		{"b", fakeSearch{name: "b", results: hit(2)}},
	})
	res, err := a.Search(context.Background(), "q", config.SearchOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("want 2 results, got %d", len(res))
	}
	at := a.Attempts()
	if len(at) != 2 || at[0].Err == nil || at[1].Err != nil {
		t.Fatalf("attempts: %+v", at)
	}
	if at[1].Provider != "b" {
		t.Fatalf("served by %q, want b", at[1].Provider)
	}
}

func TestAutoSearchFailsOverOnEmpty(t *testing.T) {
	a := newAutoSearch([]autoSearchEntry{
		{"a", fakeSearch{name: "a", results: hit(0)}},
		{"b", fakeSearch{name: "b", results: hit(1)}},
	})
	res, err := a.Search(context.Background(), "q", config.SearchOptions{})
	if err != nil || len(res) != 1 {
		t.Fatalf("res=%d err=%v", len(res), err)
	}
}

func TestAutoSearchAllFailAggregates(t *testing.T) {
	a := newAutoSearch([]autoSearchEntry{
		{"a", fakeSearch{name: "a", err: errors.New("timeout")}},
		{"b", fakeSearch{name: "b", results: hit(0)}},
	})
	_, err := a.Search(context.Background(), "q", config.SearchOptions{})
	if err == nil {
		t.Fatal("want error when all fail")
	}
	msg := err.Error()
	if !strings.Contains(msg, "a: timeout") || !strings.Contains(msg, "b:") {
		t.Fatalf("aggregated error missing detail: %q", msg)
	}
}

func TestAutoScrapeFailsOverOnEmptyContent(t *testing.T) {
	a := newAutoScrape([]autoScrapeEntry{
		{"a", fakeScrape{name: "a", content: ""}},
		{"b", fakeScrape{name: "b", content: "hello"}},
	})
	res, err := a.Scrape(context.Background(), "http://x", config.ScrapeOptions{})
	if err != nil || res.Content != "hello" {
		t.Fatalf("res=%v err=%v", res, err)
	}
}

func TestAutoSearchSupportsTimeRangeIfAnyMemberDoes(t *testing.T) {
	// fakeSearch does not implement TimeRangeSearcher -> false.
	a := newAutoSearch([]autoSearchEntry{{"a", fakeSearch{name: "a"}}})
	if a.SupportsTimeRange() {
		t.Error("no member supports time range; want false")
	}
}
