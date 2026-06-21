package provider

import "github.com/rishang/seek/config"

// This file is the single source of truth for which providers exist, how they
// authenticate (env var + optional self-host default), and what they can do.
// Capabilities are *derived* from the interfaces each provider implements, so
// the matrix can never drift from the code: add a method, gain a capability.
//
// Consumers (the CLI's config init, auto-chain ordering, key prompts) read this
// via the accessors below instead of maintaining their own provider lists.

// Capability is a bitmask of the operations a provider supports.
type Capability uint8

const (
	CapSearch Capability = 1 << iota
	CapFetch
	CapCrawl
)

// Has reports whether the mask includes cap.
func (c Capability) Has(cap Capability) bool { return c&cap != 0 }

// Info is the public, derived description of a registered provider.
type Info struct {
	Name        string
	Env         string     // env var that overrides the stored API key
	HostDefault string     // managed-cloud base URL for self-hostable providers; "" otherwise
	Caps        Capability // derived from the implemented interfaces
}

// registration is the static declaration for one provider. new constructs an
// instance; capabilities are probed from it rather than declared here.
type registration struct {
	name        string
	env         string
	hostDefault string
	new         func(config.ProviderConfig) Provider
}

// registry lists every known provider in canonical order. This order is the
// display order in config init and the fallback ranking for the auto chain.
// To add a provider: implement it, then add one line here.
var registry = []registration{
	{"firecrawl", "FIRECRAWL_API_KEY", "https://api.firecrawl.dev", func(c config.ProviderConfig) Provider { return NewFirecrawlProvider(c) }},
	{"tavily", "TAVILY_API_KEY", "", func(c config.ProviderConfig) Provider { return NewTavilyProvider(c) }},
	{"spider.cloud", "SPIDER_API_KEY", "", func(c config.ProviderConfig) Provider { return NewSpiderProvider(c) }},
	{"webcrawlerapi", "WEBCRAWLERAPI_API_KEY", "", func(c config.ProviderConfig) Provider { return NewWebCrawlerAPIProvider(c) }},
	{"lightpanda", "LIGHTPANDA_API_KEY", "https://euwest.cloud.lightpanda.io", func(c config.ProviderConfig) Provider { return NewLightpandaProvider(c) }},
	{"brave", "BRAVE_API_KEY", "", func(c config.ProviderConfig) Provider { return NewBraveProvider(c) }},
	{"exa", "EXA_API_KEY", "", func(c config.ProviderConfig) Provider { return NewExaProvider(c) }},
	{"perplexity", "PERPLEXITY_API_KEY", "", func(c config.ProviderConfig) Provider { return NewPerplexityProvider(c) }},
}

// byName indexes the registry for construction lookups.
var byName = func() map[string]registration {
	m := make(map[string]registration, len(registry))
	for _, r := range registry {
		m[r.name] = r
	}
	return m
}()

// infos is the derived, public view of the registry, computed once at init.
// Capabilities come from probing a throwaway instance (constructors do no I/O),
// so they always match the interfaces the provider implements.
var infos = func() []Info {
	out := make([]Info, len(registry))
	for i, r := range registry {
		p := r.new(config.ProviderConfig{})
		var caps Capability
		if _, ok := p.(SearchProvider); ok {
			caps |= CapSearch
		}
		if _, ok := p.(FetchProvider); ok {
			caps |= CapFetch
		}
		if _, ok := p.(CrawlProvider); ok {
			caps |= CapCrawl
		}
		out[i] = Info{Name: r.name, Env: r.env, HostDefault: r.hostDefault, Caps: caps}
	}
	return out
}()

// Providers returns a copy of the registered providers in canonical order.
func Providers() []Info { return append([]Info(nil), infos...) }

// NamesFor returns the names of providers that support cap, in canonical order.
func NamesFor(cap Capability) []string {
	var out []string
	for _, in := range infos {
		if in.Caps.Has(cap) {
			out = append(out, in.Name)
		}
	}
	return out
}

// HostDefault returns the managed-cloud base URL for a self-hostable provider,
// or "" when the provider is not self-hostable / unknown.
func HostDefault(name string) string {
	for _, in := range infos {
		if in.Name == name {
			return in.HostDefault
		}
	}
	return ""
}
