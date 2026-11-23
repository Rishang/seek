package provider

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/rishang/seek/config"
)

// JinaProvider supports search (s.jina.ai) and fetch (r.jina.ai) via Jina Reader.
// Docs: https://jina.ai/reader (OpenAPI: https://r.jina.ai/openapi.json)
//
// Reader works without a key at a low IP-based rate limit, so the provider
// builds requests directly and only sends Authorization when a key is set.
type JinaProvider struct {
	*httpClient
	host string // optional override of the reader base URL (self-hosted Reader)
}

// JinaReaderProvider is the fetch-only form of Jina used when no API key is
// configured, because s.jina.ai rejects keyless requests.
type JinaReaderProvider struct {
	p *JinaProvider
}

// jinaReaderBase and jinaSearchBase are vars so tests can point them at a local server.
var (
	jinaReaderBase = "https://r.jina.ai"
	jinaSearchBase = "https://s.jina.ai"
)

// NewJinaProvider returns the fetch-only reader when cfg has a host but no key,
// and the full search+fetch provider otherwise.
func NewJinaProvider(cfg config.ProviderConfig) Provider {
	p := &JinaProvider{httpClient: newHTTPClient("jina", cfg.APIKey), host: cfg.Host}
	if cfg.APIKey == "" && cfg.Host != "" {
		return &JinaReaderProvider{p: p}
	}
	return p
}

// ---- response types ----

type jinaPage struct {
	Title         string `json:"title"`
	Description   string `json:"description"`
	URL           string `json:"url"`
	Content       string `json:"content"`
	HTML          string `json:"html"`
	PublishedTime string `json:"publishedTime"`
}

type jinaFetchResponse struct {
	Data jinaPage `json:"data"`
}

type jinaSearchResponse struct {
	Data []jinaPage `json:"data"`
}

func (p *JinaProvider) send(ctx context.Context, op, method, url string, query map[string]string, headers map[string]string, body, out any) error {
	r := p.client.R().
		SetContext(ctx).
		SetHeader("Accept", "application/json").
		SetQueryParams(query).
		SetHeaders(headers).
		SetSuccessResult(out)
	if p.apiKey != "" {
		r.SetHeader("Authorization", "Bearer "+p.apiKey)
	}
	if body != nil {
		r.SetHeader("Content-Type", "application/json").SetBody(body)
	}
	resp, err := r.Send(method, url)
	if err != nil {
		return fmt.Errorf("jina %s request failed: %w", op, err)
	}
	return p.expectOK(op, resp)
}

// ---- Search ----

// SupportsKind: s.jina.ai takes type=news; its image results are not page-shaped.
func (p *JinaProvider) SupportsKind(kind string) bool { return kind == config.KindNews }

func (p *JinaProvider) Search(ctx context.Context, query string, opts config.SearchOptions) ([]config.SearchResult, error) {
	q := map[string]string{"q": query, "num": "10"}
	if opts.Kind == config.KindNews {
		q["type"] = "news"
	}
	// Without no-content, s.jina.ai reads every result page before responding.
	headers := map[string]string{"X-Respond-With": "no-content"}

	var resp jinaSearchResponse
	if err := p.send(ctx, "search", http.MethodGet, jinaSearchBase+"/search", q, headers, nil, &resp); err != nil {
		return nil, err
	}

	results := make([]config.SearchResult, len(resp.Data))
	for i, d := range resp.Data {
		snippet := d.Description
		if snippet == "" {
			snippet = d.Content
		}
		results[i] = config.SearchResult{
			Title:         d.Title,
			URL:           d.URL,
			Snippet:       strings.TrimSpace(snippet),
			PublishedDate: d.PublishedTime,
		}
	}
	return results, nil
}

// ---- Fetch ----

func (p *JinaProvider) Fetch(ctx context.Context, url string, opts config.FetchOptions) (*config.FetchResult, error) {
	base := p.host
	if base == "" {
		base = jinaReaderBase
	}
	headers := map[string]string{}
	format := "markdown"
	if opts.OutputFormat == config.FormatHTML {
		format = "html"
		headers["X-Respond-With"] = "html"
	}

	var resp jinaFetchResponse
	body := map[string]string{"url": url}
	if err := p.send(ctx, "fetch", http.MethodPost, strings.TrimRight(base, "/")+"/", nil, headers, body, &resp); err != nil {
		return nil, err
	}

	content := resp.Data.Content
	if format == "html" {
		content = resp.Data.HTML
	}
	if content == "" {
		return nil, fmt.Errorf("jina fetch returned no content for %s", url)
	}
	return &config.FetchResult{URL: url, Content: content, Format: format}, nil
}

func (r *JinaReaderProvider) Fetch(ctx context.Context, url string, opts config.FetchOptions) (*config.FetchResult, error) {
	return r.p.Fetch(ctx, url, opts)
}

var (
	_ SearchProvider = (*JinaProvider)(nil)
	_ KindSearcher   = (*JinaProvider)(nil)
	_ FetchProvider  = (*JinaProvider)(nil)
	_ FetchProvider  = (*JinaReaderProvider)(nil)
)
