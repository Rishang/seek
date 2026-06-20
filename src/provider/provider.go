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

// FetchProvider extracts content from a single URL.
type FetchProvider interface {
	Fetch(ctx context.Context, url string, opts config.FetchOptions) (*config.FetchResult, error)
}

// CrawlProvider crawls a website starting from a URL.
type CrawlProvider interface {
	Crawl(ctx context.Context, url string) (*config.CrawlResult, error)
}

// Provider composes the optional capabilities a provider may offer.
// A concrete provider implements whichever interfaces it supports.
type Provider interface {
	Name() string
}

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
