package provider

import (
	"fmt"
	"time"

	"github.com/rishang/seek/cache"
	"github.com/rishang/seek/config"
)

// opCache holds the cache settings for a single operation.
type opCache struct {
	store *cache.Store
	ttl   time.Duration
}

// Factory creates and holds Provider instances keyed by name. Caching is
// configured per operation; when an operation has a cache, its capability
// accessor returns a cache-wrapped provider.
type Factory struct {
	providers map[string]Provider
	caches    map[string]opCache  // keyed by "search" | "fetch" | "crawl"
	chains    map[string][]string // auto candidate order, keyed by "search" | "fetch"
}

// NewFactory builds providers from the given list of ProviderConfig entries.
func NewFactory(providers []config.ProviderConfig) *Factory {
	f := &Factory{
		providers: make(map[string]Provider, len(providers)),
		caches:    make(map[string]opCache, 3),
		chains:    make(map[string][]string, 2),
	}
	for _, pc := range providers {
		if pc.APIKey == "" && pc.Host == "" {
			continue // unconfigured: no key and no host
		}
		if r, ok := byName[pc.Name]; ok {
			f.providers[pc.Name] = r.new(pc)
		}
		// Unknown provider: skipped silently (caller can check existence).
	}
	return f
}

// SetCache enables caching for a single operation ("search", "fetch", or
// "crawl") using the given store and TTL.
func (f *Factory) SetCache(op string, store *cache.Store, ttl time.Duration) {
	f.caches[op] = opCache{store: store, ttl: ttl}
}

// DisableCache turns off caching for every operation.
func (f *Factory) DisableCache() { f.caches = make(map[string]opCache) }

// SetAutoChain stores the ordered candidate provider names the "auto" provider
// considers for an operation ("search" | "fetch"). The factory filters these
// to configured + capable providers when building the meta-provider.
func (f *Factory) SetAutoChain(op string, names []string) { f.chains[op] = names }

// Get returns the raw Provider by name, or nil if not configured.
func (f *Factory) Get(name string) Provider {
	return f.providers[name]
}

// Search returns a provider that supports search, or an error. Search results
// are not cached. The name "auto" builds a failover chain.
func (f *Factory) Search(name string) (SearchProvider, error) {
	if name == "auto" {
		return f.autoSearch()
	}
	return capability[SearchProvider](f, name, "search")
}

// autoSearch builds the search failover chain from the stored candidates,
// keeping only configured + capable providers (order preserved).
func (f *Factory) autoSearch() (SearchProvider, error) {
	var chain []autoSearchEntry
	for _, n := range f.chains["search"] {
		if sp, err := capability[SearchProvider](f, n, "search"); err == nil {
			chain = append(chain, autoSearchEntry{name: n, sp: sp})
		}
	}
	if len(chain) == 0 {
		return nil, fmt.Errorf("provider %q: no configured provider supports search", "auto")
	}
	return newAutoSearch(chain), nil
}

// Fetch returns a provider that supports fetch, or an error. The name "auto"
// builds a failover chain.
func (f *Factory) Fetch(name string) (FetchProvider, error) {
	var (
		sp  FetchProvider
		err error
	)
	if name == "auto" {
		sp, err = f.autoFetch()
	} else {
		sp, err = capability[FetchProvider](f, name, "fetch")
	}
	if err != nil {
		return nil, err
	}
	if c, ok := f.caches["fetch"]; ok && c.store != nil {
		return cachingFetch{FetchProvider: sp, store: c.store, provider: name, ttl: c.ttl}, nil
	}
	return sp, nil
}

// autoFetch builds the fetch failover chain from the stored candidates,
// keeping only configured + capable providers (order preserved).
func (f *Factory) autoFetch() (FetchProvider, error) {
	var chain []autoFetchEntry
	for _, n := range f.chains["fetch"] {
		if sp, err := capability[FetchProvider](f, n, "fetch"); err == nil {
			chain = append(chain, autoFetchEntry{name: n, sp: sp})
		}
	}
	if len(chain) == 0 {
		return nil, fmt.Errorf("provider %q: no configured provider supports fetch", "auto")
	}
	return newAutoFetch(chain), nil
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
