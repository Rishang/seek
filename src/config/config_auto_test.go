package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDefaultUsesAutoForSearchAndScrape(t *testing.T) {
	d := Default()
	if d.Search.Provider != "auto" {
		t.Errorf("search provider: want auto, got %q", d.Search.Provider)
	}
	if d.Scrape.Provider != "auto" {
		t.Errorf("scrape provider: want auto, got %q", d.Scrape.Provider)
	}
	if d.Crawl.Provider != "firecrawl" {
		t.Errorf("crawl provider: want firecrawl, got %q", d.Crawl.Provider)
	}
}

func TestDefaultHasBuiltinPriority(t *testing.T) {
	d := Default()
	if len(d.Priority) != len(DefaultPriority) {
		t.Fatalf("default priority: want %v, got %v", DefaultPriority, d.Priority)
	}
	for i, n := range DefaultPriority {
		if d.Priority[i] != n {
			t.Fatalf("default priority[%d]: want %q, got %q", i, n, d.Priority[i])
		}
	}
}

func TestSaveEmitsProvidersPriority(t *testing.T) {
	out, err := marshalYAML(file{Config: Default(), Providers: providersSection{Priority: Default().Priority}})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "providers:") || !strings.Contains(s, "priority:") {
		t.Errorf("expected a top-level providers.priority block:\n%s", s)
	}
}

func TestProvidersPriorityOverridesDefault(t *testing.T) {
	const in = `
config:
  search:
    provider: auto
providers:
  priority: [brave, exa]
`
	var f file
	if err := yaml.Unmarshal([]byte(in), &f); err != nil {
		t.Fatal(err)
	}
	got := f.Providers.Priority
	if len(got) != 2 || got[0] != "brave" || got[1] != "exa" {
		t.Errorf("providers.priority round-trip: got %v", got)
	}
}
