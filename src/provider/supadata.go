package provider

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rishang/seek/config"
)

// SupadataProvider supports fetch, crawl, map, parse, and transcribe via the Supadata API.
// Docs: https://docs.supadata.ai
//
// Transcribe accepts any video page URL (YouTube, TikTok, Instagram, X,
// Facebook) or a direct media file; native captions are used first, with AI
// transcription as the fallback. Auth is an x-api-key header, so requests
// bypass the shared bearer helpers.
type SupadataProvider struct {
	*httpClient
}

func NewSupadataProvider(cfg config.ProviderConfig) *SupadataProvider {
	return &SupadataProvider{httpClient: newHTTPClient("supadata", cfg.APIKey)}
}

// supadataBase is a var so tests can point it at a local server.
var supadataBase = "https://api.supadata.ai/v1"

// supadataPollInterval is how often an async job is polled (a var so tests can shorten it).
var supadataPollInterval = 2 * time.Second

// ---- response types ----

// supadataTranscript covers both the immediate result (200) and the job flow:
// a 202 carries JobID, and polling returns Status until "completed".
type supadataTranscript struct {
	Content string `json:"content"` // plain text, since we request text=true
	Lang    string `json:"lang"`
	JobID   string `json:"jobId"`
	Status  string `json:"status"` // queued | active | completed | failed
	Error   any    `json:"error"`
}

type supadataPage struct {
	URL     string `json:"url"`
	Content string `json:"content"` // markdown
	Name    string `json:"name"`
}

type supadataCrawlStatus struct {
	Status string         `json:"status"` // scraping | completed | failed | cancelled
	Pages  []supadataPage `json:"pages"`
	Next   string         `json:"next"` // absolute URL of the next results page, when more remain
}

// ---- Fetch ----

// Fetch scrapes a page. Supadata only returns markdown, so the requested
// output format is ignored. It parses PDFs but passes Office files (docx,
// xlsx) through as raw zip bytes, which are rejected so auto fails over.
func (p *SupadataProvider) Fetch(ctx context.Context, url string, _ config.FetchOptions) (*config.FetchResult, error) {
	var page supadataPage
	if _, err := p.do(ctx, "fetch", http.MethodGet, supadataBase+"/web/scrape", map[string]string{"url": url}, nil, &page); err != nil {
		return nil, err
	}
	if looksBinary(page.Content) {
		return nil, fmt.Errorf("supadata fetch: %s returned unparsed binary content", url)
	}
	return &config.FetchResult{URL: url, Content: page.Content, Format: string(config.FormatMarkdown)}, nil
}

// Parse converts a document URL to markdown. PDFs only; Office files come back as raw
// bytes and are rejected by Fetch.
func (p *SupadataProvider) Parse(ctx context.Context, url string) (*config.FetchResult, error) {
	return p.Fetch(ctx, url, config.FetchOptions{OutputFormat: config.FormatMarkdown})
}

// ---- Crawl ----

func (p *SupadataProvider) Crawl(ctx context.Context, url string) (*config.CrawlResult, error) {
	var start struct {
		JobID string `json:"jobId"`
	}
	body := map[string]any{"url": url, "limit": 100}
	if _, err := p.do(ctx, "crawl start", http.MethodPost, supadataBase+"/web/crawl", nil, body, &start); err != nil {
		return nil, err
	}

	next := supadataBase + "/web/crawl/" + start.JobID
	result := &config.CrawlResult{URL: url}
	var content strings.Builder
	for next != "" {
		var st supadataCrawlStatus
		if _, err := p.do(ctx, "crawl poll", http.MethodGet, next, nil, nil, &st); err != nil {
			return nil, err
		}
		switch st.Status {
		case "failed", "cancelled":
			return nil, fmt.Errorf("supadata crawl job %s %s", start.JobID, st.Status)
		case "completed":
			for _, pg := range st.Pages {
				result.Pages = append(result.Pages, pg.URL)
				content.WriteString(pg.Content + "\n\n")
			}
			next = st.Next // follow pagination; "" ends the loop
			continue
		}
		if err := sleepCtx(ctx, supadataPollInterval); err != nil {
			return nil, fmt.Errorf("supadata crawl: %w", err)
		}
	}
	result.Content = content.String()
	return result, nil
}

// ---- Transcribe ----

func (p *SupadataProvider) Transcribe(ctx context.Context, url, lang string) (*config.FetchResult, error) {
	params := map[string]string{"url": url, "text": "true"}
	if lang != "" {
		params["lang"] = lang
	}
	var resp supadataTranscript
	code, err := p.do(ctx, "transcribe", http.MethodGet, supadataBase+"/transcript", params, nil, &resp)
	if err != nil {
		return nil, err
	}

	// Long videos are processed asynchronously: poll the job until it settles.
	for code == http.StatusAccepted || (resp.JobID != "" && resp.Status != "completed") {
		if resp.Status == "failed" {
			return nil, fmt.Errorf("supadata transcribe job failed: %v", resp.Error)
		}
		if err := sleepCtx(ctx, supadataPollInterval); err != nil {
			return nil, fmt.Errorf("supadata transcribe: %w", err)
		}
		jobID := resp.JobID
		resp = supadataTranscript{}
		if code, err = p.do(ctx, "transcribe", http.MethodGet, supadataBase+"/transcript/"+jobID, nil, nil, &resp); err != nil {
			return nil, err
		}
		resp.JobID = jobID
	}

	return &config.FetchResult{URL: url, Content: resp.Content, Format: "text"}, nil
}

// ---- Map ----

// Map lists a site's URLs. Supadata takes no limit, so it is applied here.
func (p *SupadataProvider) Map(ctx context.Context, url string, limit int) ([]config.MapLink, error) {
	var resp struct {
		URLs []string `json:"urls"`
	}
	if _, err := p.do(ctx, "map", http.MethodGet, supadataBase+"/web/map", map[string]string{"url": url}, nil, &resp); err != nil {
		return nil, err
	}
	return urlLinks(resp.URLs, limit), nil
}

// do issues an x-api-key authenticated request and accepts 200 or 202,
// returning the status so callers can detect async jobs.
func (p *SupadataProvider) do(ctx context.Context, op, method, endpoint string, params map[string]string, body, out any) (int, error) {
	r := p.client.R().
		SetContext(ctx).
		SetHeader("x-api-key", p.apiKey).
		SetQueryParams(params).
		SetSuccessResult(out)
	if body != nil {
		r.SetBodyJsonMarshal(body)
	}
	resp, err := r.Send(method, endpoint)
	if err != nil {
		return 0, fmt.Errorf("supadata %s request failed: %w", op, err)
	}
	if resp.StatusCode == http.StatusAccepted {
		return resp.StatusCode, nil
	}
	return resp.StatusCode, p.expectOK(op, resp)
}

// looksBinary reports whether s is undecoded file bytes rather than text: more
// than 5% of its first 1 KiB is U+FFFD or control characters.
// ponytail: sampling heuristic; switch to a content-type check if it misfires.
func looksBinary(s string) bool {
	var bad, n int
	for _, r := range s {
		if n == 1024 {
			break
		}
		n++
		if r == utf8.RuneError || (r < 0x20 && r != '\n' && r != '\r' && r != '\t') {
			bad++
		}
	}
	return n > 0 && bad*20 > n
}

// sleepCtx waits d or until ctx is done.
func sleepCtx(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// Compile-time interface checks.
var (
	_ MapProvider        = (*SupadataProvider)(nil)
	_ FetchProvider      = (*SupadataProvider)(nil)
	_ CrawlProvider      = (*SupadataProvider)(nil)
	_ TranscribeProvider = (*SupadataProvider)(nil)
	_ ParseProvider      = (*SupadataProvider)(nil)
)
