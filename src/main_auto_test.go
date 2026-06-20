package main

import "testing"

func TestAutoCandidatesPriorityFirstThenDefaults(t *testing.T) {
	// priority pushes brave to the front; defaults fill the rest; no dups;
	// "auto" itself is dropped.
	got := autoCandidates("search", []string{"brave", "auto"})
	if len(got) == 0 || got[0] != "brave" {
		t.Fatalf("brave should lead, got %v", got)
	}
	seen := map[string]int{}
	for _, n := range got {
		seen[n]++
		if n == "auto" {
			t.Fatalf("auto must not be a candidate: %v", got)
		}
	}
	for n, c := range seen {
		if c != 1 {
			t.Fatalf("%q appears %d times: %v", n, c, got)
		}
	}
	// every default search provider must be present somewhere
	for _, n := range defaultAutoChains["search"] {
		if seen[n] == 0 {
			t.Fatalf("default %q missing from candidates %v", n, got)
		}
	}
}
