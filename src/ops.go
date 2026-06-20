package main

import (
	"context"
	"fmt"
	"time"

	"github.com/rishang/seek/config"
	"github.com/rishang/seek/logx"
)

// This file holds the provider-operation runners shared by the CLI commands,
// the HTTP server (serve), and the MCP server (mcp). Each resolves the provider
// name (falling back to the operation's configured default), runs the request
// against the factory, and surfaces auto-failover attempts to the logger.
//
// The factory is built once in main() and only read afterwards, so these
// runners are safe to call concurrently (each "auto" call gets its own chain
// instance). ponytail: relies on the factory being read-only post-startup; if a
// future command mutates it at runtime, guard it with a mutex.

// opSearch runs a search. An empty providerName uses the configured default.
func opSearch(ctx context.Context, providerName, query string, opts config.SearchOptions) ([]config.SearchResult, error) {
	if providerName == "" {
		providerName = cfg.Search.Provider
	}
	logx.Debug("op: search provider=%s query=%q range=%s", providerName, query, fmtRange(opts.TimeRange))
	sp, err := factory.Search(providerName)
	if err != nil {
		return nil, err
	}
	results, err := sp.Search(ctx, query, opts)
	logAutoAttempts(sp)
	if err != nil {
		logx.Debug("op: search failed: %v", err)
		return nil, err
	}
	logx.Debug("op: search ok, %d result(s)", len(results))
	return results, nil
}

// opScrape runs a scrape. An empty providerName uses the configured default;
// an empty format uses the configured scrape output format.
func opScrape(ctx context.Context, providerName, url, format string) (*config.ScrapeResult, error) {
	if providerName == "" {
		providerName = cfg.Scrape.Provider
	}
	outFormat := cfg.Scrape.Options.OutputFormat
	if format != "" {
		outFormat = parseFormat(format)
	}
	logx.Debug("op: scrape provider=%s url=%q format=%s", providerName, url, outFormat)
	sp, err := factory.Scrape(providerName)
	if err != nil {
		return nil, err
	}
	result, err := sp.Scrape(ctx, url, config.ScrapeOptions{OutputFormat: outFormat})
	logAutoAttempts(sp)
	if err != nil {
		logx.Debug("op: scrape failed: %v", err)
		return nil, err
	}
	logx.Debug("op: scrape ok, %d bytes", len(result.Content))
	return result, nil
}

// opCrawl runs a crawl. An empty providerName uses the configured default.
func opCrawl(ctx context.Context, providerName, url string) (*config.CrawlResult, error) {
	if providerName == "" {
		providerName = cfg.Crawl.Provider
	}
	logx.Debug("op: crawl provider=%s url=%q", providerName, url)
	cp, err := factory.Crawl(providerName)
	if err != nil {
		return nil, err
	}
	result, err := cp.Crawl(ctx, url)
	if err != nil {
		logx.Debug("op: crawl failed: %v", err)
		return nil, err
	}
	logx.Debug("op: crawl ok, %d page(s)", len(result.Pages))
	return result, nil
}

// fmtRange renders a time range compactly for debug logs.
func fmtRange(tr config.TimeRange) string {
	if tr.IsZero() {
		return "none"
	}
	return fmtDateOrOpen(tr.Start) + ".." + fmtDateOrOpen(tr.End)
}

// buildTimeRange assembles a search time window from a "last N days" value and
// optional explicit DD/MM/YYYY bounds. rangeDays <= 0 is ignored; an explicit
// start/end overrides the corresponding bound. Shared by the CLI flags and the
// server request bodies so the date semantics stay identical.
func buildTimeRange(rangeDays int, start, end string) (config.TimeRange, error) {
	var tr config.TimeRange
	if rangeDays > 0 {
		now := time.Now()
		tr.Start = now.AddDate(0, 0, -rangeDays)
		tr.End = now
	}
	if start != "" {
		t, err := parseDMY(start)
		if err != nil {
			return tr, fmt.Errorf("invalid start %q: expected DD/MM/YYYY", start)
		}
		tr.Start = t
	}
	if end != "" {
		t, err := parseDMY(end)
		if err != nil {
			return tr, fmt.Errorf("invalid end %q: expected DD/MM/YYYY", end)
		}
		tr.End = t
	}
	if !tr.Start.IsZero() && !tr.End.IsZero() && tr.End.Before(tr.Start) {
		return tr, fmt.Errorf("end (%s) is before start (%s)",
			tr.End.Format(dmyLayout), tr.Start.Format(dmyLayout))
	}
	return tr, nil
}
