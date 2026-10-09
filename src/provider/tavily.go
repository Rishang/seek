package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rishang/seek/config"
)

// TavilyProvider supports search and crawl via Tavily.
// Docs: https://docs.tavily.com
type TavilyProvider struct {
	*httpClient
}

func NewTavilyProvider(cfg config.ProviderConfig) *TavilyProvider {
	return &TavilyProvider{httpClient: newHTTPClient("tavily", cfg.APIKey)}
}

// ---- request / response types ----

type tvSearchRequest struct {
	Query             string   `json:"query"`
	SearchDepth       string   `json:"search_depth,omitempty"`   // "basic" | "advanced"
	Topic             string   `json:"topic,omitempty"`          // "general" | "news"
	IncludeAnswer     string   `json:"include_answer,omitempty"` // "basic" | "advanced"
	IncludeRawContent bool     `json:"include_raw_content,omitempty"`
	IncludeImages     bool     `json:"include_images,omitempty"`
	IncludeImageDesc  bool     `json:"include_image_descriptions,omitempty"`
	MaxResults        int      `json:"max_results,omitempty"`
	IncludeDomains    []string `json:"include_domains,omitempty"`
	ExcludeDomains    []string `json:"exclude_domains,omitempty"`
	Days              int      `json:"days,omitempty"`
	StartDate         string   `json:"start_date,omitempty"` // YYYY-MM-DD
	EndDate           string   `json:"end_date,omitempty"`   // YYYY-MM-DD
}

type tvSearchResponse struct {
	Query        string     `json:"query"`
	Answer       string     `json:"answer"`
	Results      []tvResult `json:"results"`
	Images       []tvImage  `json:"images"`
	ResponseTime float64    `json:"response_time"`
}

type tvResult struct {
	Title         string  `json:"title"`
	URL           string  `json:"url"`
	Content       string  `json:"content"`
	Score         float64 `json:"score"`
	RawContent    *string `json:"raw_content"`
	PublishedDate string  `json:"published_date"` // populated for news results
}

// tvImage is a top-level search image. Tavily returns bare URL strings, or
// {url, description} objects when include_image_descriptions is set; accept both.
type tvImage struct {
	URL         string `json:"url"`
	Description string `json:"description"`
}

func (i *tvImage) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &i.URL)
	}
	type plain tvImage // drop the method to avoid recursion
	return json.Unmarshal(b, (*plain)(i))
}

type tvExtractRequest struct {
	URLs          []string `json:"urls"`
	ExtractDepth  string   `json:"extract_depth,omitempty"` // "basic" | "advanced"
	IncludeImages bool     `json:"include_images,omitempty"`
	Format        string   `json:"format,omitempty"` // "markdown" | "text"
}

type tvExtractResponse struct {
	Results       []tvExtractResult `json:"results"`
	FailedResults []tvFailedResult  `json:"failed_results"`
	ResponseTime  float64           `json:"response_time"`
}

type tvExtractResult struct {
	URL        string   `json:"url"`
	RawContent string   `json:"raw_content"`
	Images     []string `json:"images"`
}

type tvFailedResult struct {
	URL   string `json:"url"`
	Error string `json:"error"`
}

type tvCrawlRequest struct {
	URL           string   `json:"url"`
	MaxDepth      int      `json:"max_depth,omitempty"`
	MaxBreadth    int      `json:"max_breadth,omitempty"`
	Limit         int      `json:"limit,omitempty"`
	Query         string   `json:"query,omitempty"`
	SelectPaths   []string `json:"select_paths,omitempty"`
	SelectDomains []string `json:"select_domains,omitempty"`
	AllowExternal bool     `json:"allow_external,omitempty"`
	IncludeImages bool     `json:"include_images,omitempty"`
	ExtractDepth  string   `json:"extract_depth,omitempty"`
}

type tvCrawlResponse struct {
	BaseURL      string          `json:"base_url"`
	Results      []tvCrawlResult `json:"results"`
	ResponseTime float64         `json:"response_time"`
}

type tvCrawlResult struct {
	URL        string   `json:"url"`
	RawContent string   `json:"raw_content"`
	Images     []string `json:"images"`
}

// ---- Search ----

func (p *TavilyProvider) SupportsTimeRange() bool { return true }

func (p *TavilyProvider) SupportsKind(kind string) bool {
	return kind == config.KindNews || kind == config.KindImages
}

// tavilyBase is a var so tests can point it at a local server.
var tavilyBase = "https://api.tavily.com"

func (p *TavilyProvider) Search(ctx context.Context, query string, opts config.SearchOptions) ([]config.SearchResult, error) {
	body := tvSearchRequest{
		Query:       query,
		SearchDepth: "basic",
		MaxResults:  10,
		StartDate:   ymd(opts.TimeRange.Start),
		EndDate:     ymd(opts.TimeRange.End),
	}
	switch opts.Kind {
	case config.KindNews:
		body.Topic = "news"
	case config.KindImages:
		body.IncludeImages, body.IncludeImageDesc = true, true
	}

	var resp tvSearchResponse
	if err := p.post(ctx, "search", tavilyBase+"/search", &body, &resp); err != nil {
		return nil, err
	}

	// Images come back as a query-level list without a source page, so URL
	// and ImageURL are both the image.
	if opts.Kind == config.KindImages {
		results := make([]config.SearchResult, len(resp.Images))
		for i, img := range resp.Images {
			results[i] = config.SearchResult{Title: img.Description, URL: img.URL, ImageURL: img.URL}
		}
		return results, nil
	}

	results := make([]config.SearchResult, len(resp.Results))
	for i, item := range resp.Results {
		results[i] = config.SearchResult{
			Title:         item.Title,
			URL:           item.URL,
			Snippet:       item.Content,
			PublishedDate: item.PublishedDate,
		}
	}
	return results, nil
}

// ---- Fetch (Tavily Extract) ----

func (p *TavilyProvider) Fetch(ctx context.Context, url string, opts config.FetchOptions) (*config.FetchResult, error) {
	format := "markdown"
	if opts.OutputFormat == config.FormatHTML {
		format = "text" // Tavily doesn't have HTML output; use text
	}

	body := tvExtractRequest{
		URLs:         []string{url},
		ExtractDepth: "basic",
		Format:       format,
	}

	var resp tvExtractResponse
	if err := p.post(ctx, "extract", "https://api.tavily.com/extract", &body, &resp); err != nil {
		return nil, err
	}

	if len(resp.Results) == 0 {
		if len(resp.FailedResults) > 0 {
			return nil, fmt.Errorf("tavily extract failed for %s: %s", url, resp.FailedResults[0].Error)
		}
		return nil, fmt.Errorf("tavily extract returned no results for %s", url)
	}

	return &config.FetchResult{
		URL:     url,
		Content: resp.Results[0].RawContent,
		Format:  string(opts.OutputFormat),
	}, nil
}

// ---- Transcribe ----

// Transcribe extracts the video page as markdown: for YouTube, tavily's extract
// includes a timestamped "### Transcript" section when captions exist (no ASR
// fallback), alongside the title, description, and chapters.
func (p *TavilyProvider) Transcribe(ctx context.Context, url, _ string) (*config.FetchResult, error) {
	return p.Fetch(ctx, url, config.FetchOptions{OutputFormat: config.FormatMarkdown})
}

// ---- Crawl ----

func (p *TavilyProvider) Crawl(ctx context.Context, url string) (*config.CrawlResult, error) {
	body := tvCrawlRequest{
		URL:        url,
		MaxDepth:   2,
		MaxBreadth: 10,
		Limit:      30,
	}

	var resp tvCrawlResponse
	if err := p.post(ctx, "crawl", "https://api.tavily.com/crawl", &body, &resp); err != nil {
		return nil, err
	}

	pages := make([]string, len(resp.Results))
	var allContent string
	for i, item := range resp.Results {
		pages[i] = item.URL
		allContent += item.RawContent + "\n\n"
	}

	return &config.CrawlResult{
		URL:     url,
		Pages:   pages,
		Content: allContent,
	}, nil
}

var (
	_ SearchProvider     = (*TavilyProvider)(nil)
	_ KindSearcher       = (*TavilyProvider)(nil)
	_ FetchProvider      = (*TavilyProvider)(nil)
	_ CrawlProvider      = (*TavilyProvider)(nil)
	_ TranscribeProvider = (*TavilyProvider)(nil)
)
