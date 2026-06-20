package provider

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rishang/seek/config"
)

// FirecrawlProvider supports search, scrape, and crawl via firecrawl.
// Docs: https://docs.firecrawl.dev
type FirecrawlProvider struct {
	*httpClient
	host string // empty for cloud; set for self-hosted OSS
}

func NewFirecrawlProvider(cfg config.ProviderConfig) *FirecrawlProvider {
	return &FirecrawlProvider{
		httpClient: newHTTPClient("firecrawl", cfg.APIKey),
		host:       cfg.Host,
	}
}

func (p *FirecrawlProvider) Name() string { return "firecrawl" }

func (p *FirecrawlProvider) baseURL() string {
	if p.host != "" {
		return strings.TrimRight(p.host, "/")
	}
	return "https://api.firecrawl.dev"
}

// ---- request / response types (Firecrawl v2) ----

type fcSearchRequest struct {
	Query             string           `json:"query"`
	Limit             int              `json:"limit,omitempty"`
	TBS               string           `json:"tbs,omitempty"`
	Sources           []string         `json:"sources,omitempty"`
	IncludeDomains    []string         `json:"includeDomains,omitempty"`
	ExcludeDomains    []string         `json:"excludeDomains,omitempty"`
	IgnoreInvalidURLs bool             `json:"ignoreInvalidURLs,omitempty"`
	ScrapeOptions     *fcScrapeOptions `json:"scrapeOptions,omitempty"`
}

type fcSearchResponse struct {
	Success bool           `json:"success"`
	Data    []fcSearchItem `json:"data"`
}

type fcSearchItem struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

type fcScrapeRequest struct {
	URL             string   `json:"url"`
	Formats         []string `json:"formats,omitempty"`
	OnlyMainContent bool     `json:"onlyMainContent,omitempty"`
	WaitFor         int      `json:"waitFor,omitempty"`
	Timeout         int      `json:"timeout,omitempty"`
}

type fcScrapeResponse struct {
	Success bool         `json:"success"`
	Data    fcScrapeData `json:"data"`
}

type fcScrapeData struct {
	Markdown string     `json:"markdown"`
	HTML     string     `json:"html"`
	Metadata fcMetadata `json:"metadata"`
}

type fcMetadata struct {
	Title       string `json:"title"`
	Description string `json:"ogDescription"`
}

type fcScrapeOptions struct {
	Formats         []string `json:"formats,omitempty"`
	OnlyMainContent bool     `json:"onlyMainContent,omitempty"`
}

type fcCrawlRequest struct {
	URL                string           `json:"url"`
	Limit              int              `json:"limit,omitempty"`
	MaxDiscoveryDepth  int              `json:"maxDiscoveryDepth,omitempty"`
	IncludePaths       []string         `json:"includePaths,omitempty"`
	ExcludePaths       []string         `json:"excludePaths,omitempty"`
	AllowExternalLinks bool             `json:"allowExternalLinks,omitempty"`
	AllowSubdomains    bool             `json:"allowSubdomains,omitempty"`
	ScrapeOptions      *fcScrapeOptions `json:"scrapeOptions,omitempty"`
}

type fcCrawlStartResponse struct {
	Success bool   `json:"success"`
	ID      string `json:"id"`
}

type fcCrawlStatusResponse struct {
	Status string         `json:"status"`
	Total  int            `json:"total"`
	Data   []fcScrapeData `json:"data"`
}

// ---- Search ----

func (p *FirecrawlProvider) SupportsTimeRange() bool { return true }

func (p *FirecrawlProvider) Search(ctx context.Context, query string, opts config.SearchOptions) ([]config.SearchResult, error) {
	body := fcSearchRequest{
		Query:   query,
		Limit:   10,
		TBS:     googleTBS(opts.TimeRange),
		Sources: []string{"web"},
	}

	var resp fcSearchResponse
	if err := p.post(ctx, "search", p.baseURL()+"/v2/search", &body, &resp); err != nil {
		return nil, err
	}

	results := make([]config.SearchResult, len(resp.Data))
	for i, item := range resp.Data {
		results[i] = config.SearchResult{
			Title:   item.Title,
			URL:     item.URL,
			Snippet: item.Description,
		}
	}
	return results, nil
}

// ---- Scrape ----

func (p *FirecrawlProvider) Scrape(ctx context.Context, url string, opts config.ScrapeOptions) (*config.ScrapeResult, error) {
	formats := []string{"markdown"}
	if opts.OutputFormat == config.FormatHTML {
		formats = []string{"html"}
	}

	body := fcScrapeRequest{
		URL:             url,
		Formats:         formats,
		OnlyMainContent: true,
		Timeout:         30000,
	}

	var resp fcScrapeResponse
	if err := p.post(ctx, "scrape", p.baseURL()+"/v2/scrape", &body, &resp); err != nil {
		return nil, err
	}

	content := resp.Data.Markdown
	if content == "" {
		content = resp.Data.HTML
	}
	return &config.ScrapeResult{
		URL:     url,
		Content: content,
		Format:  string(opts.OutputFormat),
	}, nil
}

// ---- Crawl ----

func (p *FirecrawlProvider) Crawl(ctx context.Context, url string) (*config.CrawlResult, error) {
	// 1. Start crawl
	startBody := fcCrawlRequest{
		URL:               url,
		Limit:             100,
		MaxDiscoveryDepth: 3,
		ScrapeOptions: &fcScrapeOptions{
			Formats:         []string{"markdown"},
			OnlyMainContent: true,
		},
	}

	var startResp fcCrawlStartResponse
	if err := p.post(ctx, "crawl start", p.baseURL()+"/v2/crawl", &startBody, &startResp); err != nil {
		return nil, err
	}
	jobID := startResp.ID

	// 2. Poll for completion
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			var statusResp fcCrawlStatusResponse
			if err := p.get(ctx, "crawl poll", p.baseURL()+"/v2/crawl/"+jobID, &statusResp); err != nil {
				return nil, err
			}

			if statusResp.Status == "completed" {
				pages := make([]string, len(statusResp.Data))
				var allContent string
				for i, d := range statusResp.Data {
					pages[i] = d.Metadata.Title
					allContent += d.Markdown + "\n\n"
				}
				return &config.CrawlResult{
					URL:     url,
					Pages:   pages,
					Content: allContent,
				}, nil
			}
			if statusResp.Status == "failed" {
				return nil, fmt.Errorf("firecrawl crawl job %s failed", jobID)
			}
		}
	}
}

// Compile-time interface checks.
var (
	_ SearchProvider = (*FirecrawlProvider)(nil)
	_ ScrapeProvider = (*FirecrawlProvider)(nil)
	_ CrawlProvider  = (*FirecrawlProvider)(nil)
)
