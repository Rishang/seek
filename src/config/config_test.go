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
	if !cfg.Scrape.Cache.IsEnabled() {
		t.Fatal("expected caching enabled by default")
	}
	if cfg.Scrape.Options.OutputFormat != FormatMarkdown {
		t.Fatalf("expected default format markdown, got %q", cfg.Scrape.Options.OutputFormat)
	}
}

func TestLoadOverlaysOntoDefaults(t *testing.T) {
	path := writeConfig(t, `
config:
  scrape:
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
	if cfg.Scrape.Provider != "lightpanda" {
		t.Fatalf("scrape provider = %q", cfg.Scrape.Provider)
	}
	if cfg.Scrape.Cache.IsEnabled() {
		t.Fatal("expected scrape caching disabled")
	}
	if got := cfg.Scrape.Cache.TTL().Seconds(); got != 60 {
		t.Fatalf("ttl = %v, want 60s", got)
	}
	if cfg.Scrape.Options.OutputFormat != FormatHTML {
		t.Fatalf("format = %q", cfg.Scrape.Options.OutputFormat)
	}

	// Untouched operations keep their defaults; crawl caching stays enabled.
	if cfg.Search.Provider != "auto" {
		t.Fatalf("search default not preserved: %+v", cfg.Search)
	}
	if cfg.Crawl.Provider != "firecrawl" || !cfg.Crawl.Cache.IsEnabled() {
		t.Fatalf("crawl defaults not preserved: %+v", cfg.Crawl)
	}
}
