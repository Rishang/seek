package provider

import (
	"context"
	"fmt"

	"github.com/rishang/seek/config"
)

// BraveProvider supports web, news, and image search via the Brave Search API.
// Docs: https://api-dashboard.search.brave.com/app/documentation/web-search
//
// Brave authenticates with an X-Subscription-Token header (not bearer auth) and
// takes the query as a GET parameter, so it issues the request directly rather
// than through the shared post/get helpers.
type BraveProvider struct {
	*httpClient
}

func NewBraveProvider(cfg config.ProviderConfig) *BraveProvider {
	return &BraveProvider{httpClient: newHTTPClient("brave", cfg.APIKey)}
}

// ---- response types ----

type braveSearchResponse struct {
	Web struct {
		Results []braveResult `json:"results"`
	} `json:"web"`
}

// braveNewsResponse is shared by the news and image endpoints, which both
// return a flat top-level results list.
type braveNewsResponse struct {
	Results []braveResult `json:"results"`
}

type braveResult struct {
	Title       string `json:"title"`
	URL         string `json:"url"` // for images: the source page
	Description string `json:"description"`
	PageAge     string `json:"page_age"` // ISO 8601 publish date, when known
	Properties  struct {
		URL string `json:"url"` // images only: the original image URL
	} `json:"properties"`
}

// bravePaths maps a search kind to its Brave endpoint path.
var bravePaths = map[string]string{
	config.KindWeb:    "/res/v1/web/search",
	config.KindNews:   "/res/v1/news/search",
	config.KindImages: "/res/v1/images/search",
}

// ---- Search ----

func (p *BraveProvider) SupportsTimeRange() bool { return true }

func (p *BraveProvider) SupportsKind(kind string) bool { return bravePaths[kind] != "" }

// braveBase is a var so tests can point the endpoints at a local server.
var braveBase = "https://api.search.brave.com"

func (p *BraveProvider) Search(ctx context.Context, query string, opts config.SearchOptions) ([]config.SearchResult, error) {
	path, ok := bravePaths[opts.Kind]
	if !ok {
		return nil, fmt.Errorf("brave: unsupported search kind %q", opts.Kind)
	}

	// Web nests results under "web"; news and images return them top-level.
	var web braveSearchResponse
	var flat braveNewsResponse
	var out any = &flat
	if opts.Kind == config.KindWeb {
		out = &web
	}
	r := p.client.R().
		SetContext(ctx).
		SetHeader("Accept", "application/json").
		SetHeader("X-Subscription-Token", p.apiKey).
		SetQueryParam("q", query).
		SetQueryParam("count", "10").
		SetSuccessResult(out)
	// The image endpoint has no freshness filter.
	if freshness := braveFreshness(opts.TimeRange); freshness != "" && opts.Kind != config.KindImages {
		r.SetQueryParam("freshness", freshness)
	}
	resp1, err := r.Get(braveBase + path)
	if err != nil {
		return nil, fmt.Errorf("brave search request failed: %w", err)
	}
	if err := p.expectOK("search", resp1); err != nil {
		return nil, err
	}

	items := flat.Results
	if opts.Kind == config.KindWeb {
		items = web.Web.Results
	}
	results := make([]config.SearchResult, len(items))
	for i, item := range items {
		results[i] = config.SearchResult{
			Title:         item.Title,
			URL:           item.URL,
			Snippet:       item.Description,
			PublishedDate: item.PageAge,
			ImageURL:      item.Properties.URL,
		}
	}
	return results, nil
}

var (
	_ SearchProvider = (*BraveProvider)(nil)
	_ KindSearcher   = (*BraveProvider)(nil)
)
