package provider

import (
	"context"
	"fmt"

	"github.com/rishang/seek/config"
)

// BraveProvider supports search via the Brave Search API.
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

func (p *BraveProvider) Name() string { return "brave" }

// ---- response types ----

type braveSearchResponse struct {
	Web struct {
		Results []braveResult `json:"results"`
	} `json:"web"`
}

type braveResult struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
}

// ---- Search ----

func (p *BraveProvider) Search(ctx context.Context, query string) ([]config.SearchResult, error) {
	var resp braveSearchResponse
	r, err := p.client.R().
		SetContext(ctx).
		SetHeader("Accept", "application/json").
		SetHeader("X-Subscription-Token", p.apiKey).
		SetQueryParam("q", query).
		SetQueryParam("count", "10").
		SetSuccessResult(&resp).
		Get("https://api.search.brave.com/res/v1/web/search")
	if err != nil {
		return nil, fmt.Errorf("brave search request failed: %w", err)
	}
	if err := p.expectOK("search", r); err != nil {
		return nil, err
	}

	results := make([]config.SearchResult, len(resp.Web.Results))
	for i, item := range resp.Web.Results {
		results[i] = config.SearchResult{
			Title:   item.Title,
			URL:     item.URL,
			Snippet: item.Description,
		}
	}
	return results, nil
}

var _ SearchProvider = (*BraveProvider)(nil)
