# provider=auto Failover Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an `auto` meta-provider that, for search and scrape, tries each configured provider in priority order and returns the first non-empty result.

**Architecture:** `auto` is a meta-provider satisfying `SearchProvider`/`ScrapeProvider`. Chain *membership* is derived from configured providers (provider.yaml/env, filtered by capability); chain *order* is a built-in code ranking optionally reordered by an additive per-op `priority:` hint in config.yaml. The factory builds the meta-provider on request; call sites in `main.go` are unchanged because `"auto"` is just another provider name.

**Tech Stack:** Go 1.25, `urfave/cli/v3`, `imroc/req/v3`, `charmbracelet/huh` (interactive config), `gopkg.in/yaml.v3`.

## Global Constraints

- Module `github.com/rishang/seek`, rooted in `src/`. All paths below are relative to `src/`.
- `provider/`, `config/`, `cache/` packages **never** log — they return errors; `main.go` logs.
- stdout = data; stderr = logs via `logx`. Never mix.
- `omitempty` on optional JSON/YAML fields.
- Every provider file ends with compile-time interface checks (`var _ Interface = (*T)(nil)`).
- Wrap errors with context: `fmt.Errorf("... %w", ...)`.
- Use the Taskfile: `task test`, `task vet`, `task fmt`. Run from repo root. Before finishing each task: tests green.
- Scope: **search + scrape only**. Crawl is untouched.

---

### Task 1: Config schema — `Priority` field + `auto` defaults

**Files:**
- Modify: `config/config.go` (the `Operation` struct ~line 102; `Default()` ~line 180)
- Test: `config/config_auto_test.go` (create)

**Interfaces:**
- Produces: `config.Operation.Priority []string` (yaml `priority,omitempty`); `config.Default()` returns `Search.Provider == "auto"`, `Scrape.Provider == "auto"`, `Crawl.Provider == "firecrawl"`.

- [ ] **Step 1: Write the failing test**

Create `config/config_auto_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd src && go test ./config/ -run 'TestDefaultUsesAuto|TestPriority' -v`
Expected: FAIL — `Operation` has no field `Priority`, and/or default providers are `firecrawl`.

- [ ] **Step 3: Implement**

In `config/config.go`, add the `Priority` field to `Operation` (after `Provider`):

```go
// Operation configures a single capability (search, scrape, or crawl).
type Operation struct {
	Provider string      `yaml:"provider"`
	Priority []string    `yaml:"priority,omitempty"` // optional auto try-order hint; reorders only, never restricts
	Cache    CacheConfig `yaml:"cache,omitempty"`
	Options  Options     `yaml:"options,omitempty"`
}
```

In `Default()`, change the search and scrape providers to `"auto"` (leave crawl as `firecrawl`):

```go
	return Config{
		Search: Operation{Provider: "auto"},
		Scrape: Operation{Provider: "auto", Cache: enabledCache(), Options: Options{OutputFormat: FormatMarkdown}},
		Crawl:  Operation{Provider: "firecrawl", Cache: enabledCache()},
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd src && go test ./config/ -v`
Expected: PASS (all config tests, including pre-existing ones).

- [ ] **Step 5: Commit**

```bash
git add src/config/config.go src/config/config_auto_test.go
git commit -m "feat(config): add auto Priority hint and default search/scrape to auto"
```

---

### Task 2: Factory only builds configured providers

**Files:**
- Modify: `provider/factory.go` (`NewFactory` ~line 26-52)
- Test: `provider/factory_test.go` (create)

**Interfaces:**
- Consumes: `config.ProviderConfig{Name, APIKey, Host}`.
- Produces: behavior — `NewFactory` skips any `ProviderConfig` whose `APIKey` and `Host` are both empty; `Factory.Get(name)` returns nil for such providers.

- [ ] **Step 1: Write the failing test**

Create `provider/factory_test.go`:

```go
package provider

import (
	"testing"

	"github.com/rishang/seek/config"
)

func TestNewFactorySkipsUnconfigured(t *testing.T) {
	f := NewFactory([]config.ProviderConfig{
		{Name: "tavily"},                              // no key, no host -> skipped
		{Name: "exa", APIKey: "k"},                    // key -> built
		{Name: "lightpanda", Host: "http://localhost"}, // host only -> built
	})
	if f.Get("tavily") != nil {
		t.Error("tavily has no key/host; should not be built")
	}
	if f.Get("exa") == nil {
		t.Error("exa has a key; should be built")
	}
	if f.Get("lightpanda") == nil {
		t.Error("lightpanda has a host; should be built")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd src && go test ./provider/ -run TestNewFactorySkipsUnconfigured -v`
Expected: FAIL — `tavily` is built today, so `Get("tavily")` is non-nil.

- [ ] **Step 3: Implement**

In `provider/factory.go`, guard the build loop in `NewFactory`. Replace the `for _, pc := range providers {` loop body opening with a skip check:

```go
	for _, pc := range providers {
		if pc.APIKey == "" && pc.Host == "" {
			continue // unconfigured: no key and no host
		}
		switch pc.Name {
```

(Leave the rest of the switch unchanged.)

- [ ] **Step 4: Run test to verify it passes**

Run: `cd src && go test ./provider/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add src/provider/factory.go src/provider/factory_test.go
git commit -m "feat(provider): factory skips providers with no key or host"
```

---

### Task 3: `AutoReporter` interface + the `auto` meta-provider

**Files:**
- Modify: `provider/provider.go` (add `Attempt` + `AutoReporter`)
- Create: `provider/auto.go`
- Test: `provider/auto_test.go`

**Interfaces:**
- Consumes: `SearchProvider`, `ScrapeProvider`, `TimeRangeSearcher` from `provider.go`; `config.SearchResult`, `config.ScrapeResult`.
- Produces:
  - `type Attempt struct { Provider string; Err error }`
  - `type AutoReporter interface { Attempts() []Attempt }`
  - `type autoSearch struct{ ... }` with constructor `newAutoSearch(chain []autoSearchEntry) *autoSearch`, where `autoSearchEntry{ name string; sp SearchProvider }`. Implements `SearchProvider`, `AutoReporter`, `TimeRangeSearcher`, `Name() string` (returns `"auto"`).
  - `type autoScrape struct{ ... }` with constructor `newAutoScrape(chain []autoScrapeEntry) *autoScrape`, where `autoScrapeEntry{ name string; sp ScrapeProvider }`. Implements `ScrapeProvider`, `AutoReporter`, `Name() string`.

- [ ] **Step 1: Write the failing test**

Create `provider/auto_test.go`:

```go
package provider

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rishang/seek/config"
)

// stub search/scrape providers for chain tests.
type stubSearch struct {
	name    string
	results []config.SearchResult
	err     error
}

func (s stubSearch) Name() string { return s.name }
func (s stubSearch) Search(_ context.Context, _ string, _ config.SearchOptions) ([]config.SearchResult, error) {
	return s.results, s.err
}

type stubScrape struct {
	name    string
	content string
	err     error
}

func (s stubScrape) Name() string { return s.name }
func (s stubScrape) Scrape(_ context.Context, url string, _ config.ScrapeOptions) (*config.ScrapeResult, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &config.ScrapeResult{URL: url, Content: s.content}, nil
}

func hit(n int) []config.SearchResult {
	return make([]config.SearchResult, n)
}

func TestAutoSearchFailsOverOnError(t *testing.T) {
	a := newAutoSearch([]autoSearchEntry{
		{"a", stubSearch{name: "a", err: errors.New("boom")}},
		{"b", stubSearch{name: "b", results: hit(2)}},
	})
	res, err := a.Search(context.Background(), "q", config.SearchOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("want 2 results, got %d", len(res))
	}
	at := a.Attempts()
	if len(at) != 2 || at[0].Err == nil || at[1].Err != nil {
		t.Fatalf("attempts: %+v", at)
	}
	if at[1].Provider != "b" {
		t.Fatalf("served by %q, want b", at[1].Provider)
	}
}

func TestAutoSearchFailsOverOnEmpty(t *testing.T) {
	a := newAutoSearch([]autoSearchEntry{
		{"a", stubSearch{name: "a", results: hit(0)}},
		{"b", stubSearch{name: "b", results: hit(1)}},
	})
	res, err := a.Search(context.Background(), "q", config.SearchOptions{})
	if err != nil || len(res) != 1 {
		t.Fatalf("res=%d err=%v", len(res), err)
	}
}

func TestAutoSearchAllFailAggregates(t *testing.T) {
	a := newAutoSearch([]autoSearchEntry{
		{"a", stubSearch{name: "a", err: errors.New("timeout")}},
		{"b", stubSearch{name: "b", results: hit(0)}},
	})
	_, err := a.Search(context.Background(), "q", config.SearchOptions{})
	if err == nil {
		t.Fatal("want error when all fail")
	}
	msg := err.Error()
	if !strings.Contains(msg, "a: timeout") || !strings.Contains(msg, "b:") {
		t.Fatalf("aggregated error missing detail: %q", msg)
	}
}

func TestAutoScrapeFailsOverOnEmptyContent(t *testing.T) {
	a := newAutoScrape([]autoScrapeEntry{
		{"a", stubScrape{name: "a", content: ""}},
		{"b", stubScrape{name: "b", content: "hello"}},
	})
	res, err := a.Scrape(context.Background(), "http://x", config.ScrapeOptions{})
	if err != nil || res.Content != "hello" {
		t.Fatalf("res=%v err=%v", res, err)
	}
}

func TestAutoSearchSupportsTimeRangeIfAnyMemberDoes(t *testing.T) {
	// stubSearch does not implement TimeRangeSearcher -> false.
	a := newAutoSearch([]autoSearchEntry{{"a", stubSearch{name: "a"}}})
	if a.SupportsTimeRange() {
		t.Error("no member supports time range; want false")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd src && go test ./provider/ -run TestAuto -v`
Expected: FAIL — `newAutoSearch`, `autoSearchEntry`, etc. are undefined.

- [ ] **Step 3: Implement the interface additions**

In `provider/provider.go`, add after the `Provider` interface:

```go
// Attempt records one provider tried by an auto chain. Err is nil for the
// provider that served the result.
type Attempt struct {
	Provider string
	Err      error
}

// AutoReporter is implemented by the auto meta-provider so the CLI can report
// which provider served a request and why earlier ones failed.
type AutoReporter interface {
	Attempts() []Attempt
}
```

- [ ] **Step 4: Implement `provider/auto.go`**

```go
package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rishang/seek/config"
)

// auto is a meta-provider: it tries an ordered chain of real providers and
// returns the first non-empty success. A provider is skipped (failed over) on
// either an error or an empty result. When the whole chain is exhausted it
// returns an aggregated error naming every attempt.

var (
	errNoResults    = errors.New("no results")
	errEmptyContent = errors.New("empty content")
)

// ---- search ----

type autoSearchEntry struct {
	name string
	sp   SearchProvider
}

type autoSearch struct {
	chain    []autoSearchEntry
	attempts []Attempt
}

func newAutoSearch(chain []autoSearchEntry) *autoSearch { return &autoSearch{chain: chain} }

func (a *autoSearch) Name() string { return "auto" }

func (a *autoSearch) Attempts() []Attempt { return a.attempts }

// SupportsTimeRange reports true if any provider in the chain honors a time
// range, so the CLI's eager "ignored time range" warning only fires when none
// do.
func (a *autoSearch) SupportsTimeRange() bool {
	for _, e := range a.chain {
		if tr, ok := e.sp.(TimeRangeSearcher); ok && tr.SupportsTimeRange() {
			return true
		}
	}
	return false
}

func (a *autoSearch) Search(ctx context.Context, query string, opts config.SearchOptions) ([]config.SearchResult, error) {
	a.attempts = a.attempts[:0]
	for _, e := range a.chain {
		res, err := e.sp.Search(ctx, query, opts)
		if err != nil {
			a.attempts = append(a.attempts, Attempt{Provider: e.name, Err: err})
			continue
		}
		if len(res) == 0 {
			a.attempts = append(a.attempts, Attempt{Provider: e.name, Err: errNoResults})
			continue
		}
		a.attempts = append(a.attempts, Attempt{Provider: e.name})
		return res, nil
	}
	return nil, chainError("search", a.attempts)
}

// ---- scrape ----

type autoScrapeEntry struct {
	name string
	sp   ScrapeProvider
}

type autoScrape struct {
	chain    []autoScrapeEntry
	attempts []Attempt
}

func newAutoScrape(chain []autoScrapeEntry) *autoScrape { return &autoScrape{chain: chain} }

func (a *autoScrape) Name() string { return "auto" }

func (a *autoScrape) Attempts() []Attempt { return a.attempts }

func (a *autoScrape) Scrape(ctx context.Context, url string, opts config.ScrapeOptions) (*config.ScrapeResult, error) {
	a.attempts = a.attempts[:0]
	for _, e := range a.chain {
		res, err := e.sp.Scrape(ctx, url, opts)
		if err != nil {
			a.attempts = append(a.attempts, Attempt{Provider: e.name, Err: err})
			continue
		}
		if res == nil || res.Content == "" {
			a.attempts = append(a.attempts, Attempt{Provider: e.name, Err: errEmptyContent})
			continue
		}
		a.attempts = append(a.attempts, Attempt{Provider: e.name})
		return res, nil
	}
	return nil, chainError("scrape", a.attempts)
}

// chainError aggregates the failed attempts into a single descriptive error.
func chainError(op string, attempts []Attempt) error {
	parts := make([]string, 0, len(attempts))
	for _, a := range attempts {
		if a.Err != nil {
			parts = append(parts, fmt.Sprintf("%s: %v", a.Provider, a.Err))
		}
	}
	if len(parts) == 0 {
		return fmt.Errorf("auto %s: no providers available", op)
	}
	return fmt.Errorf("auto %s: %s", op, strings.Join(parts, "; "))
}

// Compile-time interface checks.
var (
	_ SearchProvider    = (*autoSearch)(nil)
	_ AutoReporter      = (*autoSearch)(nil)
	_ TimeRangeSearcher = (*autoSearch)(nil)
	_ ScrapeProvider    = (*autoScrape)(nil)
	_ AutoReporter      = (*autoScrape)(nil)
)
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd src && go test ./provider/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add src/provider/provider.go src/provider/auto.go src/provider/auto_test.go
git commit -m "feat(provider): add auto meta-provider with failover and AutoReporter"
```

---

### Task 4: Factory builds `auto`; cache forwards `AutoReporter`

**Files:**
- Modify: `provider/factory.go` (`Factory` struct, `NewFactory`, `Search`, `Scrape`; add `SetAutoChain`, `autoSearch`/`autoScrape` builders)
- Modify: `provider/cache.go` (forward `Attempts()` through `cachingScrape`)
- Test: `provider/factory_auto_test.go` (create)

**Interfaces:**
- Consumes: `newAutoSearch`, `autoSearchEntry`, `newAutoScrape`, `autoScrapeEntry`, `AutoReporter` from Task 3; `capability[T]` helper (existing).
- Produces:
  - `func (f *Factory) SetAutoChain(op string, names []string)` — stores the ordered candidate names for `"search"`/`"scrape"`.
  - `factory.Search("auto")` / `factory.Scrape("auto")` build the meta-provider from the stored candidates, keeping only configured + capable ones (order preserved); error if none remain.
  - `cachingScrape.Attempts() []Attempt` forwards to a wrapped `AutoReporter` (nil otherwise).

- [ ] **Step 1: Write the failing test**

Create `provider/factory_auto_test.go`:

```go
package provider

import (
	"context"
	"testing"

	"github.com/rishang/seek/config"
)

func TestFactoryAutoSearchFiltersToConfigured(t *testing.T) {
	// exa configured (key), brave not built (no key); chain lists both + a
	// scrape-only provider (webcrawlerapi) that must be filtered for search.
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

func TestCachingScrapeForwardsAttempts(t *testing.T) {
	inner := newAutoScrape([]autoScrapeEntry{{"a", stubScrape{name: "a", content: "x"}}})
	c := cachingScrape{ScrapeProvider: inner}
	if _, err := inner.Scrape(context.Background(), "http://x", config.ScrapeOptions{}); err != nil {
		t.Fatal(err)
	}
	ar, ok := any(c).(AutoReporter)
	if !ok {
		t.Fatal("cachingScrape should implement AutoReporter")
	}
	if len(ar.Attempts()) != 1 || ar.Attempts()[0].Provider != "a" {
		t.Fatalf("attempts not forwarded: %+v", ar.Attempts())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd src && go test ./provider/ -run 'TestFactoryAuto|TestCachingScrapeForwards' -v`
Expected: FAIL — `SetAutoChain` undefined; `Search("auto")` falls through to `capability` and errors "not configured"; `cachingScrape` has no `Attempts`.

- [ ] **Step 3: Implement factory chain storage + builders**

In `provider/factory.go`, add a `chains` field to `Factory` and initialize it in `NewFactory`:

```go
type Factory struct {
	providers map[string]Provider
	caches    map[string]opCache // keyed by "search" | "scrape" | "crawl"
	chains    map[string][]string // auto candidate order, keyed by "search" | "scrape"
}
```

In `NewFactory`, initialize the map alongside the others:

```go
	f := &Factory{
		providers: make(map[string]Provider, len(providers)),
		caches:    make(map[string]opCache, 3),
		chains:    make(map[string][]string, 2),
	}
```

Add the setter (near `SetCache`):

```go
// SetAutoChain stores the ordered candidate provider names the "auto" provider
// considers for an operation ("search" | "scrape"). The factory filters these
// to configured + capable providers when building the meta-provider.
func (f *Factory) SetAutoChain(op string, names []string) { f.chains[op] = names }
```

Change `Search` to intercept `"auto"`:

```go
func (f *Factory) Search(name string) (SearchProvider, error) {
	if name == "auto" {
		return f.autoSearch()
	}
	return capability[SearchProvider](f, name, "search")
}

func (f *Factory) autoSearch() (SearchProvider, error) {
	var chain []autoSearchEntry
	for _, n := range f.chains["search"] {
		if sp, err := capability[SearchProvider](f, n, "search"); err == nil {
			chain = append(chain, autoSearchEntry{name: n, sp: sp})
		}
	}
	if len(chain) == 0 {
		return nil, fmt.Errorf("provider \"auto\": no configured provider supports search")
	}
	return newAutoSearch(chain), nil
}
```

Change `Scrape` to intercept `"auto"` before the cache wrap:

```go
func (f *Factory) Scrape(name string) (ScrapeProvider, error) {
	var (
		sp  ScrapeProvider
		err error
	)
	if name == "auto" {
		sp, err = f.autoScrape()
	} else {
		sp, err = capability[ScrapeProvider](f, name, "scrape")
	}
	if err != nil {
		return nil, err
	}
	if c, ok := f.caches["scrape"]; ok && c.store != nil {
		return cachingScrape{ScrapeProvider: sp, store: c.store, provider: name, ttl: c.ttl}, nil
	}
	return sp, nil
}

func (f *Factory) autoScrape() (ScrapeProvider, error) {
	var chain []autoScrapeEntry
	for _, n := range f.chains["scrape"] {
		if sp, err := capability[ScrapeProvider](f, n, "scrape"); err == nil {
			chain = append(chain, autoScrapeEntry{name: n, sp: sp})
		}
	}
	if len(chain) == 0 {
		return nil, fmt.Errorf("provider \"auto\": no configured provider supports scrape")
	}
	return newAutoScrape(chain), nil
}
```

- [ ] **Step 4: Forward AutoReporter through cachingScrape**

In `provider/cache.go`, add a method on `cachingScrape` (after its `Scrape` method):

```go
// Attempts forwards the wrapped provider's auto attempts (nil when the wrapped
// provider is not an auto meta-provider), so the CLI can report failover even
// through the cache decorator.
func (c cachingScrape) Attempts() []Attempt {
	if ar, ok := c.ScrapeProvider.(AutoReporter); ok {
		return ar.Attempts()
	}
	return nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd src && go test ./provider/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add src/provider/factory.go src/provider/cache.go src/provider/factory_auto_test.go
git commit -m "feat(provider): factory builds auto chain; cache forwards AutoReporter"
```

---

### Task 5: Wire `auto` in `main.go` — chain resolution, defaults, logging

**Files:**
- Modify: `main.go` (`main()` wiring; add `autoCandidates`; add `logAutoAttempts`; call it in search + scrape actions)
- Modify: `config_cmd.go` (add `defaultAutoChains` var alongside `searchProviders`/`scrapeProviders`)
- Test: `main_auto_test.go` (create)

**Interfaces:**
- Consumes: `factory.SetAutoChain`, `provider.AutoReporter` from Task 4; `cfg.Search.Priority`/`cfg.Scrape.Priority` from Task 1; `providerEnv` (existing, `main.go`).
- Produces:
  - `var defaultAutoChains = map[string][]string{...}` in `config_cmd.go`.
  - `func autoCandidates(op string, priority []string) []string` in `main.go` — dedup of `priority` ++ `defaultAutoChains[op]` ++ `providerEnv` order, dropping empty and `"auto"`.
  - `func logAutoAttempts(p any)` in `main.go` — logs failovers via `logx`.

- [ ] **Step 1: Write the failing test**

Create `main_auto_test.go`:

```go
package main

import "testing"

func TestAutoCandidatesPriorityFirstThenDefaultsThenEnv(t *testing.T) {
	// priority pushes brave to the front; defaults/env fill the rest; no dups;
	// "auto" itself is dropped.
	got := autoCandidates("search", []string{"brave", "auto"})
	if len(got) == 0 || got[0] != "brave" {
		t.Fatalf("brave should lead, got %v", got)
	}
	seen := map[string]int{}
	for _, n := range got {
		seen[n]++
		if n == "auto" {
			t.Fatalf("auto must not be a candidate: %v", got)
		}
	}
	for n, c := range seen {
		if c != 1 {
			t.Fatalf("%q appears %d times: %v", n, c, got)
		}
	}
	// every default search provider must be present somewhere
	for _, n := range defaultAutoChains["search"] {
		if seen[n] == 0 {
			t.Fatalf("default %q missing from candidates %v", n, got)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd src && go test . -run TestAutoCandidates -v`
Expected: FAIL — `autoCandidates` and `defaultAutoChains` undefined.

- [ ] **Step 3: Add `defaultAutoChains`**

In `config_cmd.go`, after the `searchProviders`/`scrapeProviders`/`crawlProviders` block (~line 27), add:

```go
// defaultAutoChains is the built-in try-order ranking for the "auto" provider,
// per operation. It ranks the providers the user has actually configured — it
// does not decide membership. Must stay a subset of the per-capability lists
// above. Not written to config.yaml by `seek config init`.
var defaultAutoChains = map[string][]string{
	"search": {"exa", "brave", "tavily", "firecrawl", "spider.cloud"},
	"scrape": {"firecrawl", "spider.cloud", "lightpanda", "tavily", "exa", "webcrawlerapi"},
}
```

- [ ] **Step 4: Add `autoCandidates` and wire `SetAutoChain`**

In `main.go`, add the helper (near `providerFor`):

```go
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
```

In `main()`, after `factory = loadProviders()` and `cfg` is loaded, register the chains:

```go
	factory.SetAutoChain("search", autoCandidates("search", cfg.Search.Priority))
	factory.SetAutoChain("scrape", autoCandidates("scrape", cfg.Scrape.Priority))
```

- [ ] **Step 5: Add `logAutoAttempts` and call it**

In `main.go`, add:

```go
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
```

In the **search** action, call it right after the `sp.Search(...)` call returns, before the error check, so failovers log even on total failure:

```go
			results, err := sp.Search(ctx, query, opts)
			logAutoAttempts(sp)
			if err != nil {
				return err
			}
			return render(cmd, results)
```

In the **scrape** action, similarly after `sp.Scrape(...)`:

```go
			result, err := sp.Scrape(ctx, url, config.ScrapeOptions{OutputFormat: outFormat})
			logAutoAttempts(sp)
			if err != nil {
				return err
			}
			fmt.Println(result.Content)
			return nil
```

(The existing time-range warning in the search action needs no change: `*autoSearch` implements `TimeRangeSearcher`, so the existing `sp.(provider.TimeRangeSearcher)` check now reflects whether any chain member honors a range.)

- [ ] **Step 6: Run tests + vet + build**

Run: `cd src && go test . -v && go build ./...`
Expected: PASS and clean build.

- [ ] **Step 7: Commit**

```bash
git add src/main.go src/config_cmd.go src/main_auto_test.go
git commit -m "feat: wire auto chain resolution and failover logging in main"
```

---

### Task 6: `seek config init` supports `auto` (selection + multi-provider keys)

**Files:**
- Modify: `config_cmd.go` (`searchProviders`/`scrapeProviders` selectable options; `validateProvider` use; `runConfigForm`; `selectedProviders`; add `autoMembership`; `runConfigInit` interactive branch)
- Modify: `main.go` (`providerFlag` usage strings for search & scrape)
- Test: `config_cmd_auto_test.go` (create)

**Interfaces:**
- Consumes: `defaultAutoChains` (Task 5), `searchProviders`/`scrapeProviders` (existing), `config.Config` (Task 1).
- Produces:
  - `validateProvider` accepts `"auto"` for search and scrape.
  - `selectedProviders` never returns `"auto"` as a name.
  - `func autoMembership(op string, configured map[string]config.Credential) []string` — capable providers for `op`, ordered, used to pre-check the multi-select.

- [ ] **Step 1: Write the failing test**

Create `config_cmd_auto_test.go`:

```go
package main

import (
	"testing"

	"github.com/rishang/seek/config"
)

func TestValidateProviderAcceptsAuto(t *testing.T) {
	if err := validateProvider("search", "auto", append([]string{"auto"}, searchProviders...)); err != nil {
		t.Errorf("search auto should validate: %v", err)
	}
}

func TestSelectedProvidersDropsAuto(t *testing.T) {
	c := config.Config{
		Search: config.Operation{Provider: "auto"},
		Scrape: config.Operation{Provider: "exa"},
		Crawl:  config.Operation{Provider: "firecrawl"},
	}
	for _, n := range selectedProviders(c) {
		if n == "auto" {
			t.Fatalf("selectedProviders must not include auto: %v", selectedProviders(c))
		}
	}
}

func TestAutoMembershipIsCapableSubset(t *testing.T) {
	got := autoMembership("search", map[string]config.Credential{})
	for _, n := range got {
		found := false
		for _, s := range searchProviders {
			if s == n {
				found = true
			}
		}
		if !found {
			t.Fatalf("%q is not a search provider", n)
		}
	}
	if len(got) == 0 {
		t.Fatal("expected some search providers")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd src && go test . -run 'TestValidateProviderAcceptsAuto|TestSelectedProvidersDropsAuto|TestAutoMembership' -v`
Expected: FAIL — `autoMembership` undefined; `selectedProviders` includes `"auto"`.

- [ ] **Step 3: Implement — drop `auto` from `selectedProviders`, add `autoMembership`**

In `config_cmd.go`, update `selectedProviders` to skip the literal `"auto"`:

```go
// selectedProviders returns the distinct real providers chosen across the
// operations. "auto" is not a real provider and is dropped — auto membership is
// gathered separately (see autoMembership).
func selectedProviders(c config.Config) []string {
	var out []string
	seen := map[string]bool{}
	for _, name := range []string{c.Search.Provider, c.Scrape.Provider, c.Crawl.Provider} {
		if name == "" || name == "auto" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// autoMembership returns the capable providers for an op, in defaultAutoChains
// order, used to populate the "which providers to set up for auto" multi-select.
func autoMembership(op string, _ map[string]config.Credential) []string {
	capable := map[string]bool{}
	switch op {
	case "search":
		for _, n := range searchProviders {
			capable[n] = true
		}
	case "scrape":
		for _, n := range scrapeProviders {
			capable[n] = true
		}
	}
	var out []string
	seen := map[string]bool{}
	add := func(names []string) {
		for _, n := range names {
			if capable[n] && !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	add(defaultAutoChains[op])
	if op == "search" {
		add(searchProviders)
	} else {
		add(scrapeProviders)
	}
	return out
}
```

- [ ] **Step 4: Implement — accept `auto` in selects, flags, and validation**

In `config_cmd.go`, add `"auto"` as the first option for the search and scrape selects in `runConfigForm` (leave crawl unchanged):

```go
			huh.NewSelect[string]().Title("Search provider").
				Options(huh.NewOptions(append([]string{"auto"}, searchProviders...)...)...).Value(&searchP),
			huh.NewSelect[string]().Title("Scrape provider").
				Options(huh.NewOptions(append([]string{"auto"}, scrapeProviders...)...)...).Value(&scrapeP),
```

Change the default pre-selected values in `runConfigForm` from `"firecrawl"` to `"auto"` for search and scrape:

```go
	searchP := orValue(c.Search.Provider, "auto")
	scrapeP := orValue(c.Scrape.Provider, "auto")
	crawlP := orValue(c.Crawl.Provider, "firecrawl")
```

In `applyInitFlags`, allow `auto` when validating the search and scrape flags by extending the allowed list:

```go
	if cmd.IsSet("search") {
		v := cmd.String("search")
		if err := validateProvider("search", v, append([]string{"auto"}, searchProviders...)); err != nil {
			return err
		}
		c.Search.Provider = v
	}
	if cmd.IsSet("scrape") {
		v := cmd.String("scrape")
		if err := validateProvider("scrape", v, append([]string{"auto"}, scrapeProviders...)); err != nil {
			return err
		}
		c.Scrape.Provider = v
	}
```

(Leave the `crawl` flag validation unchanged — crawl does not support `auto`.)

- [ ] **Step 5: Implement — multi-select membership prompt in interactive init**

In `config_cmd.go`, add a helper that runs the multi-select(s) and returns the union of providers to prompt keys for:

```go
// gatherAutoMembership prompts (multi-select) for which providers to set up for
// each op currently set to "auto", pre-checking those already configured. It
// returns the union of picks. Writes nothing to config.yaml.
func gatherAutoMembership(c config.Config, creds map[string]config.Credential) ([]string, error) {
	picked := map[string]bool{}
	var fields []huh.Field
	values := map[string]*[]string{}

	addGroup := func(op, title string, sel string) {
		if sel != "auto" {
			return
		}
		opts := autoMembership(op, creds)
		var pre []string
		for _, n := range opts {
			if creds[n].APIKey != "" || creds[n].Host != "" {
				pre = append(pre, n)
			}
		}
		v := pre
		values[op] = &v
		fields = append(fields, huh.NewMultiSelect[string]().
			Title(title).
			Options(huh.NewOptions(opts...)...).
			Value(values[op]))
	}
	addGroup("search", "Providers to set up for auto search", c.Search.Provider)
	addGroup("scrape", "Providers to set up for auto scrape", c.Scrape.Provider)

	if len(fields) > 0 {
		if err := huh.NewForm(huh.NewGroup(fields...)).Run(); err != nil {
			if err == huh.ErrUserAborted {
				return nil, nil
			}
			return nil, err
		}
	}
	for _, vp := range values {
		for _, n := range *vp {
			picked[n] = true
		}
	}
	out := make([]string, 0, len(picked))
	for n := range picked {
		out = append(out, n)
	}
	return out, nil
}
```

In `runConfigInit`, in the interactive branch, merge the auto picks into the credential-prompt set. Replace the existing credential-prompt call:

```go
		if err := runCredsForm(creds, selectedProviders(c)); err != nil {
			return err
		}
```

with:

```go
		names := selectedProviders(c)
		autoNames, err := gatherAutoMembership(c, creds)
		if err != nil {
			return err
		}
		names = mergeUnique(names, autoNames)
		if err := runCredsForm(creds, names); err != nil {
			return err
		}
```

And add the small merge helper near `selectedProviders`:

```go
// mergeUnique concatenates name lists, preserving order and dropping duplicates.
func mergeUnique(lists ...[]string) []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range lists {
		for _, n := range l {
			if n != "" && !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	return out
}
```

- [ ] **Step 6: Update CLI flag usage strings**

In `main.go`, update the `providerFlag` usage for search and scrape to advertise `auto`:

```go
			providerFlag("Provider: auto (default), firecrawl, tavily, spider.cloud, brave, exa"),
```

(in `searchCmd`) and

```go
			providerFlag("Provider: auto (default), firecrawl, tavily, spider.cloud, webcrawlerapi, lightpanda, exa"),
```

(in `scrapeCmd`). Leave `crawlCmd`'s usage unchanged.

- [ ] **Step 7: Run tests + build**

Run: `cd src && go test . ./... -v && go build ./...`
Expected: PASS, clean build.

- [ ] **Step 8: Commit**

```bash
git add src/config_cmd.go src/main.go src/config_cmd_auto_test.go
git commit -m "feat(config): init supports auto selection and multi-provider key setup"
```

---

### Task 7: Documentation

**Files:**
- Modify: `../notes/providers.md` (repo-root `notes/`, NOT under `src/`)
- Modify: `config_cmd.go` (the `config` command `Description` text, ~line 42)

**Interfaces:** None (docs only).

- [ ] **Step 1: Update `notes/providers.md`**

Add a section describing `auto` (place it after the capability matrix). Use this content:

```markdown
## The `auto` provider (search & scrape)

`auto` is a meta-provider that tries configured providers in priority order and
returns the first non-empty result, failing over on either an error or an empty
result. It is the default for `search` and `scrape`. Crawl does not support it.

- **Membership** comes from provider.yaml (plus env overrides): every provider
  you have a key/host for, that supports the operation, is in the chain. There
  is no provider list in config.yaml.
- **Order** is the built-in `defaultAutoChains` ranking (in `config_cmd.go`),
  optionally reordered by an additive per-op `priority:` hint in config.yaml.
  The hint only moves listed providers to the front; unlisted-but-configured
  providers still run, after them.
- On total failure `auto` returns an aggregated error naming every attempt.
- `SEEK_LOG=debug` shows which provider served; failovers log at `warn`.

Example config.yaml priority hint:

    config:
      search:
        provider: auto
        priority: [brave, exa]
```

Also update the add-a-provider checklist to note: a new search/scrape provider should be added to `defaultAutoChains` so `auto` ranks it.

- [ ] **Step 2: Update the `config` command description**

In `config_cmd.go`, extend the `Description` in `configCmd()` to mention auto. Change it to:

```go
		Description: "Configure seek. With no subcommand this runs `init`.\n\n" +
			"  seek config init    Create or edit config and provider keys\n" +
			"  seek config view    Show the effective configuration\n\n" +
			"search and scrape default to `auto`: providers are tried in priority\n" +
			"order until one returns a result. Set a `priority:` list per operation\n" +
			"in config.yaml to reorder; membership comes from configured keys.\n\n" +
			"Docs: " + readmeURL,
```

- [ ] **Step 3: Verify build + full suite**

Run: `cd src && task fmt && task vet && task test`
Expected: formatting clean, vet clean, all tests pass.

- [ ] **Step 4: Commit**

```bash
git add notes/providers.md src/config_cmd.go
git commit -m "docs: document the auto failover provider"
```

---

## Final verification

- [ ] Run `cd src && task fmt && task vet && task test` — all green.
- [ ] Manual smoke (requires at least one configured key), confirms failover wiring:
  - `task run -- config view` shows `search` / `scrape` provider `auto`.
  - `SEEK_LOG=debug task run -- search "golang generics"` prints `auto: served by <provider>` on stderr and results on stdout.
