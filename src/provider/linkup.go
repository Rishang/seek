package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/rishang/seek/config"
)

// LinkupProvider supports search and fetch via the Linkup API.
// Docs: https://docs.linkup.so
type LinkupProvider struct {
	*httpClient
}

// linkupBase is a var so tests can point the endpoints at a local server.
var linkupBase = "https://api.linkup.so/v1"

func NewLinkupProvider(cfg config.ProviderConfig) *LinkupProvider {
	return &LinkupProvider{httpClient: newHTTPClient("linkup", cfg.APIKey)}
}

// ---- request / response types ----

type linkupSearchRequest struct {
	Q          string `json:"q"`
	Depth      string `json:"depth"`
	OutputType string `json:"outputType"`
	MaxResults int    `json:"maxResults,omitempty"`
	FromDate   string `json:"fromDate,omitempty"` // YYYY-MM-DD
	ToDate     string `json:"toDate,omitempty"`   // YYYY-MM-DD
}

type linkupSearchResponse struct {
	Results []struct {
		Type        string `json:"type"` // "text" | "image"
		Name        string `json:"name"`
		URL         string `json:"url"`
		Content     string `json:"content"`
		PublishedAt string `json:"publishedAt"`
	} `json:"results"`
}

type linkupFetchRequest struct {
	URL string `json:"url"`
}

type linkupFetchResponse struct {
	Markdown string `json:"markdown"`
}

// ---- Search ----

func (p *LinkupProvider) SupportsTimeRange() bool { return true }

func (p *LinkupProvider) Search(ctx context.Context, query string, opts config.SearchOptions) ([]config.SearchResult, error) {
	body := linkupSearchRequest{
		Q:          query,
		Depth:      "standard",
		OutputType: "searchResults",
		MaxResults: 10,
		FromDate:   ymd(opts.TimeRange.Start),
		ToDate:     ymd(opts.TimeRange.End),
	}

	var resp linkupSearchResponse
	if err := p.post(ctx, "search", linkupBase+"/search", &body, &resp); err != nil {
		return nil, err
	}

	results := make([]config.SearchResult, 0, len(resp.Results))
	for _, item := range resp.Results {
		if item.Type == "image" {
			continue
		}
		results = append(results, config.SearchResult{
			Title:         item.Name,
			URL:           item.URL,
			Snippet:       strings.TrimSpace(item.Content),
			PublishedDate: item.PublishedAt,
		})
	}
	return results, nil
}

// ---- Fetch ----

// Fetch returns the page as markdown; Linkup has no HTML output, so --format is ignored.
func (p *LinkupProvider) Fetch(ctx context.Context, url string, _ config.FetchOptions) (*config.FetchResult, error) {
	var resp linkupFetchResponse
	if err := p.post(ctx, "fetch", linkupBase+"/fetch", &linkupFetchRequest{URL: url}, &resp); err != nil {
		return nil, err
	}
	if resp.Markdown == "" {
		return nil, fmt.Errorf("linkup fetch returned no content for %s", url)
	}
	return &config.FetchResult{URL: url, Content: resp.Markdown, Format: "markdown"}, nil
}

var (
	_ SearchProvider    = (*LinkupProvider)(nil)
	_ TimeRangeSearcher = (*LinkupProvider)(nil)
	_ FetchProvider     = (*LinkupProvider)(nil)
)
