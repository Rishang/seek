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
	"github.com/rishang/seek/provider"
	"github.com/urfave/cli/v3"
)

// readmeURL is surfaced in help output so users can find the full docs.
const readmeURL = "https://github.com/rishang/seek#readme"

// Providers grouped by the capability they support, derived from the provider
// registry (the single source of truth). Reused by the config view, the init
// form, and the init flag validation.
var (
	searchProviders = provider.NamesFor(provider.CapSearch)
	fetchProviders  = provider.NamesFor(provider.CapFetch)
	crawlProviders  = provider.NamesFor(provider.CapCrawl)
)

func configCmd() *cli.Command {
	init := configInitCmd()
	return &cli.Command{
		Name:  "config",
		Usage: "Create or edit the configuration",
		Description: "Configure seek. With no subcommand this runs `init`.\n\n" +
			"  seek config init    Create or edit config and provider keys\n" +
			"  seek config view    Show the effective configuration\n\n" +
			"search and fetch default to `auto`: providers are tried in priority\n" +
			"order until one returns a result. Set a top-level `providers.priority`\n" +
			"list in config.yaml to reorder (index 0 = highest); membership comes\n" +
			"from configured keys.\n\n" +
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
	printOp("fetch", c.Fetch, true, true)
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
			&cli.StringFlag{Name: "fetch", Usage: "Fetch provider"},
			&cli.StringFlag{Name: "crawl", Usage: "Crawl provider"},
			&cli.StringFlag{Name: "format", Usage: "Fetch output format: markdown, html, json"},
			&cli.IntFlag{Name: "ttl", Usage: "Cache TTL in days (fetch & crawl)"},
			&cli.BoolFlag{Name: "cache", Value: true, Usage: "Enable fetch/crawl caching (use --cache=false to disable)"},
			&cli.StringFlag{Name: "store", Usage: "Cache backend (sqlite)"},
			&cli.StringSliceFlag{Name: "key", Usage: "Provider API key as name=value (repeatable)"},
			&cli.StringSliceFlag{Name: "host", Usage: "Provider host base URL as name=url (repeatable; OSS providers)"},
			&cli.BoolFlag{Name: "yes", Aliases: []string{"y"}, Usage: "Overwrite an existing file without prompting"},
		},
		Action: runConfigInit,
	}
}

// initValueFlags are the flags that, when set, switch init to non-interactive.
var initValueFlags = []string{"search", "fetch", "crawl", "format", "ttl", "cache", "store", "key", "host"}

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
		// One form, three stages (providers → keys → settings). Because it is a
		// single huh form, shift+tab navigates back to any earlier step.
		ok, err := runInitForm(&c, creds, cfgPath, cmd.Bool("yes"))
		if err != nil {
			return err
		}
		if !ok {
			fmt.Println("Cancelled; no changes written.")
			return nil
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
	// Save when there are creds to write, or when a file already exists so that
	// removals (e.g. de-selecting providers in the form) are persisted even when
	// the last provider was dropped.
	if len(creds) > 0 || fileExists(provPath) {
		if err := config.SaveProviders(provPath, creds); err != nil {
			return err
		}
		fmt.Printf("Wrote %s\n", provPath)
	}
	return nil
}

// allProviderNames lists every known provider in declaration order.
func allProviderNames() []string {
	out := make([]string, len(providerEnv))
	for i, p := range providerEnv {
		out[i] = p.Name
	}
	return out
}

// configuredNames returns the providers that already have a key or host stored
// in creds, in declaration order. Used to pre-check the select form.
func configuredNames(creds map[string]config.Credential) []string {
	var out []string
	for _, p := range providerEnv {
		if creds[p.Name].APIKey != "" || creds[p.Name].Host != "" {
			out = append(out, p.Name)
		}
	}
	return out
}

// pickDefault returns cur when it is among opts, else fallback. Keeps a form's
// preselected value valid when the stored provider is no longer a valid option.
func pickDefault(cur string, opts []string, fallback string) string {
	for _, o := range opts {
		if o == cur {
			return cur
		}
	}
	return fallback
}

// runInitForm drives the interactive init as two huh forms:
//
//  1. Providers: a multi-select of which providers to configure (pre-checked
//     from creds), followed by a per-provider group (key, plus host for OSS
//     providers) shown only while that provider is selected.
//  2. Settings: operation defaults, cache settings, and the overwrite confirm.
//
// Two forms (not one) so the settings stage can offer only the providers just
// configured: the search/fetch/crawl dropdowns list "auto" plus the providers
// selected in form 1. An unconfigured provider can't run, so offering it as a
// default would be a dead end. A single live-filtered form would need dynamic
// select options, which huh can't size to their content (blank-gap rendering);
// splitting the forms keeps the option lists static and correct.
//
// It mutates c and creds in place and returns false when the user cancels.
func runInitForm(c *config.Config, creds map[string]config.Credential, path string, assumeYes bool) (bool, error) {
	selected := configuredNames(creds)
	selectedSet := func() map[string]bool {
		m := make(map[string]bool, len(selected))
		for _, n := range selected {
			m[n] = true
		}
		return m
	}

	// Form-1 bindings, pre-filled from existing creds. Built for every provider
	// up front (stable pointers); groups are hidden unless the provider is picked.
	keyVals := map[string]*string{}
	hostVals := map[string]*string{}

	// Form 1: which providers to configure, then their keys.
	provGroups := []*huh.Group{
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Providers to configure").
				Description("Select any number; enter their keys next. shift+tab steps back.").
				Options(huh.NewOptions(allProviderNames()...)...).
				Value(&selected),
		),
	}
	for _, p := range providerEnv {
		name := p.Name
		k := creds[name].APIKey
		keyVals[name] = &k
		fields := []huh.Field{
			huh.NewInput().Title(name + " API key").Description("leave blank to skip").
				EchoMode(huh.EchoModePassword).Value(keyVals[name]),
		}
		if def := provider.HostDefault(name); def != "" {
			h := orValue(creds[name].Host, def)
			hostVals[name] = &h
			fields = append(fields, huh.NewInput().Title(name+" host").Value(hostVals[name]))
		}
		provGroups = append(provGroups, huh.NewGroup(fields...).
			WithHideFunc(func() bool { return !selectedSet()[name] }))
	}
	if cancelled, err := runGroups(provGroups...); err != nil || cancelled {
		return false, err
	}

	// Persist the selection now so form 2's option lists reflect exactly what was
	// just configured (creds is only saved by the caller on a true return, so a
	// cancel in form 2 still writes nothing).
	applyProviderSelection(creds, selectedSet(), keyVals, hostVals)

	// Form-2 option lists: only providers that ended up configured (a key or host
	// is present), plus "auto" where the operation supports it. A provider that
	// was checked but left blank can't run, so it's excluded here just like an
	// unselected one. Crawl has no "auto".
	configured := map[string]bool{}
	for _, n := range configuredNames(creds) {
		configured[n] = true
	}
	searchOpts := append([]string{"auto"}, capableSubset(searchProviders, configured)...)
	fetchOpts := append([]string{"auto"}, capableSubset(fetchProviders, configured)...)
	crawlOpts := capableSubset(crawlProviders, configured)

	searchP := pickDefault(orValue(c.Search.Provider, "auto"), searchOpts, "auto")
	fetchP := pickDefault(orValue(c.Fetch.Provider, "auto"), fetchOpts, "auto")
	format := orValue(string(c.Fetch.Options.OutputFormat), "markdown")
	fetchCache := c.Fetch.Cache.IsEnabled()
	crawlCache := c.Crawl.Cache.IsEnabled()
	ttlDays := strconv.Itoa(effectiveTTLDays(c.Fetch.Cache))
	confirm := true

	settings := []huh.Field{
		huh.NewSelect[string]().Title("Search provider").
			Options(huh.NewOptions(searchOpts...)...).Value(&searchP),
		huh.NewSelect[string]().Title("Fetch provider").
			Options(huh.NewOptions(fetchOpts...)...).Value(&fetchP),
		huh.NewSelect[string]().Title("Fetch output format").
			Options(huh.NewOptions("markdown", "html", "json")...).Value(&format),
	}
	// Crawl has no "auto", so only offer it when a crawl-capable provider was
	// configured; otherwise there's no valid default to pick.
	var crawlP string
	if len(crawlOpts) > 0 {
		crawlP = pickDefault(orValue(c.Crawl.Provider, crawlOpts[0]), crawlOpts, crawlOpts[0])
		settings = append(settings, huh.NewSelect[string]().Title("Crawl provider").
			Options(huh.NewOptions(crawlOpts...)...).Value(&crawlP))
	}

	setGroups := []*huh.Group{
		huh.NewGroup(settings...),
		huh.NewGroup(
			huh.NewConfirm().Title("Cache fetch results?").Value(&fetchCache),
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
		setGroups = append(setGroups, huh.NewGroup(
			huh.NewConfirm().Title(fmt.Sprintf("Overwrite %s?", path)).Value(&confirm),
		))
	}
	if cancelled, err := runGroups(setGroups...); err != nil || cancelled {
		return false, err
	}
	if !confirm {
		return false, nil
	}

	// Write back settings. Each value was chosen from a static option list, so no
	// post-hoc clamping is needed.
	c.Search.Provider = searchP
	c.Fetch.Provider = fetchP
	if crawlP != "" {
		c.Crawl.Provider = crawlP
	}
	c.Fetch.Options.OutputFormat = parseFormat(format)
	setCacheEnabled(&c.Fetch.Cache, fetchCache)
	setCacheEnabled(&c.Crawl.Cache, crawlCache)
	if n, err := strconv.Atoi(strings.TrimSpace(ttlDays)); err == nil && n > 0 {
		c.Fetch.Cache.TTLSecs = n * 86400
		c.Crawl.Cache.TTLSecs = n * 86400
	}
	return true, nil
}

func pruneEmptyCreds(creds map[string]config.Credential) {
	for name, c := range creds {
		if c.APIKey == "" && c.Host == "" {
			delete(creds, name)
		}
	}
}

// applyProviderSelection writes the form's key/host inputs back into creds.
// Providers in selected are upserted from keyVals/hostVals; providers not in
// selected are removed entirely, so de-selecting a provider in the init form
// drops its stored credential.
func applyProviderSelection(creds map[string]config.Credential, selected map[string]bool, keyVals, hostVals map[string]*string) {
	for name, kv := range keyVals {
		if !selected[name] {
			delete(creds, name)
			continue
		}
		cred := creds[name]
		cred.APIKey = strings.TrimSpace(*kv)
		if hv, ok := hostVals[name]; ok {
			cred.Host = strings.TrimSpace(*hv)
		}
		creds[name] = cred
	}
}

// capableSubset returns the providers in capable (preserving capable's order)
// that are present in set. Used to scope the init settings dropdowns to the
// providers that were actually configured, since an unconfigured provider
// can't run.
func capableSubset(capable []string, set map[string]bool) []string {
	var out []string
	for _, n := range capable {
		if set[n] {
			out = append(out, n)
		}
	}
	return out
}

// runGroups runs the given groups as one huh form, mapping a user-abort to a
// clean cancel (cancelled=true, err=nil) and passing any other error through.
func runGroups(groups ...*huh.Group) (cancelled bool, err error) {
	if e := huh.NewForm(groups...).Run(); e != nil {
		if e == huh.ErrUserAborted {
			return true, nil
		}
		return false, e
	}
	return false, nil
}

// applyInitFlags overlays the non-interactive flag values onto c and creds.
func applyInitFlags(cmd *cli.Command, c *config.Config, creds map[string]config.Credential) error {
	if cmd.IsSet("search") {
		v := cmd.String("search")
		if err := validateProvider("search", v, append([]string{"auto"}, searchProviders...)); err != nil {
			return err
		}
		c.Search.Provider = v
	}
	if cmd.IsSet("fetch") {
		v := cmd.String("fetch")
		if err := validateProvider("fetch", v, append([]string{"auto"}, fetchProviders...)); err != nil {
			return err
		}
		c.Fetch.Provider = v
	}
	if cmd.IsSet("crawl") {
		v := cmd.String("crawl")
		if err := validateProvider("crawl", v, crawlProviders); err != nil {
			return err
		}
		c.Crawl.Provider = v
	}
	if cmd.IsSet("format") {
		c.Fetch.Options.OutputFormat = parseFormat(cmd.String("format"))
	}
	if cmd.IsSet("ttl") {
		secs := int(cmd.Int("ttl")) * 86400
		c.Fetch.Cache.TTLSecs = secs
		c.Crawl.Cache.TTLSecs = secs
	}
	if cmd.IsSet("cache") {
		setCacheEnabled(&c.Fetch.Cache, cmd.Bool("cache"))
		setCacheEnabled(&c.Crawl.Cache, cmd.Bool("cache"))
	}
	if cmd.IsSet("store") {
		c.Fetch.Cache.Store = cmd.String("store")
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

func effectiveTTLDays(c config.CacheConfig) int {
	d := c.TTL()
	if d <= 0 {
		d = cache.DefaultTTL
	}
	return int(d / (24 * time.Hour))
}
