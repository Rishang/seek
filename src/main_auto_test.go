package main

import "testing"

func TestAutoCandidatesFiltersByCapabilityInPriorityOrder(t *testing.T) {
	// Global priority lists providers across all capabilities; for "search" only
	// search-capable ones survive, in priority order. webcrawlerapi/lightpanda
	// are fetch/crawl-only and must be dropped.
	priority := []string{"webcrawlerapi", "exa", "lightpanda", "brave", "auto", "exa"}
	got := autoCandidates("search", priority)

	for _, n := range got {
		if n == "auto" {
			t.Fatalf("auto must not be a candidate: %v", got)
		}
		if !contains(searchProviders, n) {
			t.Fatalf("%q is not search-capable but appeared: %v", n, got)
		}
	}
	// Priority order respected for the capable, listed providers.
	if len(got) < 2 || got[0] != "exa" || got[1] != "brave" {
		t.Fatalf("expected exa then brave to lead, got %v", got)
	}
	// No duplicates.
	seen := map[string]int{}
	for _, n := range got {
		seen[n]++
		if seen[n] > 1 {
			t.Fatalf("%q duplicated: %v", n, got)
		}
	}
	// Safety net: every search-capable provider appears even if absent from priority.
	for _, n := range searchProviders {
		if seen[n] == 0 {
			t.Fatalf("capable provider %q missing from candidates %v", n, got)
		}
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
