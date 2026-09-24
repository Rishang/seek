package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Search.Provider != "auto" {
		t.Fatalf("expected default provider auto, got %q", cfg.Search.Provider)
	}
	if !cfg.Fetch.Cache.IsEnabled() {
		t.Fatal("expected caching enabled by default")
	}
	if cfg.Fetch.Options.OutputFormat != FormatMarkdown {
		t.Fatalf("expected default format markdown, got %q", cfg.Fetch.Options.OutputFormat)
	}
}

func TestLoadOverlaysOntoDefaults(t *testing.T) {
	path := writeConfig(t, `
config:
  fetch:
    provider: lightpanda
    cache:
      enabled: false
      ttl: 60
    options:
      output_format: html
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Overridden fields.
	if cfg.Fetch.Provider != "lightpanda" {
		t.Fatalf("fetch provider = %q", cfg.Fetch.Provider)
	}
	if cfg.Fetch.Cache.IsEnabled() {
		t.Fatal("expected fetch caching disabled")
	}
	if got := cfg.Fetch.Cache.TTL().Seconds(); got != 60 {
		t.Fatalf("ttl = %v, want 60s", got)
	}
	if cfg.Fetch.Options.OutputFormat != FormatHTML {
		t.Fatalf("format = %q", cfg.Fetch.Options.OutputFormat)
	}

	// Untouched operations keep their defaults; crawl caching stays enabled.
	if cfg.Search.Provider != "auto" {
		t.Fatalf("search default not preserved: %+v", cfg.Search)
	}
	if cfg.Crawl.Provider != "firecrawl" || !cfg.Crawl.Cache.IsEnabled() {
		t.Fatalf("crawl defaults not preserved: %+v", cfg.Crawl)
	}
}

func TestSaveProvidersPreservesAgent(t *testing.T) {
	path := writeConfig(t, `
providers:
  brave:
    api_key: old
agent:
  base_url: http://localhost:8080/v1
  model: big
  extract_model: small
`)
	if err := SaveProviders(path, map[string]Credential{"exa": {APIKey: "k"}}); err != nil {
		t.Fatalf("SaveProviders: %v", err)
	}
	a, err := LoadAgent(path)
	if err != nil {
		t.Fatalf("LoadAgent: %v", err)
	}
	if a.BaseURL != "http://localhost:8080/v1" || a.Model != "big" || a.ExtractModel != "small" {
		t.Fatalf("agent section not preserved: %+v", a)
	}
}
