package provider

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/rishang/seek/config"
)

// SupadataProvider supports transcribe via the Supadata transcript API.
// Docs: https://docs.supadata.ai/api-reference/endpoint/transcript/transcript
//
// Accepts any video page URL (YouTube, TikTok, Instagram, X, Facebook) or a
// direct media file; native captions are used first, with AI transcription as
// the fallback. Auth is an x-api-key header, so requests bypass the shared
// bearer helpers.
type SupadataProvider struct {
	*httpClient
}

func NewSupadataProvider(cfg config.ProviderConfig) *SupadataProvider {
	return &SupadataProvider{httpClient: newHTTPClient("supadata", cfg.APIKey)}
}

// supadataBase is a var so tests can point it at a local server.
var supadataBase = "https://api.supadata.ai/v1/transcript"

// supadataPollInterval is how often an async job is polled (a var so tests can shorten it).
var supadataPollInterval = 2 * time.Second

// ---- response types ----

// supadataResponse covers both the immediate result (200) and the job flow:
// a 202 carries JobID, and polling returns Status until "completed".
type supadataResponse struct {
	Content string `json:"content"` // plain text, since we request text=true
	Lang    string `json:"lang"`
	JobID   string `json:"jobId"`
	Status  string `json:"status"` // queued | active | completed | failed
	Error   any    `json:"error"`
}

// ---- Transcribe ----

func (p *SupadataProvider) Transcribe(ctx context.Context, url, lang string) (*config.FetchResult, error) {
	params := map[string]string{"url": url, "text": "true"}
	if lang != "" {
		params["lang"] = lang
	}
	resp, code, err := p.call(ctx, supadataBase, params)
	if err != nil {
		return nil, err
	}

	// Long videos are processed asynchronously: poll the job until it settles.
	for code == http.StatusAccepted || (resp.JobID != "" && resp.Status != "completed") {
		if resp.Status == "failed" {
			return nil, fmt.Errorf("supadata transcribe job failed: %v", resp.Error)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("supadata transcribe: %w", ctx.Err())
		case <-time.After(supadataPollInterval):
		}
		jobID := resp.JobID
		if resp, code, err = p.call(ctx, supadataBase+"/"+jobID, nil); err != nil {
			return nil, err
		}
		resp.JobID = jobID
	}

	return &config.FetchResult{URL: url, Content: resp.Content, Format: "text"}, nil
}

// call issues an authenticated GET and accepts 200 or 202.
func (p *SupadataProvider) call(ctx context.Context, endpoint string, params map[string]string) (supadataResponse, int, error) {
	var out supadataResponse
	r, err := p.client.R().
		SetContext(ctx).
		SetHeader("x-api-key", p.apiKey).
		SetQueryParams(params).
		SetSuccessResult(&out).
		Get(endpoint)
	if err != nil {
		return out, 0, fmt.Errorf("supadata transcribe request failed: %w", err)
	}
	if r.StatusCode != http.StatusAccepted {
		if err := p.expectOK("transcribe", r); err != nil {
			return out, r.StatusCode, err
		}
	}
	return out, r.StatusCode, nil
}

var _ TranscribeProvider = (*SupadataProvider)(nil)
