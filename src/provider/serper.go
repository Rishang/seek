package provider

import (
	"context"
	"fmt"

	"github.com/rishang/seek/config"
)

// SerperProvider supports Google web, news and image search via google.serper.dev
// and fetch via scrape.serper.dev.
// Docs: https://serper.dev
//
// Serper authenticates with an X-API-KEY header (not bearer auth), so it
// issues requests directly rather than through the shared post helper.
type SerperProvider struct {
	*httpClient
}

// serperSearchBase and serperScrapeURL are vars so tests can point them at a local server.
var (
	serperSearchBase = "https://google.serper.dev"
	serperScrapeURL  = "https://scrape.serper.dev"
)

func NewSerperProvider(cfg config.ProviderConfig) *SerperProvider {
	return &SerperProvider{httpClient: newHTTPClient("serper", cfg.APIKey)}
}

// ---- request / response types ----

type serperSearchRequest struct {
	Q   string `json:"q"`
	Num int    `json:"num,omitempty"`
	TBS string `json:"tbs,omitempty"`
}

type serperResult struct {
	Title    string `json:"title"`
	Link     string `json:"link"` // for images: the source page
	Snippet  string `json:"snippet"`
	Date     string `json:"date"`
	ImageURL string `json:"imageUrl"`
}

// serperSearchResponse covers all three endpoints; each fills only its own list.
type serperSearchResponse struct {
	Organic []serperResult `json:"organic"`
	News    []serperResult `json:"news"`
	Images  []serperResult `json:"images"`
}

type serperScrapeRequest struct {
	URL             string `json:"url"`
	IncludeMarkdown bool   `json:"includeMarkdown"`
}

type serperScrapeResponse struct {
	Text     string `json:"text"`
	Markdown string `json:"markdown"`
}

// serperPaths maps a search kind to its Serper endpoint path.
var serperPaths = map[string]string{
	config.KindWeb:    "/search",
	config.KindNews:   "/news",
	config.KindImages: "/images",
}

func (p *SerperProvider) postJSON(ctx context.Context, op, url string, body, out any) error {
	resp, err := p.client.R().
		SetContext(ctx).
		SetHeader("X-API-KEY", p.apiKey).
		SetHeader("Content-Type", "application/json").
		SetBody(body).
		SetSuccessResult(out).
		Post(url)
	if err != nil {
		return fmt.Errorf("serper %s request failed: %w", op, err)
	}
	return p.expectOK(op, resp)
}

// ---- Search ----

func (p *SerperProvider) SupportsTimeRange() bool { return true }

func (p *SerperProvider) SupportsKind(kind string) bool { return serperPaths[kind] != "" }

func (p *SerperProvider) Search(ctx context.Context, query string, opts config.SearchOptions) ([]config.SearchResult, error) {
	path, ok := serperPaths[opts.Kind]
	if !ok {
		return nil, fmt.Errorf("serper: unsupported search kind %q", opts.Kind)
	}
	body := serperSearchRequest{Q: query, Num: 10, TBS: googleTBS(opts.TimeRange)}

	var resp serperSearchResponse
	if err := p.postJSON(ctx, "search", serperSearchBase+path, &body, &resp); err != nil {
		return nil, err
	}

	items := resp.Organic
	switch opts.Kind {
	case config.KindNews:
		items = resp.News
	case config.KindImages:
		items = resp.Images
	}
	results := make([]config.SearchResult, len(items))
	for i, item := range items {
		results[i] = config.SearchResult{
			Title:         item.Title,
			URL:           item.Link,
			Snippet:       item.Snippet,
			PublishedDate: item.Date,
		}
		if opts.Kind == config.KindImages {
			results[i].ImageURL = item.ImageURL
		}
	}
	return results, nil
}

// ---- Fetch ----

// Fetch returns the page as markdown, falling back to Serper's plain text; the
// scrape endpoint has no HTML output, so --format is ignored.
func (p *SerperProvider) Fetch(ctx context.Context, url string, _ config.FetchOptions) (*config.FetchResult, error) {
	var resp serperScrapeResponse
	if err := p.postJSON(ctx, "fetch", serperScrapeURL, &serperScrapeRequest{URL: url, IncludeMarkdown: true}, &resp); err != nil {
		return nil, err
	}
	content := resp.Markdown
	if content == "" {
		content = resp.Text
	}
	if content == "" {
		return nil, fmt.Errorf("serper fetch returned no content for %s", url)
	}
	return &config.FetchResult{URL: url, Content: content, Format: "markdown"}, nil
}

var (
	_ SearchProvider    = (*SerperProvider)(nil)
	_ KindSearcher      = (*SerperProvider)(nil)
	_ TimeRangeSearcher = (*SerperProvider)(nil)
	_ FetchProvider     = (*SerperProvider)(nil)
)
