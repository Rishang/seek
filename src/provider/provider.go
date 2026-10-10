package provider

import (
	"context"

	"github.com/rishang/seek/config"
)

// SearchProvider performs web searches.
type SearchProvider interface {
	Search(ctx context.Context, query string, opts config.SearchOptions) ([]config.SearchResult, error)
}

// TimeRangeSearcher is implemented by search providers that honor
// SearchOptions.TimeRange. The CLI checks this to warn when a requested time
// range will be ignored by the selected provider.
type TimeRangeSearcher interface {
	SupportsTimeRange() bool
}

// KindSearcher is implemented by search providers that can run non-web
// searches (SearchOptions.Kind = "news" | "images"). Web search is always
// supported, so providers without it only serve plain web searches.
type KindSearcher interface {
	SupportsKind(kind string) bool
}

// SupportsKind reports whether sp can run a search of the given kind.
func SupportsKind(sp SearchProvider, kind string) bool {
	if kind == config.KindWeb {
		return true
	}
	ks, ok := sp.(KindSearcher)
	return ok && ks.SupportsKind(kind)
}

// FetchProvider extracts content from a single URL.
type FetchProvider interface {
	Fetch(ctx context.Context, url string, opts config.FetchOptions) (*config.FetchResult, error)
}

// CrawlProvider crawls a website starting from a URL.
type CrawlProvider interface {
	Crawl(ctx context.Context, url string) (*config.CrawlResult, error)
}

// TranscribeProvider returns the spoken-word transcript of a video URL
// (YouTube, TikTok, a direct media file, ...). lang is an optional ISO 639-1
// preference; providers fall back to the first available language.
type TranscribeProvider interface {
	Transcribe(ctx context.Context, url, lang string) (*config.FetchResult, error)
}

// ParseProvider converts a document URL (PDF, docx, xlsx, ...) to markdown.
type ParseProvider interface {
	Parse(ctx context.Context, url string) (*config.FetchResult, error)
}

// Provider is any registered provider. The concrete capabilities (search,
// fetch, crawl) are probed by type-asserting to the interfaces above, so a
// provider need only implement whichever it supports.
type Provider = any

// Attempt records one provider tried by an auto chain. Err is nil for the
// provider that served the result.
type Attempt struct {
	Provider string
	Err      error
}

// AutoReporter is implemented by the auto meta-provider so the CLI can report
// which provider served a request and why earlier ones failed.
type AutoReporter interface {
	Attempts() []Attempt
}
