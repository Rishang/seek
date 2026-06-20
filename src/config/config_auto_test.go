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

func TestPriorityOmittedWhenEmpty(t *testing.T) {
	out, err := marshalYAML(file{Config: Default()})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "priority") {
		t.Errorf("default config should not emit a priority key:\n%s", out)
	}
}

func TestPriorityRoundTrips(t *testing.T) {
	const in = `
config:
  search:
    provider: auto
    priority: [brave, exa]
`
	var f file
	if err := yaml.Unmarshal([]byte(in), &f); err != nil {
		t.Fatal(err)
	}
	got := f.Config.Search.Priority
	if len(got) != 2 || got[0] != "brave" || got[1] != "exa" {
		t.Errorf("priority round-trip: got %v", got)
	}
}
