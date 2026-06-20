package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/rishang/seek/cache"
	"github.com/rishang/seek/config"
	"github.com/rishang/seek/logx"
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
	factory.SetAutoChain("search", autoCandidates("search", cfg.Search.Priority))
	factory.SetAutoChain("scrape", autoCandidates("scrape", cfg.Scrape.Priority))

	store, err := setupCache()
	if err != nil {
		logx.Warn("cache disabled: %v", err)
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
		logx.Error("%v", err)
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

// autoCandidates builds the ordered candidate list the "auto" provider draws
// from for an operation: the optional config priority hint first, then the
// built-in default ranking, then the providerEnv order as a safety net. Names
// are de-duplicated (first occurrence wins); empties and "auto" are dropped.
// The factory filters this list to configured + capable providers.
func autoCandidates(op string, priority []string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(names []string) {
		for _, n := range names {
			if n == "" || n == "auto" || seen[n] {
				continue
			}
			seen[n] = true
			out = append(out, n)
		}
	}
	add(priority)
	add(defaultAutoChains[op])
	env := make([]string, len(providerEnv))
	for i, p := range providerEnv {
		env[i] = p.Name
	}
	add(env)
	return out
}

// logAutoAttempts surfaces auto-provider failover: a Warn per failed provider
// and a Debug for the one that served. No-op when p is not an auto provider.
func logAutoAttempts(p any) {
	ar, ok := p.(provider.AutoReporter)
	if !ok {
		return
	}
	for _, a := range ar.Attempts() {
		if a.Err != nil {
			logx.Warn("auto: %s failed: %v", a.Provider, a.Err)
		} else {
			logx.Debug("auto: served by %s", a.Provider)
		}
	}
}

func searchCmd() *cli.Command {
	return &cli.Command{
		Name:      "search",
		Usage:     "Run a web search",
		UsageText: "seek search [-p provider] [--start DD/MM/YYYY] [--end DD/MM/YYYY] [--range N] <query>",
		Flags: []cli.Flag{
			providerFlag("Provider: auto (default), firecrawl, tavily, spider.cloud, brave, exa"),
			&cli.StringFlag{Name: "start", Usage: "Only results published on/after this date (DD/MM/YYYY)"},
			&cli.StringFlag{Name: "end", Usage: "Only results published on/before this date (DD/MM/YYYY)"},
			&cli.IntFlag{Name: "range", Usage: "Only results from the last N days (today back N days)"},
			outputFlag,
			noCacheFlag,
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() < 1 {
				return fmt.Errorf("query required")
			}
			query := cmd.Args().First()
			applyNoCache(cmd)

			opts, err := searchOptions(cmd)
			if err != nil {
				return err
			}

			name := providerFor(cmd, cfg.Search.Provider)
			sp, err := factory.Search(name)
			if err != nil {
				return err
			}
			if !opts.TimeRange.IsZero() {
				if tr, ok := sp.(provider.TimeRangeSearcher); !ok || !tr.SupportsTimeRange() {
					logx.Warn("provider %q does not support a search time range; ignoring --start/--end/--range", name)
				}
			}
			results, err := sp.Search(ctx, query, opts)
			logAutoAttempts(sp)
			if err != nil {
				return err
			}
			return render(cmd, results)
		},
	}
}

// dmyLayout is the date format accepted by --start and --end.
const dmyLayout = "02/01/2006"

// parseDMY parses a DD/MM/YYYY date.
func parseDMY(s string) (time.Time, error) {
	return time.Parse(dmyLayout, s)
}

// searchOptions builds the search-time options from the --start, --end, and
// --range flags. --range sets both bounds (today back N days); explicit
// --start/--end override the corresponding bound.
func searchOptions(cmd *cli.Command) (config.SearchOptions, error) {
	var tr config.TimeRange

	if cmd.IsSet("range") {
		n := int(cmd.Int("range"))
		if n <= 0 {
			return config.SearchOptions{}, fmt.Errorf("--range must be a positive number of days")
		}
		now := time.Now()
		tr.Start = now.AddDate(0, 0, -n)
		tr.End = now
	}
	if cmd.IsSet("start") {
		t, err := parseDMY(cmd.String("start"))
		if err != nil {
			return config.SearchOptions{}, fmt.Errorf("invalid --start %q: expected DD/MM/YYYY", cmd.String("start"))
		}
		tr.Start = t
	}
	if cmd.IsSet("end") {
		t, err := parseDMY(cmd.String("end"))
		if err != nil {
			return config.SearchOptions{}, fmt.Errorf("invalid --end %q: expected DD/MM/YYYY", cmd.String("end"))
		}
		tr.End = t
	}
	if !tr.Start.IsZero() && !tr.End.IsZero() && tr.End.Before(tr.Start) {
		return config.SearchOptions{}, fmt.Errorf("--end (%s) is before --start (%s)",
			tr.End.Format(dmyLayout), tr.Start.Format(dmyLayout))
	}
	if !tr.IsZero() {
		logx.Debug("search time range: start=%s end=%s",
			fmtDateOrOpen(tr.Start), fmtDateOrOpen(tr.End))
	}
	return config.SearchOptions{TimeRange: tr}, nil
}

func fmtDateOrOpen(t time.Time) string {
	if t.IsZero() {
		return "(open)"
	}
	return t.Format(dmyLayout)
}

func scrapeCmd() *cli.Command {
	return &cli.Command{
		Name:      "scrape",
		Usage:     "Extract content from a URL",
		UsageText: "seek scrape [-p provider] [-f format] <url>",
		Flags: []cli.Flag{
			providerFlag("Provider: auto (default), firecrawl, tavily, spider.cloud, webcrawlerapi, lightpanda, exa"),
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
			logAutoAttempts(sp)
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
		logx.Warn("config: %v", err)
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
		logx.Warn("invalid SEEK_CACHE_TTL %q, ignoring", v)
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
	{"exa", "EXA_API_KEY"},
}

// loadProviders builds the factory, taking each API key from provider.yaml and
// letting the matching environment variable override it when set.
func loadProviders() *provider.Factory {
	creds, err := config.LoadProviders(providersPath())
	if err != nil {
		logx.Warn("providers: %v", err)
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
