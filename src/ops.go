package main

import (
	"context"
	"fmt"
	"time"

	"github.com/rishang/seek/config"
	"github.com/rishang/seek/logx"
	"github.com/rishang/seek/provider"
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
	logx.Debug("op: search provider=%s query=%q kind=%s range=%s", providerName, query, opts.Kind, fmtRange(opts.TimeRange))
	sp, err := factory.Search(providerName)
	if err != nil {
		return nil, err
	}
	if err := checkKind(sp, providerName, opts.Kind); err != nil {
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

// checkKind fails fast when sp cannot run a news/images search. Unlike an
// unsupported time range this is an error, not a warning: falling back to a web
// search would return a different kind of result.
func checkKind(sp provider.SearchProvider, name, kind string) error {
	if provider.SupportsKind(sp, kind) {
		return nil
	}
	if name == "auto" {
		return fmt.Errorf("no configured provider supports %s search", kind)
	}
	return fmt.Errorf("provider %q does not support %s search", name, kind)
}

// parseKind validates a search kind from a request body ("" or "web" mean a
// regular web search).
func parseKind(s string) (string, error) {
	switch s {
	case "", "web":
		return config.KindWeb, nil
	case config.KindNews, config.KindImages:
		return s, nil
	}
	return "", fmt.Errorf("invalid kind %q: expected web, news, or images", s)
}

// opFetch runs a fetch. An empty providerName uses the configured default;
// an empty format uses the configured fetch output format.
func opFetch(ctx context.Context, providerName, url, format string) (*config.FetchResult, error) {
	if providerName == "" {
		providerName = cfg.Fetch.Provider
	}
	outFormat := cfg.Fetch.Options.OutputFormat
	if format != "" {
		outFormat = parseFormat(format)
	}
	logx.Debug("op: fetch provider=%s url=%q format=%s", providerName, url, outFormat)
	sp, err := factory.Fetch(providerName)
	if err != nil {
		return nil, err
	}
	result, err := sp.Fetch(ctx, url, config.FetchOptions{OutputFormat: outFormat})
	logAutoAttempts(sp)
	if err != nil {
		logx.Debug("op: fetch failed: %v", err)
		return nil, err
	}
	logx.Debug("op: fetch ok, %d bytes", len(result.Content))
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

// opTranscribe runs a transcribe. An empty providerName uses the configured
// default.
func opTranscribe(ctx context.Context, providerName, url, lang string) (*config.FetchResult, error) {
	if providerName == "" {
		providerName = cfg.Transcribe.Provider
	}
	logx.Debug("op: transcribe provider=%s url=%q lang=%s", providerName, url, lang)
	tp, err := factory.Transcribe(providerName)
	if err != nil {
		return nil, err
	}
	result, err := tp.Transcribe(ctx, url, lang)
	logAutoAttempts(tp)
	if err != nil {
		logx.Debug("op: transcribe failed: %v", err)
		return nil, err
	}
	logx.Debug("op: transcribe ok, %d bytes", len(result.Content))
	return result, nil
}

// opParse parses a document URL. An empty providerName uses the configured
// default.
func opParse(ctx context.Context, providerName, url string) (*config.FetchResult, error) {
	if providerName == "" {
		providerName = cfg.Parse.Provider
	}
	logx.Debug("op: parse provider=%s url=%q", providerName, url)
	pp, err := factory.Parse(providerName)
	if err != nil {
		return nil, err
	}
	result, err := pp.Parse(ctx, url)
	logAutoAttempts(pp)
	if err != nil {
		logx.Debug("op: parse failed: %v", err)
		return nil, err
	}
	logx.Debug("op: parse ok, %d bytes", len(result.Content))
	return result, nil
}

// defaultMapLimit caps map results when the caller gives no limit.
const defaultMapLimit = 100

// opMap runs a map. An empty providerName uses the configured default; a
// non-positive limit uses defaultMapLimit.
func opMap(ctx context.Context, providerName, url string, limit int) ([]config.MapLink, error) {
	if providerName == "" {
		providerName = cfg.Map.Provider
	}
	if limit <= 0 {
		limit = defaultMapLimit
	}
	logx.Debug("op: map provider=%s url=%q limit=%d", providerName, url, limit)
	mp, err := factory.Map(providerName)
	if err != nil {
		return nil, err
	}
	links, err := mp.Map(ctx, url, limit)
	logAutoAttempts(mp)
	if err != nil {
		logx.Debug("op: map failed: %v", err)
		return nil, err
	}
	logx.Debug("op: map ok, %d link(s)", len(links))
	return links, nil
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
