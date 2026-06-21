package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/rishang/seek/config"
)

// ExaProvider supports search and fetch via the Exa API.
// Exa is a neural/embeddings search engine that can also return page contents
// inline. Fetch maps to the /contents endpoint.
//
// Docs: https://exa.ai/docs/reference/search
type ExaProvider struct {
	*httpClient
}

const exaBaseURL = "https://api.exa.ai"

func NewExaProvider(cfg config.ProviderConfig) *ExaProvider {
	return &ExaProvider{httpClient: newHTTPClient("exa", cfg.APIKey)}
}

// ---- request / response types ----

// exaContents controls what page material Exa returns per result.
type exaContents struct {
	Text       *exaText `json:"text,omitempty"`
	Highlights bool     `json:"highlights,omitempty"`
}

type exaText struct {
	MaxCharacters   int  `json:"maxCharacters,omitempty"`
	IncludeHTMLTags bool `json:"includeHtmlTags,omitempty"`
}

type exaSearchRequest struct {
	Query              string       `json:"query"`
	Type               string       `json:"type,omitempty"`
	NumResults         int          `json:"numResults,omitempty"`
	StartPublishedDate string       `json:"startPublishedDate,omitempty"` // ISO 8601
	EndPublishedDate   string       `json:"endPublishedDate,omitempty"`   // ISO 8601
	Contents           *exaContents `json:"contents,omitempty"`
}

type exaResult struct {
	Title         string `json:"title"`
	URL           string `json:"url"`
	ID            string `json:"id"`
	PublishedDate string `json:"publishedDate"`
	Author        string `json:"author"`
	Text          string `json:"text"`
}

type exaSearchResponse struct {
	Results []exaResult `json:"results"`
}

type exaContentsRequest struct {
	URLs []string `json:"urls"`
	Text *exaText `json:"text,omitempty"`
}

type exaContentsResponse struct {
	Results []exaResult `json:"results"`
}

// ---- Search ----

func (p *ExaProvider) SupportsTimeRange() bool { return true }

func (p *ExaProvider) Search(ctx context.Context, query string, opts config.SearchOptions) ([]config.SearchResult, error) {
	body := exaSearchRequest{
		Query:              query,
		Type:               "auto",
		NumResults:         10,
		StartPublishedDate: iso8601(opts.TimeRange.Start),
		EndPublishedDate:   iso8601(opts.TimeRange.End),
		// A short text excerpt gives a reliable snippet without pulling the
		// full page for every result.
		Contents: &exaContents{Text: &exaText{MaxCharacters: 1000}},
	}

	var resp exaSearchResponse
	if err := p.post(ctx, "search", exaBaseURL+"/search", &body, &resp); err != nil {
		return nil, err
	}

	results := make([]config.SearchResult, len(resp.Results))
	for i, item := range resp.Results {
		results[i] = config.SearchResult{
			Title:         item.Title,
			URL:           item.URL,
			Snippet:       strings.TrimSpace(item.Text),
			PublishedDate: item.PublishedDate,
		}
	}
	return results, nil
}

// ---- Fetch (Exa /contents) ----

func (p *ExaProvider) Fetch(ctx context.Context, url string, opts config.FetchOptions) (*config.FetchResult, error) {
	body := exaContentsRequest{
		URLs: []string{url},
		Text: &exaText{IncludeHTMLTags: opts.OutputFormat == config.FormatHTML},
	}

	var resp exaContentsResponse
	if err := p.post(ctx, "fetch", exaBaseURL+"/contents", &body, &resp); err != nil {
		return nil, err
	}
	if len(resp.Results) == 0 {
		return nil, fmt.Errorf("exa fetch returned no contents for %s", url)
	}

	format := "markdown"
	if opts.OutputFormat == config.FormatHTML {
		format = "html"
	}
	return &config.FetchResult{
		URL:     url,
		Content: resp.Results[0].Text,
		Format:  format,
	}, nil
}

// Compile-time interface checks.
var (
	_ SearchProvider = (*ExaProvider)(nil)
	_ FetchProvider  = (*ExaProvider)(nil)
)
