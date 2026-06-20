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

// ScrapeProvider extracts content from a single URL.
type ScrapeProvider interface {
	Scrape(ctx context.Context, url string, opts config.ScrapeOptions) (*config.ScrapeResult, error)
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
