package provider

import (
	"context"
	"strings"

	"github.com/rishang/seek/config"
)

// PerplexityProvider supports web search via the Perplexity Search API.
//
// Search uses the dedicated Search API (/search), which returns ranked web
// results with snippets and publish dates, and supports M/D/YYYY date-range
// filters (TimeRangeSearcher).
//
// It is search-only: Perplexity has no page-scraping endpoint, and extracting a
// page through a chat model yields a paraphrased summary rather than the real
// page content, so fetch is left to providers that actually scrape.
//
// Perplexity uses bearer auth, so it goes through the shared post helper.
//
// Docs: https://docs.perplexity.ai
type PerplexityProvider struct {
	*httpClient
}

const perplexityBaseURL = "https://api.perplexity.ai"

func NewPerplexityProvider(cfg config.ProviderConfig) *PerplexityProvider {
	return &PerplexityProvider{httpClient: newHTTPClient("perplexity", cfg.APIKey)}
}

func (p *PerplexityProvider) Name() string { return "perplexity" }

// ---- request / response types ----

type pplxSearchRequest struct {
	Query                  string `json:"query"`
	MaxResults             int    `json:"max_results,omitempty"`
	SearchAfterDateFilter  string `json:"search_after_date_filter,omitempty"`  // M/D/YYYY
	SearchBeforeDateFilter string `json:"search_before_date_filter,omitempty"` // M/D/YYYY
}

type pplxSearchResponse struct {
	Results []pplxSearchResult `json:"results"`
}

type pplxSearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
	Date    string `json:"date"`
}

// ---- Search ----

func (p *PerplexityProvider) SupportsTimeRange() bool { return true }

func (p *PerplexityProvider) Search(ctx context.Context, query string, opts config.SearchOptions) ([]config.SearchResult, error) {
	body := pplxSearchRequest{
		Query:                  query,
		MaxResults:             10,
		SearchAfterDateFilter:  mdy(opts.TimeRange.Start),
		SearchBeforeDateFilter: mdy(opts.TimeRange.End),
	}

	var resp pplxSearchResponse
	if err := p.post(ctx, "search", perplexityBaseURL+"/search", &body, &resp); err != nil {
		return nil, err
	}

	results := make([]config.SearchResult, len(resp.Results))
	for i, item := range resp.Results {
		results[i] = config.SearchResult{
			Title:         item.Title,
			URL:           item.URL,
			Snippet:       strings.TrimSpace(item.Snippet),
			PublishedDate: item.Date,
		}
	}
	return results, nil
}

// Compile-time interface checks.
var (
	_ SearchProvider    = (*PerplexityProvider)(nil)
	_ TimeRangeSearcher = (*PerplexityProvider)(nil)
)
