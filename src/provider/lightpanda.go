package provider

import (
	"context"
	"strings"

	"github.com/rishang/seek/config"
)

// LightpandaProvider supports scrape only, via Lightpanda's HTTP fetch API.
// Lightpanda is a headless browser; the cloud endpoint renders the page and
// returns its content as html or markdown.
//
// Docs: https://lightpanda.io/docs/usage/api
type LightpandaProvider struct {
	*httpClient
	host string // optional override of the fetch endpoint (self-hosted)
}

// lightpandaDefaultHost is the default cloud base URL.
const lightpandaDefaultHost = "https://euwest.cloud.lightpanda.io"

func NewLightpandaProvider(cfg config.ProviderConfig) *LightpandaProvider {
	return &LightpandaProvider{
		httpClient: newHTTPClient("lightpanda", cfg.APIKey),
		host:       cfg.Host,
	}
}

func (p *LightpandaProvider) Name() string { return "lightpanda" }

func (p *LightpandaProvider) fetchURL() string {
	base := p.host
	if base == "" {
		base = lightpandaDefaultHost
	}
	return strings.TrimRight(base, "/") + "/api/fetch"
}

type lpFetchRequest struct {
	URL          string `json:"url"`
	OutputFormat string `json:"output_format"` // "html" | "markdown"
}

type lpFetchResponse struct {
	Data   string `json:"data"`
	Status int    `json:"status"`
}

// Scrape renders targetURL and returns its content. Lightpanda only supports
// html and markdown; any other requested format falls back to markdown.
func (p *LightpandaProvider) Scrape(ctx context.Context, targetURL string, opts config.ScrapeOptions) (*config.ScrapeResult, error) {
	format := "markdown"
	if opts.OutputFormat == config.FormatHTML {
		format = "html"
	}

	body := lpFetchRequest{URL: targetURL, OutputFormat: format}
	var resp lpFetchResponse
	if err := p.post(ctx, "fetch", p.fetchURL(), &body, &resp); err != nil {
		return nil, err
	}

	return &config.ScrapeResult{
		URL:     targetURL,
		Content: resp.Data,
		Format:  format,
	}, nil
}

var _ ScrapeProvider = (*LightpandaProvider)(nil)
