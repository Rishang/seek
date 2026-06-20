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

func TestSelectedProvidersDropsAuto(t *testing.T) {
	c := config.Config{
		Search: config.Operation{Provider: "auto"},
		Scrape: config.Operation{Provider: "exa"},
		Crawl:  config.Operation{Provider: "firecrawl"},
	}
	for _, n := range selectedProviders(c) {
		if n == "auto" {
			t.Fatalf("selectedProviders must not include auto: %v", selectedProviders(c))
		}
	}
}

func TestAutoMembershipIsCapableSubset(t *testing.T) {
	got := autoMembership("search", map[string]config.Credential{})
	for _, n := range got {
		found := false
		for _, s := range searchProviders {
			if s == n {
				found = true
			}
		}
		if !found {
			t.Fatalf("%q is not a search provider", n)
		}
	}
	if len(got) == 0 {
		t.Fatal("expected some search providers")
	}
}
