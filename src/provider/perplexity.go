package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/rishang/seek/config"
)

// PerplexityProvider supports search and fetch via the Perplexity API.
//
// Search uses the dedicated Search API (/search), which returns ranked web
// results with snippets and publish dates. Fetch uses the Chat Completions API
// (/chat/completions) with an online "sonar" model to retrieve and extract a
// single page; because that content is model-processed it is a best-effort
// extraction rather than the raw page source.
//
// Perplexity uses bearer auth, so it goes through the shared post helper.
//
// Docs: https://docs.perplexity.ai
type PerplexityProvider struct {
	*httpClient
}

const perplexityBaseURL = "https://api.perplexity.ai"

// perplexityFetchModel is the online model used to extract page content.
const perplexityFetchModel = "sonar"

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

type pplxChatRequest struct {
	Model    string        `json:"model"`
	Messages []pplxMessage `json:"messages"`
}

type pplxMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type pplxChatResponse struct {
	Choices []pplxChoice `json:"choices"`
}

type pplxChoice struct {
	Message pplxMessage `json:"message"`
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

// ---- Fetch (Chat Completions with an online model) ----

func (p *PerplexityProvider) Fetch(ctx context.Context, url string, opts config.FetchOptions) (*config.FetchResult, error) {
	format := "markdown"
	if opts.OutputFormat == config.FormatHTML {
		format = "html"
	}
	prompt := fmt.Sprintf(
		"Retrieve the page at %s and return its main content as %s. "+
			"Output only the extracted content with no commentary.",
		url, format,
	)

	body := pplxChatRequest{
		Model: perplexityFetchModel,
		Messages: []pplxMessage{
			{Role: "system", Content: "You are a web content extractor. Return only the requested page content."},
			{Role: "user", Content: prompt},
		},
	}

	var resp pplxChatResponse
	if err := p.post(ctx, "fetch", perplexityBaseURL+"/chat/completions", &body, &resp); err != nil {
		return nil, err
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("perplexity fetch returned no content for %s", url)
	}

	return &config.FetchResult{
		URL:     url,
		Content: strings.TrimSpace(resp.Choices[0].Message.Content),
		Format:  format,
	}, nil
}

// Compile-time interface checks.
var (
	_ SearchProvider = (*PerplexityProvider)(nil)
	_ FetchProvider  = (*PerplexityProvider)(nil)
)
