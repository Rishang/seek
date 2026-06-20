package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/mattn/go-isatty"
	"github.com/rishang/seek/cache"
	"github.com/rishang/seek/config"
	"github.com/urfave/cli/v3"
)

// readmeURL is surfaced in help output so users can find the full docs.
const readmeURL = "https://github.com/rishang/seek#readme"

// Providers grouped by the capability they support. Reused by the config view
// and the init flag validation.
var (
	searchProviders = []string{"firecrawl", "tavily", "spider.cloud", "brave"}
	scrapeProviders = []string{"firecrawl", "tavily", "spider.cloud", "webcrawlerapi", "lightpanda"}
	crawlProviders  = []string{"firecrawl", "tavily", "spider.cloud", "webcrawlerapi"}
)

// providerHostDefaults lists the self-hostable (OSS) providers and the base URL
// of their managed cloud. init prompts for a host for these, defaulting to the
// cloud URL.
var providerHostDefaults = map[string]string{
	"firecrawl":  "https://api.firecrawl.dev",
	"lightpanda": "https://euwest.cloud.lightpanda.io",
}

func configCmd() *cli.Command {
	init := configInitCmd()
	return &cli.Command{
		Name:  "config",
		Usage: "Create or edit the configuration",
		Description: "Configure seek. With no subcommand this runs `init`.\n\n" +
			"  seek config init    Create or edit config and provider keys\n" +
			"  seek config view    Show the effective configuration\n\n" +
			"Docs: " + readmeURL,
		Flags:  init.Flags,
		Action: init.Action,
		Commands: []*cli.Command{
			init,
			configViewCmd(),
		},
	}
}

// configPath returns the path the config is (or would be) loaded from.
func configPath() string {
	if p := os.Getenv("SEEK_CONFIG"); p != "" {
		return p
	}
	return config.DefaultPath()
}

// providersPath returns the path provider.yaml is (or would be) loaded from.
func providersPath() string {
	if p := os.Getenv("SEEK_PROVIDERS"); p != "" {
		return p
	}
	return config.ProvidersPath()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// ---- config view ----

func configViewCmd() *cli.Command {
	return &cli.Command{
		Name:  "view",
		Usage: "Show the effective configuration",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			printEffectiveConfig(cfg, configPath())
			return nil
		},
	}
}

// printEffectiveConfig renders the resolved settings in a readable form.
func printEffectiveConfig(c config.Config, path string) {
	status := "found"
	if !fileExists(path) {
		status = "not found — using built-in defaults"
	}
	fmt.Printf("Config file: %s (%s)\n", path, status)
	fmt.Println()
	fmt.Println("Effective configuration:")
	printOp("search", c.Search, false, false)
	printOp("scrape", c.Scrape, true, true)
	printOp("crawl", c.Crawl, true, false)

	creds, _ := config.LoadProviders(providersPath())
	fmt.Printf("\n  api keys (%s)\n", providersPath())
	for _, p := range providerEnv {
		mark := "missing"
		if keyConfigured(p.Name, p.Env, creds) {
			mark = "set"
		}
		line := fmt.Sprintf("    %-14s key %s", p.Name, mark)
		if c, ok := creds[p.Name]; ok && c.Host != "" {
			line += fmt.Sprintf("   host %s", c.Host)
		}
		fmt.Println(line)
	}

	fmt.Println()
	fmt.Println("Edit:  seek config init")
}

// keyConfigured reports whether a provider has an API key from either the
// environment override or provider.yaml.
func keyConfigured(name, env string, creds map[string]config.Credential) bool {
	if os.Getenv(env) != "" {
		return true
	}
	c, ok := creds[name]
	return ok && c.APIKey != ""
}

func printOp(name string, op config.Operation, showCache, showFormat bool) {
	fmt.Printf("\n  %s\n", name)
	fmt.Printf("    provider   %s\n", orValue(op.Provider, "(none)"))
	if showCache {
		if op.Cache.IsEnabled() {
			fmt.Printf("    cache      on  (ttl %s, store %s)\n", humanTTL(op.Cache), orValue(op.Cache.Store, "sqlite"))
		} else {
			fmt.Println("    cache      off")
		}
	}
	if showFormat {
		fmt.Printf("    format     %s\n", orValue(string(op.Options.OutputFormat), "markdown"))
	}
}

func orValue(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// humanTTL formats an operation's effective cache TTL (whole days when even).
func humanTTL(c config.CacheConfig) string {
	d := c.TTL()
	if d <= 0 {
		d = cache.DefaultTTL
	}
	if d%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	}
	return d.String()
}

// ---- config init ----

func configInitCmd() *cli.Command {
	return &cli.Command{
		Name:      "init",
		Usage:     "Create or edit config and provider keys",
		UsageText: "seek config init            (interactive)\nseek config init --search brave --key brave=bsa-xxx  (flags)",
		Description: "With no flags on a terminal, launches an interactive form (and prompts\n" +
			"for API keys of the selected providers). Pass any setting flag, or run\n" +
			"without a TTY, for non-interactive mode.\n\n" +
			"Config is written to config.yaml; API keys go to provider.yaml (mode 0600).",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "path", Usage: "Config file path (default SEEK_CONFIG or ~/.seek/config.yaml)"},
			&cli.StringFlag{Name: "search", Usage: "Search provider"},
			&cli.StringFlag{Name: "scrape", Usage: "Scrape provider"},
			&cli.StringFlag{Name: "crawl", Usage: "Crawl provider"},
			&cli.StringFlag{Name: "format", Usage: "Scrape output format: markdown, html, json"},
			&cli.IntFlag{Name: "ttl", Usage: "Cache TTL in days (scrape & crawl)"},
			&cli.BoolFlag{Name: "cache", Value: true, Usage: "Enable scrape/crawl caching (use --cache=false to disable)"},
			&cli.StringFlag{Name: "store", Usage: "Cache backend (sqlite)"},
			&cli.StringSliceFlag{Name: "key", Usage: "Provider API key as name=value (repeatable)"},
			&cli.StringSliceFlag{Name: "host", Usage: "Provider host base URL as name=url (repeatable; OSS providers)"},
			&cli.BoolFlag{Name: "yes", Aliases: []string{"y"}, Usage: "Overwrite an existing file without prompting"},
		},
		Action: runConfigInit,
	}
}

// initValueFlags are the flags that, when set, switch init to non-interactive.
var initValueFlags = []string{"search", "scrape", "crawl", "format", "ttl", "cache", "store", "key", "host"}

func anyInitFlagSet(cmd *cli.Command) bool {
	for _, name := range initValueFlags {
		if cmd.IsSet(name) {
			return true
		}
	}
	return false
}

func runConfigInit(ctx context.Context, cmd *cli.Command) error {
	cfgPath := cmd.String("path")
	if cfgPath == "" {
		cfgPath = configPath()
	}
	provPath := providersPath()

	// Start from existing files when present so init edits rather than resets.
	c := config.Default()
	if fileExists(cfgPath) {
		if existing, err := config.Load(cfgPath); err == nil {
			c = existing
		}
	}
	creds, err := config.LoadProviders(provPath)
	if err != nil {
		return err
	}

	interactive := isatty.IsTerminal(os.Stdin.Fd()) && !anyInitFlagSet(cmd)
	if interactive {
		ok, err := runConfigForm(&c, cfgPath, cmd.Bool("yes"))
		if err != nil {
			return err
		}
		if !ok {
			fmt.Println("Cancelled; no changes written.")
			return nil
		}
		if err := runCredsForm(creds, selectedProviders(c)); err != nil {
			return err
		}
	} else {
		if err := applyInitFlags(cmd, &c, creds); err != nil {
			return err
		}
		if fileExists(cfgPath) && !cmd.Bool("yes") {
			return fmt.Errorf("%s already exists; pass --yes to overwrite", cfgPath)
		}
	}

	if err := config.Save(cfgPath, c); err != nil {
		return err
	}
	fmt.Printf("Wrote %s\n", cfgPath)

	pruneEmptyCreds(creds)
	if len(creds) > 0 {
		if err := config.SaveProviders(provPath, creds); err != nil {
			return err
		}
		fmt.Printf("Wrote %s\n", provPath)
	}
	return nil
}

// selectedProviders returns the distinct providers chosen across the operations.
func selectedProviders(c config.Config) []string {
	var out []string
	seen := map[string]bool{}
	for _, name := range []string{c.Search.Provider, c.Scrape.Provider, c.Crawl.Provider} {
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

func pruneEmptyCreds(creds map[string]config.Credential) {
	for name, c := range creds {
		if c.APIKey == "" && c.Host == "" {
			delete(creds, name)
		}
	}
}

// applyInitFlags overlays the non-interactive flag values onto c and creds.
func applyInitFlags(cmd *cli.Command, c *config.Config, creds map[string]config.Credential) error {
	if cmd.IsSet("search") {
		v := cmd.String("search")
		if err := validateProvider("search", v, searchProviders); err != nil {
			return err
		}
		c.Search.Provider = v
	}
	if cmd.IsSet("scrape") {
		v := cmd.String("scrape")
		if err := validateProvider("scrape", v, scrapeProviders); err != nil {
			return err
		}
		c.Scrape.Provider = v
	}
	if cmd.IsSet("crawl") {
		v := cmd.String("crawl")
		if err := validateProvider("crawl", v, crawlProviders); err != nil {
			return err
		}
		c.Crawl.Provider = v
	}
	if cmd.IsSet("format") {
		c.Scrape.Options.OutputFormat = parseFormat(cmd.String("format"))
	}
	if cmd.IsSet("ttl") {
		secs := int(cmd.Int("ttl")) * 86400
		c.Scrape.Cache.TTLSecs = secs
		c.Crawl.Cache.TTLSecs = secs
	}
	if cmd.IsSet("cache") {
		setCacheEnabled(&c.Scrape.Cache, cmd.Bool("cache"))
		setCacheEnabled(&c.Crawl.Cache, cmd.Bool("cache"))
	}
	if cmd.IsSet("store") {
		c.Scrape.Cache.Store = cmd.String("store")
		c.Crawl.Cache.Store = cmd.String("store")
	}
	for _, kv := range cmd.StringSlice("key") {
		name, val, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("invalid --key %q, expected name=value", kv)
		}
		cred := creds[name]
		cred.APIKey = val
		creds[name] = cred
	}
	for _, kv := range cmd.StringSlice("host") {
		name, val, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("invalid --host %q, expected name=url", kv)
		}
		cred := creds[name]
		cred.Host = val
		creds[name] = cred
	}
	return nil
}

func validateProvider(op, name string, allowed []string) error {
	for _, a := range allowed {
		if a == name {
			return nil
		}
	}
	return fmt.Errorf("%q does not support %s; choose one of: %s", name, op, strings.Join(allowed, ", "))
}

// setCacheEnabled sets the enabled flag using a fresh pointer (operations must
// not share an *bool; see config.Default).
func setCacheEnabled(c *config.CacheConfig, on bool) {
	v := on
	c.Enabled = &v
}

// runConfigForm drives the interactive settings TUI, mutating c. It returns
// false when the user cancels.
func runConfigForm(c *config.Config, path string, assumeYes bool) (bool, error) {
	searchP := orValue(c.Search.Provider, "firecrawl")
	scrapeP := orValue(c.Scrape.Provider, "firecrawl")
	crawlP := orValue(c.Crawl.Provider, "firecrawl")
	format := orValue(string(c.Scrape.Options.OutputFormat), "markdown")
	scrapeCache := c.Scrape.Cache.IsEnabled()
	crawlCache := c.Crawl.Cache.IsEnabled()
	ttlDays := strconv.Itoa(effectiveTTLDays(c.Scrape.Cache))

	confirm := true
	groups := []*huh.Group{
		huh.NewGroup(
			huh.NewSelect[string]().Title("Search provider").
				Options(huh.NewOptions(searchProviders...)...).Value(&searchP),
			huh.NewSelect[string]().Title("Scrape provider").
				Options(huh.NewOptions(scrapeProviders...)...).Value(&scrapeP),
			huh.NewSelect[string]().Title("Scrape output format").
				Options(huh.NewOptions("markdown", "html", "json")...).Value(&format),
			huh.NewSelect[string]().Title("Crawl provider").
				Options(huh.NewOptions(crawlProviders...)...).Value(&crawlP),
		),
		huh.NewGroup(
			huh.NewConfirm().Title("Cache scrape results?").Value(&scrapeCache),
			huh.NewConfirm().Title("Cache crawl results?").Value(&crawlCache),
			huh.NewInput().Title("Cache TTL (days)").Value(&ttlDays).
				Validate(func(s string) error {
					n, err := strconv.Atoi(strings.TrimSpace(s))
					if err != nil || n <= 0 {
						return fmt.Errorf("enter a positive whole number")
					}
					return nil
				}),
		),
	}
	if fileExists(path) && !assumeYes {
		groups = append(groups, huh.NewGroup(
			huh.NewConfirm().Title(fmt.Sprintf("Overwrite %s?", path)).Value(&confirm),
		))
	}

	if err := huh.NewForm(groups...).Run(); err != nil {
		if err == huh.ErrUserAborted {
			return false, nil
		}
		return false, err
	}
	if !confirm {
		return false, nil
	}

	c.Search.Provider = searchP
	c.Scrape.Provider = scrapeP
	c.Crawl.Provider = crawlP
	c.Scrape.Options.OutputFormat = parseFormat(format)
	setCacheEnabled(&c.Scrape.Cache, scrapeCache)
	setCacheEnabled(&c.Crawl.Cache, crawlCache)
	if n, err := strconv.Atoi(strings.TrimSpace(ttlDays)); err == nil && n > 0 {
		c.Scrape.Cache.TTLSecs = n * 86400
		c.Crawl.Cache.TTLSecs = n * 86400
	}
	return true, nil
}

// runCredsForm prompts (masked) for the API key of each selected provider —
// and, for self-hostable providers, the host base URL (defaulting to the cloud
// URL) — pre-filling any values already in creds.
func runCredsForm(creds map[string]config.Credential, names []string) error {
	if len(names) == 0 {
		return nil
	}
	keyVals := make(map[string]*string, len(names))
	hostVals := make(map[string]*string)
	fields := make([]huh.Field, 0, len(names))
	for _, n := range names {
		k := creds[n].APIKey
		keyVals[n] = &k
		fields = append(fields, huh.NewInput().
			Title(n+" API key").
			Description("leave blank to skip").
			EchoMode(huh.EchoModePassword).
			Value(keyVals[n]))

		if def, ok := providerHostDefaults[n]; ok {
			h := orValue(creds[n].Host, def)
			hostVals[n] = &h
			fields = append(fields, huh.NewInput().Title(n+" host").Value(hostVals[n]))
		}
	}

	if err := huh.NewForm(huh.NewGroup(fields...)).Run(); err != nil {
		if err == huh.ErrUserAborted {
			return nil
		}
		return err
	}
	for n := range keyVals {
		cred := creds[n]
		cred.APIKey = strings.TrimSpace(*keyVals[n])
		if hv, ok := hostVals[n]; ok {
			cred.Host = strings.TrimSpace(*hv)
		}
		creds[n] = cred
	}
	return nil
}

func effectiveTTLDays(c config.CacheConfig) int {
	d := c.TTL()
	if d <= 0 {
		d = cache.DefaultTTL
	}
	return int(d / (24 * time.Hour))
}
