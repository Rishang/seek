package provider

import "testing"

// TestRegistryCapabilitiesMatchInterfaces guards the derived capability matrix:
// each provider's Caps must reflect exactly the capability interfaces its type
// implements. This is the safety net that lets the rest of the codebase treat
// the registry as the single source of truth.
func TestRegistryCapabilitiesMatchInterfaces(t *testing.T) {
	want := map[string]Capability{
		"firecrawl":     CapSearch | CapFetch | CapCrawl,
		"tavily":        CapSearch | CapFetch | CapCrawl,
		"spider.cloud":  CapSearch | CapFetch | CapCrawl,
		"webcrawlerapi": CapFetch | CapCrawl,
		"lightpanda":    CapFetch,
		"brave":         CapSearch,
		"exa":           CapSearch | CapFetch,
		"perplexity":    CapSearch | CapFetch,
	}

	got := Providers()
	if len(got) != len(want) {
		t.Fatalf("registry has %d providers, expected %d", len(got), len(want))
	}
	for _, in := range got {
		w, ok := want[in.Name]
		if !ok {
			t.Errorf("unexpected provider %q in registry", in.Name)
			continue
		}
		if in.Caps != w {
			t.Errorf("%s: caps = %b, want %b", in.Name, in.Caps, w)
		}
	}
}

func TestNamesForFiltersByCapability(t *testing.T) {
	search := NamesFor(CapSearch)
	if !contains(search, "perplexity") || !contains(search, "brave") {
		t.Errorf("search providers missing expected entries: %v", search)
	}
	for _, n := range NamesFor(CapCrawl) {
		// Only firecrawl/tavily/spider.cloud/webcrawlerapi crawl; none of the
		// search-only providers should appear.
		if n == "brave" || n == "exa" || n == "perplexity" || n == "lightpanda" {
			t.Errorf("%q is not crawl-capable but appeared in %v", n, NamesFor(CapCrawl))
		}
	}
}

func TestHostDefaultOnlyForSelfHostable(t *testing.T) {
	if HostDefault("firecrawl") == "" || HostDefault("lightpanda") == "" {
		t.Error("firecrawl and lightpanda should have a host default")
	}
	if HostDefault("perplexity") != "" {
		t.Error("cloud-only providers should have no host default")
	}
	if HostDefault("nonexistent") != "" {
		t.Error("unknown provider should have no host default")
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
