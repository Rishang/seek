package provider

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/rishang/seek/cache"
	"github.com/rishang/seek/config"
)

// stubFetch records how many times the underlying provider is hit.
type stubFetch struct{ calls int }

func (s *stubFetch) Fetch(_ context.Context, url string, opts config.FetchOptions) (*config.FetchResult, error) {
	s.calls++
	return &config.FetchResult{URL: url, Content: "fresh", Format: string(opts.OutputFormat)}, nil
}

func newStore(t *testing.T) cache.Store {
	t.Helper()
	store, err := cache.OpenSQLite(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestCachingFetchHitMiss(t *testing.T) {
	ctx := context.Background()
	stub := &stubFetch{}
	dec := cachingFetch{
		FetchProvider: stub,
		store:          newStore(t),
		provider:       "firecrawl",
		ttl:            time.Hour,
	}
	opts := config.FetchOptions{OutputFormat: config.FormatMarkdown}

	// First call: miss -> hits the underlying provider and caches.
	if _, err := dec.Fetch(ctx, "https://example.com", opts); err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	// Second call: hit -> served from cache, no extra provider call.
	res, err := dec.Fetch(ctx, "https://example.com", opts)
	if err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if stub.calls != 1 {
		t.Fatalf("expected 1 underlying call, got %d", stub.calls)
	}
	if res.Content != "fresh" {
		t.Fatalf("unexpected cached content %q", res.Content)
	}

	// A different format is a distinct cache key -> another provider call.
	if _, err := dec.Fetch(ctx, "https://example.com", config.FetchOptions{OutputFormat: config.FormatHTML}); err != nil {
		t.Fatalf("html fetch: %v", err)
	}
	if stub.calls != 2 {
		t.Fatalf("expected 2 underlying calls after format change, got %d", stub.calls)
	}
}
