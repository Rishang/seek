package provider

import (
	"context"
	"fmt"

	"github.com/rishang/seek/config"
)

// SpiderProvider supports search, scrape, and crawl via spider.cloud.
// Docs: https://spider.cloud/docs/api
type SpiderProvider struct {
	*httpClient
}

func NewSpiderProvider(cfg config.ProviderConfig) *SpiderProvider {
	return &SpiderProvider{httpClient: newHTTPClient("spider.cloud", cfg.APIKey)}
}

func (p *SpiderProvider) Name() string { return "spider.cloud" }

const spiderBaseURL = "https://api.spider.cloud"

// ---- request / response types ----

type spRequest struct {
	URL          string `json:"url,omitempty"`
	Search       string `json:"search,omitempty"`
	SearchLimit  int    `json:"search_limit,omitempty"`
	Limit        int    `json:"limit,omitempty"`
	Depth        int    `json:"depth,omitempty"`
	ReturnFormat string `json:"return_format,omitempty"` // "markdown", "raw", "text"
	Request      string `json:"request,omitempty"`       // "smart", "http", "chrome"
	Metadata     bool   `json:"metadata,omitempty"`
	Readability  bool   `json:"readability,omitempty"`
}

type spPage struct {
	URL      string      `json:"url"`
	Content  string      `json:"content"`
	Status   int         `json:"status"`
	Metadata *spMetadata `json:"metadata,omitempty"`
}

type spMetadata struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

// ---- Search ----

func (p *SpiderProvider) Search(ctx context.Context, query string) ([]config.SearchResult, error) {
	body := spRequest{
		Search:       query,
		SearchLimit:  10,
		ReturnFormat: "markdown",
		Request:      "smart",
		Metadata:     true,
	}

	var pages []spPage
	if err := p.post(ctx, "search", spiderBaseURL+"/search", &body, &pages); err != nil {
		return nil, err
	}

	results := make([]config.SearchResult, len(pages))
	for i, page := range pages {
		title := ""
		if page.Metadata != nil {
			title = page.Metadata.Title
		}
		snippet := page.Content
		if len(snippet) > 300 {
			snippet = snippet[:300]
		}
		results[i] = config.SearchResult{
			Title:   title,
			URL:     page.URL,
			Snippet: snippet,
		}
	}
	return results, nil
}

// ---- Scrape ----

func (p *SpiderProvider) Scrape(ctx context.Context, url string, opts config.ScrapeOptions) (*config.ScrapeResult, error) {
	format := "markdown"
	if opts.OutputFormat == config.FormatHTML {
		format = "raw"
	}

	body := spRequest{
		URL:          url,
		ReturnFormat: format,
		Request:      "smart",
		Readability:  true,
	}

	var pages []spPage
	if err := p.post(ctx, "scrape", spiderBaseURL+"/scrape", &body, &pages); err != nil {
		return nil, err
	}

	if len(pages) == 0 {
		return nil, fmt.Errorf("spider.cloud scrape returned no pages for %s", url)
	}

	return &config.ScrapeResult{
		URL:     url,
		Content: pages[0].Content,
		Format:  string(opts.OutputFormat),
	}, nil
}

// ---- Crawl ----

func (p *SpiderProvider) Crawl(ctx context.Context, url string) (*config.CrawlResult, error) {
	body := spRequest{
		URL:          url,
		Limit:        50,
		Depth:        3,
		ReturnFormat: "markdown",
		Request:      "smart",
		Readability:  true,
	}

	var pages []spPage
	if err := p.post(ctx, "crawl", spiderBaseURL+"/crawl", &body, &pages); err != nil {
		return nil, err
	}

	urls := make([]string, len(pages))
	var allContent string
	for i, page := range pages {
		urls[i] = page.URL
		allContent += page.Content + "\n\n"
	}

	return &config.CrawlResult{
		URL:     url,
		Pages:   urls,
		Content: allContent,
	}, nil
}

var (
	_ SearchProvider = (*SpiderProvider)(nil)
	_ ScrapeProvider = (*SpiderProvider)(nil)
	_ CrawlProvider  = (*SpiderProvider)(nil)
)
