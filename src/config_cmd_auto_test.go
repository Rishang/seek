package main

import (
	"testing"

	"github.com/rishang/seek/config"
)

func TestValidateProviderAcceptsAuto(t *testing.T) {
	if err := validateProvider("search", "auto", append([]string{"auto"}, searchProviders...)); err != nil {
		t.Errorf("search auto should validate: %v", err)
	}
}

func TestConfiguredNamesReturnsOnlyProvidersWithCreds(t *testing.T) {
	creds := map[string]config.Credential{
		"exa":        {APIKey: "k"},
		"lightpanda": {Host: "https://example"}, // host-only still counts
		"brave":      {},                        // empty: excluded
	}
	got := configuredNames(creds)

	want := map[string]bool{"exa": true, "lightpanda": true}
	if len(got) != len(want) {
		t.Fatalf("got %v, want keys %v", got, want)
	}
	for _, n := range got {
		if !want[n] {
			t.Fatalf("unexpected provider %q in %v", n, got)
		}
	}
	// Order must follow providerEnv declaration order (lightpanda before exa).
	if got[0] != "lightpanda" || got[1] != "exa" {
		t.Fatalf("expected providerEnv order, got %v", got)
	}
}

func TestPickDefaultFallsBackWhenInvalid(t *testing.T) {
	opts := []string{"auto", "exa"}
	if got := pickDefault("exa", opts, "auto"); got != "exa" {
		t.Fatalf("valid current should be kept, got %q", got)
	}
	if got := pickDefault("firecrawl", opts, "auto"); got != "auto" {
		t.Fatalf("invalid current should fall back, got %q", got)
	}
}

// TestApplyProviderSelectionDropsDeselected verifies that de-selecting a
// provider in the init form removes its stored credential, while selected
// providers are upserted from the form inputs.
func TestApplyProviderSelectionDropsDeselected(t *testing.T) {
	creds := map[string]config.Credential{
		"exa":        {APIKey: "old-exa"},
		"brave":      {APIKey: "old-brave"}, // will be de-selected -> dropped
		"lightpanda": {Host: "https://old"}, // host-only, de-selected -> dropped
	}
	str := func(s string) *string { return &s }
	keyVals := map[string]*string{
		"exa":        str("new-exa"),
		"brave":      str("old-brave"),
		"lightpanda": str(""),
		"tavily":     str("new-tavily"), // newly selected
	}
	hostVals := map[string]*string{"lightpanda": str("https://old")}
	selected := map[string]bool{"exa": true, "tavily": true}

	applyProviderSelection(creds, selected, keyVals, hostVals)

	if creds["exa"].APIKey != "new-exa" {
		t.Errorf("exa key should be updated, got %q", creds["exa"].APIKey)
	}
	if creds["tavily"].APIKey != "new-tavily" {
		t.Errorf("tavily should be added, got %q", creds["tavily"].APIKey)
	}
	if _, ok := creds["brave"]; ok {
		t.Error("de-selected brave should be dropped")
	}
	if _, ok := creds["lightpanda"]; ok {
		t.Error("de-selected lightpanda should be dropped")
	}
}

// TestCapableSubsetScopesToConfigured verifies the init settings dropdowns are
// limited to the configured providers, preserving capability order.
func TestCapableSubsetScopesToConfigured(t *testing.T) {
	// searchProviders order is firecrawl, tavily, spider.cloud, brave, exa,
	// perplexity. Configure two of them (out of order) plus a non-search provider.
	configured := map[string]bool{"exa": true, "firecrawl": true, "webcrawlerapi": true}
	got := capableSubset(searchProviders, configured)
	// webcrawlerapi isn't search-capable so it's dropped; order follows
	// searchProviders (firecrawl before exa), not the configured set.
	want := []string{"firecrawl", "exa"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %v, want %v", got, want)
	}
	// Nothing crawl-capable configured -> empty (no crawl default offered).
	if c := capableSubset(crawlProviders, map[string]bool{"exa": true}); len(c) != 0 {
		t.Fatalf("expected no crawl options, got %v", c)
	}
}

// TestKeyGroupHideClosuresCaptureName replicates runInitForm's per-provider
// hide-func construction to guard against a closure-capture regression (every
// closure seeing the last loop value), which would make init prompt for the
// wrong number of provider keys. Each group must be visible iff its own
// provider is selected.
func TestKeyGroupHideClosuresCaptureName(t *testing.T) {
	selected := []string{"firecrawl", "tavily"}
	selectedSet := func() map[string]bool {
		m := make(map[string]bool, len(selected))
		for _, n := range selected {
			m[n] = true
		}
		return m
	}

	hides := map[string]func() bool{}
	for _, p := range providerEnv {
		name := p.Name
		hides[name] = func() bool { return !selectedSet()[name] }
	}

	visible := 0
	for _, p := range providerEnv {
		hidden := hides[p.Name]()
		wantHidden := !selectedSet()[p.Name]
		if hidden != wantHidden {
			t.Fatalf("%s: hidden=%v, want %v (closure captured wrong name)", p.Name, hidden, wantHidden)
		}
		if !hidden {
			visible++
		}
	}
	if visible != len(selected) {
		t.Fatalf("expected %d visible key groups, got %d", len(selected), visible)
	}
}
