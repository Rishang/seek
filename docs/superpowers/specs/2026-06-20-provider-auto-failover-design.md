# Design: `provider = auto` — failover meta-provider

Date: 2026-06-20
Status: Approved (pending implementation plan)

## Summary

Add an `auto` provider that, for a given operation, tries a configured chain of
real providers in priority order and returns the first non-empty success. It
applies to **search** and **scrape** only; **crawl** is unchanged. `auto`
becomes the built-in default for search and scrape.

`auto` is a *meta-provider*: it satisfies the same capability interfaces
(`SearchProvider` / `ScrapeProvider`) as a real provider and wraps an ordered
list of them. Call sites in `main.go` keep asking the factory for a capability
by name; `"auto"` is just another name.

## Goals

- One CLI value (`auto`) that transparently fails over across providers.
- Per-operation, user-controlled priority order via config.
- No new branching at the command call sites — `auto` flows through the existing
  `factory.Search(name)` / `factory.Scrape(name)` path.
- Surface which provider actually served a result, and why earlier ones failed,
  without violating the house rule that `provider/` never logs.

## Non-goals

- Crawl failover. Crawl keeps a single explicit provider (default `firecrawl`).
- Parallel/racing providers. The chain is strictly sequential.
- Result caching for search (search is never cached, unchanged).

## Configuration

`Operation` gains an ordered chain field:

```go
type Operation struct {
	Provider  string      `yaml:"provider"`
	Providers []string    `yaml:"providers,omitempty"` // the auto chain, priority order
	Cache     CacheConfig `yaml:"cache,omitempty"`
	Options   Options     `yaml:"options,omitempty"`
}
```

config.yaml:

```yaml
config:
  search:
    provider: auto
    providers: [exa, brave, tavily]
  scrape:
    provider: auto
    providers: [firecrawl, spider.cloud, lightpanda]
  crawl:
    provider: firecrawl        # unchanged, single provider
```

### Default priority chain (in code)

The priority order for `auto` has a built-in default defined **in code**, not in
config.yaml. config.yaml only carries an order when the user explicitly sets one
to override the default.

```go
// defaultAutoChains is the built-in priority order per operation, used when an
// op selects "auto" but sets no explicit `providers:` list. Tunable; not
// written to config.yaml by `seek config init`.
var defaultAutoChains = map[string][]string{
	"search": {"exa", "brave", "tavily", "firecrawl", "spider.cloud"},
	"scrape": {"firecrawl", "spider.cloud", "lightpanda", "tavily", "exa", "webcrawlerapi"},
}
```

(Lives alongside the existing per-capability lists in `config_cmd.go`
— `searchProviders` / `scrapeProviders` — which it must stay a subset of. The
exact ordering above is a reasonable starting point and can be adjusted.)

Chain resolution for an op when `provider: auto`:

1. If `providers:` is set and non-empty in config → use it verbatim (the user's
   override, in order).
2. Otherwise → use `defaultAutoChains[op]` (the built-in default priority).

In both cases the chain is then filtered at attempt time to **configured**
providers that support the capability (see "Configured becomes real").

`config.Default()` changes:

- `Search.Provider = "auto"`
- `Scrape.Provider = "auto"`
- `Crawl.Provider = "firecrawl"` (unchanged)
- `Providers` is left **nil/empty** for every op. The built-in
  `defaultAutoChains` supplies the order; a fresh install fails over across
  whatever providers the user has keys for.

## "Configured" becomes real

Today `NewFactory` builds a provider for every known name regardless of whether
a key is present. Change:

- `NewFactory` **skips** building a provider whose `APIKey` *and* `Host` are
  both empty.

Consequences (deliberate, accepted):

- `auto`'s "skip unconfigured" filtering falls out for free: an unconfigured
  name → `factory.Get` returns nil → `capability[...]` returns
  `"provider X not configured"` → `auto` skips it.
- Explicit selection of an unconfigured provider (e.g. `-p tavily` with no key)
  now errors early with `"provider \"tavily\" not configured"` instead of
  failing later at the API call. This is a behavior change for explicit
  selection and is intended.
- Self-hosted/OSS providers (e.g. lightpanda) remain configured via `Host` even
  with no `APIKey`.

## Failover semantics

`auto` resolves its chain, then for each provider in order:

1. **Skip without attempting** if the provider is not configured or does not
   support the capability.
2. **Attempt** the call.
3. **Fall over to the next** on either:
   - an **error** (network, non-2xx, timeout, auth), or
   - an **empty result** — search returns 0 results; scrape returns empty
     content.
4. **Accept** the first non-empty success and return it.
5. If the chain is exhausted with no success → return an **aggregated error**
   naming every attempt and its individual failure, e.g.:

   ```
   auto search: exa: request timeout; brave: 401 unauthorized; tavily: 0 results
   ```

Empty-but-successful is treated as failure deliberately (chosen trade-off): the
user prefers a later provider's hit over an early provider's empty answer.

## Observability — return the chosen provider

A new capability interface in `provider/provider.go`:

```go
// Attempt records one provider tried by an auto chain. Err is nil for the
// provider that served the result.
type Attempt struct {
	Provider string
	Err      error
}

// AutoReporter is implemented by the auto meta-provider so the CLI can report
// which provider served a request and why earlier ones were skipped/failed.
type AutoReporter interface {
	Attempts() []Attempt
}
```

- The `auto` provider records one `Attempt` per provider it tried (in order);
  the served provider's `Attempt.Err` is nil.
- `main.go` — the only layer permitted to log — type-asserts `sp.(AutoReporter)`
  after the call and emits:
  - `logx.Warn("auto: %s failed: %v", a.Provider, a.Err)` per failed fallover,
  - `logx.Debug("auto: served by %s", served)` for the winner.
- This is read regardless of whether the call returned an error, so failed
  fallovers are visible even on total failure (and the aggregated error gives
  the top-level message). Honors "no logging in `provider/`" via the returned
  trail rather than direct logging.

## Factory & wiring

- `Factory` stores per-op chains: `chains map[string][]string` keyed by
  `"search"` / `"scrape"`. Populated from `main.go` after config load via a new
  setter (e.g. `SetAutoChain(op string, names []string)`). `main.go` resolves
  each op's chain as: `cfg.<Op>.Providers` if non-empty, else
  `defaultAutoChains[op]`. The factory then filters to configured/capable
  providers when it builds the meta-provider.
- `factory.Search("auto")` / `factory.Scrape("auto")`: detect the `"auto"` name,
  build the meta-provider (`autoSearch` / `autoScrape`) from the stored chain by
  resolving each name through the existing `capability[...]` helper (skipping
  unconfigured/unsupported). Any other name behaves exactly as today.
- **Cache**: scrape cache wrapping stays at the factory layer and wraps the
  `auto` provider, keyed by provider name `"auto"`. A cache hit short-circuits
  the whole chain; a miss caches the served content under `"auto"` (cache by
  URL, not by which provider happened to serve it).
- New file `provider/auto.go` holds `autoSearch` and `autoScrape`. Each embeds
  the resolved ordered capability list and an attempts slice. End the file with
  compile-time checks:
  `var _ SearchProvider = (*autoSearch)(nil)`,
  `var _ AutoReporter = (*autoSearch)(nil)`, and the `autoScrape` equivalents.

## Time-range warning

`main.go` currently warns (before the call) when the selected search provider
does not honor a requested time range. For `auto`:

- Warn once only if **no** provider in the resolved chain implements
  `TimeRangeSearcher` / `SupportsTimeRange()`. If at least one does, suppress the
  eager warning (the actual server is not known until after the call).

## `seek config init` behavior

`init` manages the per-op `provider` field but **never** reads or writes the
`providers:` chain — the priority order stays in code (`defaultAutoChains`) and
is only present in config.yaml if a user hand-edits it.

- The interactive selects and the `--search` / `--scrape` flags gain `"auto"` as
  a selectable value (listed first, so it is the obvious default). `validateProvider`
  must accept `"auto"` for search and scrape.
- `init` leaves `Operation.Providers` untouched (nil). Because the field is
  `omitempty`, a config written by `init` contains only `provider: auto` — no
  chain — and resolution falls through to `defaultAutoChains`.
- If a user has previously hand-added a `providers:` list, `init` loads the
  existing config first (it already does this at `runConfigInit`), so re-running
  `init` preserves that list rather than dropping it — it just never *creates* or
  *edits* one itself.
- `selectedProviders` (used to decide which API-key prompts to show) keys off the
  `provider` field. When `provider: auto`, it should expand to the resolved chain
  for that op so `init` prompts for the keys of the providers `auto` will use,
  rather than prompting for a literal provider named "auto".

## Documentation & surface updates

- `notes/providers.md`: document `auto`, the `providers:` chain, resolution
  order, and the configured-vs-unconfigured rule. Keep the capability matrix in
  sync.
- `config_cmd.go`: mention `auto` and the `providers:` list where providers are
  described.
- CLI flag usage strings (`providerFlag` in `main.go`) for search and scrape:
  include `auto` in the provider list.

## Testing

- Chain resolution: explicit `providers:` honored in order; absent list falls
  back to `providerEnv` order; unconfigured/unsupported names filtered out.
- Failover: first provider errors → second serves; first returns empty → second
  serves; all fail → aggregated error names every attempt.
- `NewFactory` skips keyless/hostless providers; self-hosted (Host only) still
  built.
- `AutoReporter.Attempts()` reflects the true attempt order and marks the
  served provider with nil `Err`.
- Cache: scrape via `auto` caches under `"auto"`; a hit skips the chain.

## Open behavior decisions (resolved)

- Chain source: explicit per-op config list (`providers:`) overrides a built-in
  default priority chain defined in code (`defaultAutoChains`). `seek config init`
  never reads or writes the `providers:` list. ✔
- Failover trigger: errors **and** empty results. ✔
- Activation: `auto` is a selectable value **and** the new default for search +
  scrape. ✔
- Op scope: search + scrape only; crawl unchanged. ✔
- Observability: return the chosen provider (via `AutoReporter`). ✔
- Keyless providers: skipped in `NewFactory` (accepted behavior change). ✔
- Total failure: aggregated error listing all attempts. ✔
