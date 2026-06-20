package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rishang/seek/config"
)

// auto is a meta-provider: it tries an ordered chain of real providers and
// returns the first non-empty success. A provider is skipped (failed over) on
// either an error or an empty result. When the whole chain is exhausted it
// returns an aggregated error naming every attempt.

var (
	errNoResults    = errors.New("no results")
	errEmptyContent = errors.New("empty content")
)

// ---- search ----

type autoSearchEntry struct {
	name string
	sp   SearchProvider
}

type autoSearch struct {
	chain    []autoSearchEntry
	attempts []Attempt
}

func newAutoSearch(chain []autoSearchEntry) *autoSearch { return &autoSearch{chain: chain} }

func (a *autoSearch) Name() string { return "auto" }

func (a *autoSearch) Attempts() []Attempt { return a.attempts }

// SupportsTimeRange reports true if any provider in the chain honors a time
// range, so the CLI's eager "ignored time range" warning only fires when none
// do.
func (a *autoSearch) SupportsTimeRange() bool {
	for _, e := range a.chain {
		if tr, ok := e.sp.(TimeRangeSearcher); ok && tr.SupportsTimeRange() {
			return true
		}
	}
	return false
}

func (a *autoSearch) Search(ctx context.Context, query string, opts config.SearchOptions) ([]config.SearchResult, error) {
	a.attempts = a.attempts[:0]
	for _, e := range a.chain {
		res, err := e.sp.Search(ctx, query, opts)
		if err != nil {
			a.attempts = append(a.attempts, Attempt{Provider: e.name, Err: err})
			continue
		}
		if len(res) == 0 {
			a.attempts = append(a.attempts, Attempt{Provider: e.name, Err: errNoResults})
			continue
		}
		a.attempts = append(a.attempts, Attempt{Provider: e.name})
		return res, nil
	}
	return nil, chainError("search", a.attempts)
}

// ---- scrape ----

type autoScrapeEntry struct {
	name string
	sp   ScrapeProvider
}

type autoScrape struct {
	chain    []autoScrapeEntry
	attempts []Attempt
}

func newAutoScrape(chain []autoScrapeEntry) *autoScrape { return &autoScrape{chain: chain} }

func (a *autoScrape) Name() string { return "auto" }

func (a *autoScrape) Attempts() []Attempt { return a.attempts }

func (a *autoScrape) Scrape(ctx context.Context, url string, opts config.ScrapeOptions) (*config.ScrapeResult, error) {
	a.attempts = a.attempts[:0]
	for _, e := range a.chain {
		res, err := e.sp.Scrape(ctx, url, opts)
		if err != nil {
			a.attempts = append(a.attempts, Attempt{Provider: e.name, Err: err})
			continue
		}
		if res == nil || res.Content == "" {
			a.attempts = append(a.attempts, Attempt{Provider: e.name, Err: errEmptyContent})
			continue
		}
		a.attempts = append(a.attempts, Attempt{Provider: e.name})
		return res, nil
	}
	return nil, chainError("scrape", a.attempts)
}

// chainError aggregates the failed attempts into a single descriptive error.
func chainError(op string, attempts []Attempt) error {
	parts := make([]string, 0, len(attempts))
	for _, a := range attempts {
		if a.Err != nil {
			parts = append(parts, fmt.Sprintf("%s: %v", a.Provider, a.Err))
		}
	}
	if len(parts) == 0 {
		return fmt.Errorf("auto %s: no providers available", op)
	}
	return fmt.Errorf("auto %s: %s", op, strings.Join(parts, "; "))
}

// Compile-time interface checks.
var (
	_ SearchProvider    = (*autoSearch)(nil)
	_ AutoReporter      = (*autoSearch)(nil)
	_ TimeRangeSearcher = (*autoSearch)(nil)
	_ ScrapeProvider    = (*autoScrape)(nil)
	_ AutoReporter      = (*autoScrape)(nil)
)
