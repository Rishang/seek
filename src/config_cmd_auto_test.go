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
		"brave":      {},                         // empty: excluded
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

func TestFilterConfiguredKeepsCapableOrder(t *testing.T) {
	// searchProviders order is firecrawl, tavily, spider.cloud, brave, exa.
	got := filterConfigured(searchProviders, []string{"exa", "brave", "webcrawlerapi"})
	// webcrawlerapi isn't a search provider, so it's dropped; order follows
	// searchProviders, not the configured slice.
	want := []string{"brave", "exa"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %v, want %v", got, want)
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
