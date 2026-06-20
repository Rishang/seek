package provider

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/rishang/seek/config"
)

// WebCrawlerAPIProvider supports scrape and crawl via webcrawlerapi.com.
// Docs: https://webcrawlerapi.com/docs
type WebCrawlerAPIProvider struct {
	*httpClient
}

func NewWebCrawlerAPIProvider(cfg config.ProviderConfig) *WebCrawlerAPIProvider {
	return &WebCrawlerAPIProvider{httpClient: newHTTPClient("webcrawlerapi", cfg.APIKey)}
}

func (p *WebCrawlerAPIProvider) Name() string { return "webcrawlerapi" }

const wcaBaseURL = "https://api.webcrawlerapi.com"

// ---- request / response types ----

type wcaScrapeRequest struct {
	URL             string   `json:"url"`
	OutputFormats   []string `json:"output_formats"`
	MainContentOnly bool     `json:"main_content_only,omitempty"`
}

type wcaScrapeResponse struct {
	URL         string `json:"url"`
	Markdown    string `json:"markdown"`
	HTML        string `json:"html"`
	CleanedText string `json:"cleaned_text"`
}

type wcaCrawlRequest struct {
	URL             string   `json:"url"`
	OutputFormats   []string `json:"output_formats"`
	MainContentOnly bool     `json:"main_content_only,omitempty"`
	Limit           int      `json:"limit,omitempty"`
}

type wcaCrawlStartResponse struct {
	ID string `json:"id"`
}

type wcaCrawlStatusResponse struct {
	Status string              `json:"status"`
	Pages  []wcaScrapeResponse `json:"pages"`
}

// ---- Scrape ----

func (p *WebCrawlerAPIProvider) Scrape(ctx context.Context, url string, opts config.ScrapeOptions) (*config.ScrapeResult, error) {
	body := wcaScrapeRequest{
		URL:             url,
		OutputFormats:   []string{"markdown"},
		MainContentOnly: true,
	}

	var resp wcaScrapeResponse
	if err := p.post(ctx, "scrape", wcaBaseURL+"/v2/scrape", &body, &resp); err != nil {
		return nil, err
	}

	content := resp.Markdown
	if content == "" {
		content = resp.CleanedText
	}

	return &config.ScrapeResult{
		URL:     url,
		Content: content,
		Format:  string(opts.OutputFormat),
	}, nil
}

// ---- Crawl (async) ----

func (p *WebCrawlerAPIProvider) Crawl(ctx context.Context, url string) (*config.CrawlResult, error) {
	startBody := wcaCrawlRequest{
		URL:             url,
		OutputFormats:   []string{"markdown"},
		MainContentOnly: true,
		Limit:           100,
	}

	var startResp wcaCrawlStartResponse
	r, err := p.request(ctx, "crawl start", http.MethodPost, wcaBaseURL+"/v1/crawl", &startBody, &startResp)
	if err != nil {
		return nil, err
	}
	if r.StatusCode != http.StatusOK && r.StatusCode != http.StatusAccepted {
		return nil, fmt.Errorf("webcrawlerapi crawl start returned status %d: %s", r.StatusCode, r.String())
	}

	jobID := startResp.ID

	// Poll for completion
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			var statusResp wcaCrawlStatusResponse
			r, err := p.request(ctx, "crawl poll", http.MethodGet, wcaBaseURL+"/v1/crawl/"+jobID, nil, &statusResp)
			if err != nil {
				return nil, err
			}
			if r.StatusCode != http.StatusOK {
				continue
			}

			if statusResp.Status == "completed" || statusResp.Status == "done" {
				pages := make([]string, len(statusResp.Pages))
				var allContent string
				for i, page := range statusResp.Pages {
					pages[i] = page.URL
					content := page.Markdown
					if content == "" {
						content = page.CleanedText
					}
					allContent += content + "\n\n"
				}
				return &config.CrawlResult{
					URL:     url,
					Pages:   pages,
					Content: allContent,
				}, nil
			}
			if statusResp.Status == "failed" || statusResp.Status == "error" {
				return nil, fmt.Errorf("webcrawlerapi crawl job %s failed", jobID)
			}
		}
	}
}

var (
	_ ScrapeProvider = (*WebCrawlerAPIProvider)(nil)
	_ CrawlProvider  = (*WebCrawlerAPIProvider)(nil)
)
