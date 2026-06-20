package provider

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/imroc/req/v3"
)

// httpClient wraps a req.Client with the bearer auth and JSON request/response
// handling shared by every HTTP-based provider.
type httpClient struct {
	name   string // provider name, used in error messages
	apiKey string
	client *req.Client
}

func newHTTPClient(name, apiKey string) *httpClient {
	return &httpClient{
		name:   name,
		apiKey: apiKey,
		client: req.C().SetTimeout(120 * time.Second),
	}
}

// post sends an authenticated JSON POST, decodes a 200 response into out and
// returns a descriptive error for transport failures or non-200 statuses.
func (c *httpClient) post(ctx context.Context, op, url string, body, out any) error {
	resp, err := c.request(ctx, op, http.MethodPost, url, body, out)
	if err != nil {
		return err
	}
	return c.expectOK(op, resp)
}

// get sends an authenticated GET, decoding a 200 response into out.
func (c *httpClient) get(ctx context.Context, op, url string, out any) error {
	resp, err := c.request(ctx, op, http.MethodGet, url, nil, out)
	if err != nil {
		return err
	}
	return c.expectOK(op, resp)
}

// request performs the HTTP call without asserting the status code, letting
// callers handle non-200 responses (e.g. accepted-then-poll flows).
func (c *httpClient) request(ctx context.Context, op, method, url string, body, out any) (*req.Response, error) {
	r := c.client.R().
		SetContext(ctx).
		SetHeader("Authorization", "Bearer "+c.apiKey)
	if body != nil {
		r.SetHeader("Content-Type", "application/json").SetBody(body)
	}
	if out != nil {
		r.SetSuccessResult(out)
	}

	var resp *req.Response
	var err error
	if method == http.MethodGet {
		resp, err = r.Get(url)
	} else {
		resp, err = r.Post(url)
	}
	if err != nil {
		return nil, fmt.Errorf("%s %s request failed: %w", c.name, op, err)
	}
	return resp, nil
}

func (c *httpClient) expectOK(op string, resp *req.Response) error {
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s %s returned status %d: %s", c.name, op, resp.StatusCode, resp.String())
	}
	return nil
}
