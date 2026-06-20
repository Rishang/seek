package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/rishang/seek/cache"
	"github.com/rishang/seek/config"
	"github.com/rishang/seek/provider"
	"github.com/urfave/cli/v3"
)

var (
	factory *provider.Factory
	cfg     config.Config
)

// noCacheFlag bypasses the result cache for a single request. Shared across the
// search, scrape, and crawl commands.
var noCacheFlag = &cli.BoolFlag{
	Name:  "no-cache",
	Usage: "Bypass the result cache for this request",
}

// providerFlag is reused (with operation-specific usage) by every command.
func providerFlag(usage string) *cli.StringFlag {
	return &cli.StringFlag{Name: "provider", Aliases: []string{"p"}, Usage: usage}
}

func main() {
	cfg = loadConfig()
	factory = loadProviders()

	store, err := setupCache()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: cache disabled: %v\n", err)
	}
	if store != nil {
		defer store.Close()
	}

	cmd := &cli.Command{
		Name:        "seek",
		Usage:       "The OpenRouter for web search",
		Description: "Run web search, scrape, and crawl across pluggable providers.\n\nDocs: " + readmeURL,
		Commands: []*cli.Command{
			searchCmd(),
			scrapeCmd(),
			crawlCmd(),
			configCmd(),
		},
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// applyNoCache disables caching for the current invocation when --no-cache is
// set. Safe because exactly one command runs per process.
func applyNoCache(cmd *cli.Command) {
	if cmd.Bool("no-cache") {
		factory.DisableCache()
	}
}

// providerFor returns the --provider flag value, falling back to the
// operation's configured default when the flag is not set.
func providerFor(cmd *cli.Command, fallback string) string {
	if cmd.IsSet("provider") {
		return cmd.String("provider")
	}
	return fallback
}

func searchCmd() *cli.Command {
	return &cli.Command{
		Name:      "search",
		Usage:     "Run a web search",
		UsageText: "seek search [-p provider] <query>",
		Flags: []cli.Flag{
			providerFlag("Provider: firecrawl, tavily, spider.cloud, brave"),
			outputFlag,
			noCacheFlag,
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() < 1 {
				return fmt.Errorf("query required")
			}
			query := cmd.Args().First()
			applyNoCache(cmd)

			sp, err := factory.Search(providerFor(cmd, cfg.Search.Provider))
			if err != nil {
				return err
			}
			results, err := sp.Search(ctx, query)
			if err != nil {
				return err
			}
			return render(cmd, results)
		},
	}
}

func scrapeCmd() *cli.Command {
	return &cli.Command{
		Name:      "scrape",
		Usage:     "Extract content from a URL",
		UsageText: "seek scrape [-p provider] [-f format] <url>",
		Flags: []cli.Flag{
			providerFlag("Provider: firecrawl, tavily, spider.cloud, webcrawlerapi, lightpanda"),
			&cli.StringFlag{
				Name:    "format",
				Aliases: []string{"f"},
				Usage:   "Page content format: markdown, html, json",
			},
			noCacheFlag,
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() < 1 {
				return fmt.Errorf("url required")
			}
			url := cmd.Args().First()
			applyNoCache(cmd)

			outFormat := cfg.Scrape.Options.OutputFormat
			if cmd.IsSet("format") {
				outFormat = parseFormat(cmd.String("format"))
			}

			sp, err := factory.Scrape(providerFor(cmd, cfg.Scrape.Provider))
			if err != nil {
				return err
			}
			result, err := sp.Scrape(ctx, url, config.ScrapeOptions{OutputFormat: outFormat})
			if err != nil {
				return err
			}
			// Emit the raw page content in its requested format (markdown by
			// default); the URL/format envelope would only get in the way.
			fmt.Println(result.Content)
			return nil
		},
	}
}

func crawlCmd() *cli.Command {
	return &cli.Command{
		Name:      "crawl",
		Usage:     "Crawl a website",
		UsageText: "seek crawl [-p provider] <url>",
		Flags: []cli.Flag{
			providerFlag("Provider: firecrawl, tavily, spider.cloud, webcrawlerapi"),
			outputFlag,
			noCacheFlag,
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() < 1 {
				return fmt.Errorf("url required")
			}
			url := cmd.Args().First()
			applyNoCache(cmd)

			cp, err := factory.Crawl(providerFor(cmd, cfg.Crawl.Provider))
			if err != nil {
				return err
			}
			result, err := cp.Crawl(ctx, url)
			if err != nil {
				return err
			}
			return render(cmd, result)
		},
	}
}

// loadConfig reads the config file (SEEK_CONFIG or the default path), falling
// back to built-in defaults on error.
func loadConfig() config.Config {
	path := os.Getenv("SEEK_CONFIG")
	if path == "" {
		path = config.DefaultPath()
	}
	c, err := config.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: config: %v\n", err)
		return config.Default()
	}
	return c
}

// setupCache opens the cache store (when enabled) and registers it for every
// operation whose config enables caching. SEEK_CACHE=off disables everything.
func setupCache() (cache.Store, error) {
	if os.Getenv("SEEK_CACHE") == "off" {
		return nil, nil
	}

	// Only scrape and crawl are cached; search always hits the provider.
	ops := map[string]config.CacheConfig{
		"scrape": cfg.Scrape.Cache,
		"crawl":  cfg.Crawl.Cache,
	}
	enabled := false
	for _, c := range ops {
		enabled = enabled || c.IsEnabled()
	}
	if !enabled {
		return nil, nil
	}

	path := os.Getenv("SEEK_CACHE_DB")
	if path == "" {
		path = cache.DefaultPath()
	}
	store, err := cache.OpenSQLite(path)
	if err != nil {
		return nil, err
	}

	for op, c := range ops {
		if c.IsEnabled() {
			factory.SetCache(op, store, ttlFor(c))
		}
	}
	return store, nil
}

// ttlFor resolves an operation's TTL: SEEK_CACHE_TTL (global override) wins,
// then the per-operation config value, then the built-in default.
func ttlFor(c config.CacheConfig) time.Duration {
	if v := os.Getenv("SEEK_CACHE_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
		fmt.Fprintf(os.Stderr, "warning: invalid SEEK_CACHE_TTL %q, ignoring\n", v)
	}
	if d := c.TTL(); d > 0 {
		return d
	}
	return cache.DefaultTTL
}

func parseFormat(s string) config.ScrapeOutputFormat {
	switch s {
	case "html":
		return config.FormatHTML
	case "json":
		return config.FormatJSON
	default:
		return config.FormatMarkdown
	}
}

// providerEnv maps each provider to the environment variable that overrides its
// stored API key. It is the single source of truth for known providers.
var providerEnv = []struct{ Name, Env string }{
	{"firecrawl", "FIRECRAWL_API_KEY"},
	{"tavily", "TAVILY_API_KEY"},
	{"spider.cloud", "SPIDER_API_KEY"},
	{"webcrawlerapi", "WEBCRAWLERAPI_API_KEY"},
	{"lightpanda", "LIGHTPANDA_API_KEY"},
	{"brave", "BRAVE_API_KEY"},
}

// loadProviders builds the factory, taking each API key from provider.yaml and
// letting the matching environment variable override it when set.
func loadProviders() *provider.Factory {
	creds, err := config.LoadProviders(providersPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: providers: %v\n", err)
		creds = map[string]config.Credential{}
	}

	providers := make([]config.ProviderConfig, 0, len(providerEnv))
	for _, p := range providerEnv {
		pc := config.ProviderConfig{Name: p.Name}
		if c, ok := creds[p.Name]; ok {
			pc.APIKey, pc.Host = c.APIKey, c.Host
		}
		if v := os.Getenv(p.Env); v != "" {
			pc.APIKey = v
		}
		providers = append(providers, pc)
	}
	return provider.NewFactory(providers)
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
