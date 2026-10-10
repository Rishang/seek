package provider

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rishang/seek/config"
)

// FirecrawlProvider supports search, fetch, and crawl via firecrawl.
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

func (p *FirecrawlProvider) baseURL() string {
	if p.host != "" {
		return strings.TrimRight(p.host, "/")
	}
	return "https://api.firecrawl.dev"
}

// ---- request / response types (Firecrawl v2) ----

type fcSearchRequest struct {
	Query             string          `json:"query"`
	Limit             int             `json:"limit,omitempty"`
	TBS               string          `json:"tbs,omitempty"`
	Sources           []string        `json:"sources,omitempty"`
	IncludeDomains    []string        `json:"includeDomains,omitempty"`
	ExcludeDomains    []string        `json:"excludeDomains,omitempty"`
	IgnoreInvalidURLs bool            `json:"ignoreInvalidURLs,omitempty"`
	FetchOptions      *fcFetchOptions `json:"scrapeOptions,omitempty"`
}

type fcSearchResponse struct {
	Success bool         `json:"success"`
	Data    fcSearchData `json:"data"`
}

// fcSearchData groups results by source (v2 returns an object, not a flat
// array). We request one source per call, so only its list is populated.
type fcSearchData struct {
	Web    []fcSearchItem `json:"web"`
	News   []fcSearchItem `json:"news"`
	Images []fcSearchItem `json:"images"`
}

// fcSearchItem covers all three sources: web uses description, news uses
// snippet/date, and images use imageUrl (url is the source page).
type fcSearchItem struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Snippet     string `json:"snippet"`
	Date        string `json:"date"`
	ImageURL    string `json:"imageUrl"`
}

type fcFetchRequest struct {
	URL             string   `json:"url"`
	Formats         []string `json:"formats,omitempty"`
	OnlyMainContent bool     `json:"onlyMainContent,omitempty"`
	WaitFor         int      `json:"waitFor,omitempty"`
	Timeout         int      `json:"timeout,omitempty"`
}

type fcFetchResponse struct {
	Success bool        `json:"success"`
	Data    fcFetchData `json:"data"`
}

type fcFetchData struct {
	Markdown string     `json:"markdown"`
	HTML     string     `json:"html"`
	Metadata fcMetadata `json:"metadata"`
}

type fcMetadata struct {
	Title       string `json:"title"`
	Description string `json:"ogDescription"`
}

type fcFetchOptions struct {
	Formats         []string `json:"formats,omitempty"`
	OnlyMainContent bool     `json:"onlyMainContent,omitempty"`
}

type fcCrawlRequest struct {
	URL                string          `json:"url"`
	Limit              int             `json:"limit,omitempty"`
	MaxDiscoveryDepth  int             `json:"maxDiscoveryDepth,omitempty"`
	IncludePaths       []string        `json:"includePaths,omitempty"`
	ExcludePaths       []string        `json:"excludePaths,omitempty"`
	AllowExternalLinks bool            `json:"allowExternalLinks,omitempty"`
	AllowSubdomains    bool            `json:"allowSubdomains,omitempty"`
	FetchOptions       *fcFetchOptions `json:"scrapeOptions,omitempty"`
}

type fcCrawlStartResponse struct {
	Success bool   `json:"success"`
	ID      string `json:"id"`
}

type fcCrawlStatusResponse struct {
	Status string        `json:"status"`
	Total  int           `json:"total"`
	Data   []fcFetchData `json:"data"`
}

// ---- Search ----

func (p *FirecrawlProvider) SupportsTimeRange() bool { return true }

func (p *FirecrawlProvider) SupportsKind(kind string) bool {
	return kind == config.KindNews || kind == config.KindImages
}

func (p *FirecrawlProvider) Search(ctx context.Context, query string, opts config.SearchOptions) ([]config.SearchResult, error) {
	source := opts.Kind
	if source == config.KindWeb {
		source = "web"
	}
	body := fcSearchRequest{
		Query:   query,
		Limit:   10,
		TBS:     googleTBS(opts.TimeRange),
		Sources: []string{source},
	}

	var resp fcSearchResponse
	if err := p.post(ctx, "search", p.baseURL()+"/v2/search", &body, &resp); err != nil {
		return nil, err
	}

	items := resp.Data.Web
	switch opts.Kind {
	case config.KindNews:
		items = resp.Data.News
	case config.KindImages:
		items = resp.Data.Images
	}
	results := make([]config.SearchResult, len(items))
	for i, item := range items {
		snippet := item.Description
		if snippet == "" {
			snippet = item.Snippet
		}
		results[i] = config.SearchResult{
			Title:         item.Title,
			URL:           item.URL,
			Snippet:       snippet,
			PublishedDate: item.Date,
			ImageURL:      item.ImageURL,
		}
	}
	return results, nil
}

// ---- Fetch ----

func (p *FirecrawlProvider) Fetch(ctx context.Context, url string, opts config.FetchOptions) (*config.FetchResult, error) {
	formats := []string{"markdown"}
	if opts.OutputFormat == config.FormatHTML {
		formats = []string{"html"}
	}

	body := fcFetchRequest{
		URL:             url,
		Formats:         formats,
		OnlyMainContent: true,
		Timeout:         30000,
	}

	var resp fcFetchResponse
	if err := p.post(ctx, "fetch", p.baseURL()+"/v2/scrape", &body, &resp); err != nil {
		return nil, err
	}

	content := resp.Data.Markdown
	if content == "" {
		content = resp.Data.HTML
	}
	return &config.FetchResult{
		URL:     url,
		Content: content,
		Format:  string(opts.OutputFormat),
	}, nil
}

// ---- Transcribe ----

// Transcribe scrapes the video page as markdown: for YouTube, firecrawl's
// scrape includes the caption track when one exists (no ASR fallback), along
// with the page title and description.
func (p *FirecrawlProvider) Transcribe(ctx context.Context, url, _ string) (*config.FetchResult, error) {
	return p.Fetch(ctx, url, config.FetchOptions{OutputFormat: config.FormatMarkdown})
}

// Parse converts a document URL to markdown. Scrape auto-detects the file type: PDF,
// docx/doc/odt/rtf, and xlsx/xls (each sheet becomes a table).
func (p *FirecrawlProvider) Parse(ctx context.Context, url string) (*config.FetchResult, error) {
	return p.Fetch(ctx, url, config.FetchOptions{OutputFormat: config.FormatMarkdown})
}

// ---- Crawl ----

func (p *FirecrawlProvider) Crawl(ctx context.Context, url string) (*config.CrawlResult, error) {
	// 1. Start crawl
	startBody := fcCrawlRequest{
		URL:               url,
		Limit:             100,
		MaxDiscoveryDepth: 3,
		FetchOptions: &fcFetchOptions{
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
	_ SearchProvider     = (*FirecrawlProvider)(nil)
	_ KindSearcher       = (*FirecrawlProvider)(nil)
	_ FetchProvider      = (*FirecrawlProvider)(nil)
	_ CrawlProvider      = (*FirecrawlProvider)(nil)
	_ TranscribeProvider = (*FirecrawlProvider)(nil)
	_ ParseProvider      = (*FirecrawlProvider)(nil)
)
