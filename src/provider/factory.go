package provider

import (
	"fmt"
	"time"

	"github.com/rishang/seek/cache"
	"github.com/rishang/seek/config"
)

// opCache holds the cache settings for a single operation.
type opCache struct {
	store cache.Store
	ttl   time.Duration
}

// Factory creates and holds Provider instances keyed by name. Caching is
// configured per operation; when an operation has a cache, its capability
// accessor returns a cache-wrapped provider.
type Factory struct {
	providers map[string]Provider
	caches    map[string]opCache // keyed by "search" | "scrape" | "crawl"
}

// NewFactory builds providers from the given list of ProviderConfig entries.
func NewFactory(providers []config.ProviderConfig) *Factory {
	f := &Factory{
		providers: make(map[string]Provider, len(providers)),
		caches:    make(map[string]opCache, 3),
	}
	for _, pc := range providers {
		if pc.APIKey == "" && pc.Host == "" {
			continue // unconfigured: no key and no host
		}
		switch pc.Name {
		case "firecrawl":
			f.providers[pc.Name] = NewFirecrawlProvider(pc)
		case "tavily":
			f.providers[pc.Name] = NewTavilyProvider(pc)
		case "spider.cloud":
			f.providers[pc.Name] = NewSpiderProvider(pc)
		case "webcrawlerapi":
			f.providers[pc.Name] = NewWebCrawlerAPIProvider(pc)
		case "lightpanda":
			f.providers[pc.Name] = NewLightpandaProvider(pc)
		case "brave":
			f.providers[pc.Name] = NewBraveProvider(pc)
		case "exa":
			f.providers[pc.Name] = NewExaProvider(pc)
		default:
			// Unknown provider; skip silently (caller can check existence).
		}
	}
	return f
}

// SetCache enables caching for a single operation ("search", "scrape", or
// "crawl") using the given store and TTL.
func (f *Factory) SetCache(op string, store cache.Store, ttl time.Duration) {
	f.caches[op] = opCache{store: store, ttl: ttl}
}

// DisableCache turns off caching for every operation.
func (f *Factory) DisableCache() { f.caches = make(map[string]opCache) }

// Get returns the raw Provider by name, or nil if not configured.
func (f *Factory) Get(name string) Provider {
	return f.providers[name]
}

// Search returns a provider that supports search, or an error. Search results
// are not cached.
func (f *Factory) Search(name string) (SearchProvider, error) {
	return capability[SearchProvider](f, name, "search")
}

// Scrape returns a provider that supports scrape, or an error.
func (f *Factory) Scrape(name string) (ScrapeProvider, error) {
	sp, err := capability[ScrapeProvider](f, name, "scrape")
	if err != nil {
		return nil, err
	}
	if c, ok := f.caches["scrape"]; ok && c.store != nil {
		return cachingScrape{ScrapeProvider: sp, store: c.store, provider: name, ttl: c.ttl}, nil
	}
	return sp, nil
}

// Crawl returns a provider that supports crawl, or an error.
func (f *Factory) Crawl(name string) (CrawlProvider, error) {
	cp, err := capability[CrawlProvider](f, name, "crawl")
	if err != nil {
		return nil, err
	}
	if c, ok := f.caches["crawl"]; ok && c.store != nil {
		return cachingCrawl{CrawlProvider: cp, store: c.store, provider: name, ttl: c.ttl}, nil
	}
	return cp, nil
}

// capability looks up a provider by name and asserts it supports capability T,
// returning a descriptive error when it is unconfigured or unsupported.
func capability[T any](f *Factory, name, op string) (T, error) {
	var zero T
	p := f.Get(name)
	if p == nil {
		return zero, fmt.Errorf("provider %q not configured", name)
	}
	c, ok := p.(T)
	if !ok {
		return zero, fmt.Errorf("provider %q does not support %s", name, op)
	}
	return c, nil
}
