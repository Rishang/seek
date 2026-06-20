// Package config defines the user configuration schema (config.yaml) and the
// value types shared across providers.
package config

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// marshalYAML encodes v as YAML with a 2-space indent.
func marshalYAML(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		_ = enc.Close()
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ProviderConfig holds credentials and endpoint for a single provider.
type ProviderConfig struct {
	Name   string `yaml:"name"`
	APIKey string `yaml:"api_key"`
	Host   string `yaml:"host,omitempty"` // for self-hosted / OSS providers
}

// Credential is a provider's stored secret, keyed by provider name in
// provider.yaml.
type Credential struct {
	APIKey string `yaml:"api_key,omitempty"`
	Host   string `yaml:"host,omitempty"` // for self-hosted / OSS providers
}

// providersFile is the on-disk shape of provider.yaml.
type providersFile struct {
	Providers map[string]Credential `yaml:"providers"`
}

// ProvidersPath is the default credentials file location (~/.seek/provider.yaml).
func ProvidersPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "provider.yaml"
	}
	return filepath.Join(home, ".seek", "provider.yaml")
}

// LoadProviders reads provider.yaml, returning an empty map when the file is
// absent.
func LoadProviders(path string) (map[string]Credential, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]Credential{}, nil
	}
	if err != nil {
		return nil, err
	}
	var f providersFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	if f.Providers == nil {
		f.Providers = map[string]Credential{}
	}
	return f.Providers, nil
}

// SaveProviders writes provider.yaml with 0600 permissions (it holds secrets).
func SaveProviders(path string, creds map[string]Credential) error {
	data, err := marshalYAML(providersFile{Providers: creds})
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, data, 0o600)
}

// Config is the top-level user configuration, nested under the "config" key in
// config.yaml. Each operation carries its own provider, cache, and options.
type Config struct {
	Search Operation `yaml:"search"`
	Scrape Operation `yaml:"scrape"`
	Crawl  Operation `yaml:"crawl"`
}

// Operation configures a single capability (search, scrape, or crawl).
type Operation struct {
	Provider string      `yaml:"provider"`
	Cache    CacheConfig `yaml:"cache,omitempty"`
	Options  Options     `yaml:"options,omitempty"`
}

// CacheConfig controls result caching for an operation.
type CacheConfig struct {
	Enabled *bool  `yaml:"enabled,omitempty"` // nil means the default (true)
	TTLSecs int    `yaml:"ttl,omitempty"`     // seconds; 0 means the default
	Store   string `yaml:"store,omitempty"`   // backend name, e.g. "sqlite"
}

// IsEnabled reports whether caching is on, defaulting to true when unset.
func (c CacheConfig) IsEnabled() bool { return c.Enabled == nil || *c.Enabled }

// TTL returns the configured TTL, or 0 when unset (the caller supplies the
// default in that case).
func (c CacheConfig) TTL() time.Duration { return time.Duration(c.TTLSecs) * time.Second }

// Options carries per-operation tunables. Currently only scrape uses it.
type Options struct {
	OutputFormat ScrapeOutputFormat `yaml:"output_format,omitempty"`
}

// ScrapeOutputFormat controls the response format for scrape requests.
type ScrapeOutputFormat string

const (
	FormatJSON     ScrapeOutputFormat = "json"
	FormatMarkdown ScrapeOutputFormat = "markdown"
	FormatHTML     ScrapeOutputFormat = "html"
)

// ScrapeOptions carries optional parameters for a scrape request.
type ScrapeOptions struct {
	OutputFormat ScrapeOutputFormat `yaml:"output_format,omitempty"`
}

// TimeRange is an inclusive published-date window for search results. A zero
// Start or End leaves that bound open.
type TimeRange struct {
	Start time.Time
	End   time.Time
}

// IsZero reports whether no time bound is set.
func (t TimeRange) IsZero() bool { return t.Start.IsZero() && t.End.IsZero() }

// SearchOptions carries optional parameters for a search request.
type SearchOptions struct {
	TimeRange TimeRange
}

// SearchResult represents a single result from a search provider.
type SearchResult struct {
	Title         string `json:"title"`
	URL           string `json:"url"`
	Snippet       string `json:"snippet"`
	PublishedDate string `json:"published_date,omitempty"`
}

// ScrapeResult holds the result of a scrape request.
type ScrapeResult struct {
	URL     string `json:"url"`
	Content string `json:"content"`
	Format  string `json:"format"`
}

// CrawlResult holds the result of a crawl request.
type CrawlResult struct {
	URL     string   `json:"url"`
	Pages   []string `json:"pages"`
	Content string   `json:"content"`
}

// Default returns the built-in configuration used when no file is present or to
// fill fields a file omits.
func Default() Config {
	// Each operation gets its own Enabled pointer; sharing one would let a file
	// that overrides a single operation's flag mutate the others through the
	// aliased pointer.
	enabledCache := func() CacheConfig {
		on := true
		return CacheConfig{Enabled: &on, Store: "sqlite"}
	}
	// Caching applies to scrape and crawl only; search has no cache config.
	return Config{
		Search: Operation{Provider: "firecrawl"},
		Scrape: Operation{Provider: "firecrawl", Cache: enabledCache(), Options: Options{OutputFormat: FormatMarkdown}},
		Crawl:  Operation{Provider: "firecrawl", Cache: enabledCache()},
	}
}

// DefaultPath is the default config file location (~/.seek/config.yaml).
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "config.yaml"
	}
	return filepath.Join(home, ".seek", "config.yaml")
}

// file is the on-disk wrapper: the schema is nested under a top-level "config".
type file struct {
	Config Config `yaml:"config"`
}

// Load reads config from path, overlaying any present fields onto Default(). A
// missing file is not an error — the defaults are returned.
func Load(path string) (Config, error) {
	f := file{Config: Default()}

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return f.Config, nil
	}
	if err != nil {
		return f.Config, err
	}
	if err := yaml.Unmarshal(data, &f); err != nil {
		return f.Config, err
	}
	return f.Config, nil
}

// Save writes the config to path, nested under the top-level "config" key,
// creating the parent directory as needed.
func Save(path string, c Config) error {
	data, err := marshalYAML(file{Config: c})
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, data, 0o644)
}
