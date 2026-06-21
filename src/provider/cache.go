package provider

import (
	"context"
	"encoding/json"
	"time"

	"github.com/rishang/seek/cache"
	"github.com/rishang/seek/config"
)

// The decorators below transparently cache fetch and crawl results. A cache
// hit short-circuits the network call; a miss falls through and stores the
// fresh result. Crawl results are JSON-encoded into the entry's content; fetch
// stores the raw page content with its format. (Search is not cached.)

type cachingFetch struct {
	FetchProvider
	store    *cache.Store
	provider string
	ttl      time.Duration
}

func (c cachingFetch) Fetch(ctx context.Context, url string, opts config.FetchOptions) (*config.FetchResult, error) {
	key := cache.Key{Op: "fetch", Provider: c.provider, URL: url, Format: string(opts.OutputFormat)}
	if entry, ok, _ := c.store.Get(ctx, key); ok {
		return &config.FetchResult{URL: url, Content: entry.Content, Format: entry.Format, Cached: true}, nil
	}

	result, err := c.FetchProvider.Fetch(ctx, url, opts)
	if err != nil {
		return nil, err
	}
	_ = c.store.Set(ctx, key, result.Content, c.ttl)
	return result, nil
}

// Attempts forwards the wrapped provider's auto attempts (nil when the wrapped
// provider is not an auto meta-provider), so the CLI can report failover even
// through the cache decorator.
func (c cachingFetch) Attempts() []Attempt {
	if ar, ok := c.FetchProvider.(AutoReporter); ok {
		return ar.Attempts()
	}
	return nil
}

type cachingCrawl struct {
	CrawlProvider
	store    *cache.Store
	provider string
	ttl      time.Duration
}

func (c cachingCrawl) Crawl(ctx context.Context, url string) (*config.CrawlResult, error) {
	key := cache.Key{Op: "crawl", Provider: c.provider, URL: url}
	if entry, ok, _ := c.store.Get(ctx, key); ok {
		var result config.CrawlResult
		if json.Unmarshal([]byte(entry.Content), &result) == nil {
			return &result, nil
		}
	}

	result, err := c.CrawlProvider.Crawl(ctx, url)
	if err != nil {
		return nil, err
	}
	if blob, err := json.Marshal(result); err == nil {
		_ = c.store.Set(ctx, key, string(blob), c.ttl)
	}
	return result, nil
}
