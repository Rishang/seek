package provider

import (
	"context"
	"testing"

	"github.com/rishang/seek/config"
)

func TestFactoryAutoSearchFiltersToConfigured(t *testing.T) {
	// exa configured (key), brave not built (no key); chain lists both + a
	// fetch-only provider (webcrawlerapi) that must be filtered for search.
	f := NewFactory([]config.ProviderConfig{
		{Name: "exa", APIKey: "k"},
		{Name: "brave"}, // unconfigured -> not built -> skipped
	})
	f.SetAutoChain("search", []string{"brave", "exa", "webcrawlerapi"})

	sp, err := f.Search("auto")
	if err != nil {
		t.Fatalf("Search(auto): %v", err)
	}
	as, ok := sp.(*autoSearch)
	if !ok {
		t.Fatalf("want *autoSearch, got %T", sp)
	}
	if len(as.chain) != 1 || as.chain[0].name != "exa" {
		t.Fatalf("chain should be [exa], got %v", chainNames(as.chain))
	}
}

func chainNames(c []autoSearchEntry) []string {
	out := make([]string, len(c))
	for i, e := range c {
		out[i] = e.name
	}
	return out
}

func TestFactoryAutoSearchErrorsWhenChainEmpty(t *testing.T) {
	f := NewFactory(nil)
	f.SetAutoChain("search", []string{"exa", "brave"})
	if _, err := f.Search("auto"); err == nil {
		t.Fatal("want error when no configured provider supports search")
	}
}

func TestCachingFetchForwardsAttempts(t *testing.T) {
	inner := newAutoFetch([]autoFetchEntry{{"a", fakeFetch{name: "a", content: "x"}}})
	c := cachingFetch{FetchProvider: inner}
	if _, err := inner.Fetch(context.Background(), "http://x", config.FetchOptions{}); err != nil {
		t.Fatal(err)
	}
	ar, ok := any(c).(AutoReporter)
	if !ok {
		t.Fatal("cachingFetch should implement AutoReporter")
	}
	if len(ar.Attempts()) != 1 || ar.Attempts()[0].Provider != "a" {
		t.Fatalf("attempts not forwarded: %+v", ar.Attempts())
	}
}
