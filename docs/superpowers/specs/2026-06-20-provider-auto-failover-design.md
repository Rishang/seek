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

Two separate concerns, deliberately kept in separate places:

- **Membership** — *which* providers `auto` may use — is owned by
  **provider.yaml** (plus env overrides). Any provider the user has configured a
  key/host for, that supports the operation's capability, is automatically a
  member of the chain. There is **no** provider list in config.yaml duplicating
  this; adding a key in provider.yaml is the only step needed to include a
  provider.
- **Order** — the try-priority among configured members — comes from a built-in
  code default, optionally reordered by an *optional* per-op hint in config.yaml.
  The hint can only reorder; it can never remove a configured provider from the
  chain.

`Operation` gains an optional ordering hint (NOT a membership list):

```go
type Operation struct {
	Provider string      `yaml:"provider"`
	Priority []string    `yaml:"priority,omitempty"` // optional auto try-order hint; reorders only
	Cache    CacheConfig `yaml:"cache,omitempty"`
	Options  Options     `yaml:"options,omitempty"`
}
```

config.yaml — default case (what `init` writes; no `priority`):

```yaml
config:
  search:
    provider: auto
  scrape:
    provider: auto
  crawl:
    provider: firecrawl        # unchanged, single provider
```

config.yaml — user nudges order (hand-edited, optional):

```yaml
config:
  search:
    provider: auto
    priority: [brave, exa]     # try brave then exa FIRST; every other configured
                               # provider still runs, after these, in code order
```

### Default priority ranking (in code)

```go
// defaultAutoChains is the built-in try-order ranking per operation. It ranks
// the providers actually configured (provider.yaml/env) — it does NOT decide
// membership. Should list every capable provider so the ranking is total;
// any configured provider absent here is appended last. Not written to
// config.yaml by `seek config init`.
var defaultAutoChains = map[string][]string{
	"search": {"exa", "brave", "tavily", "firecrawl", "spider.cloud"},
	"scrape": {"firecrawl", "spider.cloud", "lightpanda", "tavily", "exa", "webcrawlerapi"},
}
```

(Lives alongside the existing per-capability lists in `config_cmd.go`
— `searchProviders` / `scrapeProviders` — and must stay a subset of them. The
exact ordering above is a reasonable starting point and can be adjusted.)

### Chain resolution for an op when `provider: auto`

`main.go` owns the ordering policy and produces an ordered *candidate* list;
the factory then filters that list to providers that are actually configured and
support the capability (preserving order). The candidate order is built by
concatenating these and de-duplicating (first occurrence wins):

1. `op.Priority` (the optional config hint), in the order given.
2. `defaultAutoChains[op]` (the code baseline).
3. `providerEnv` order (safety net so a configured provider missing from both
   lists above is never dropped — it lands at the end).

After filtering, every configured + capable provider appears exactly once, with
the user's hinted providers first, then code-default order, then any remainder.
Membership is never narrowed by the hint — only reordered.

`config.Default()` changes:

- `Search.Provider = "auto"`
- `Scrape.Provider = "auto"`
- `Crawl.Provider = "firecrawl"` (unchanged)
- `Priority` is left **nil/empty** for every op. Order comes from
  `defaultAutoChains`; membership from whatever the user has keys for.

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

- `Factory` stores per-op candidate orders: `chains map[string][]string` keyed
  by `"search"` / `"scrape"`. Populated from `main.go` after config load via a
  new setter (e.g. `SetAutoChain(op string, names []string)`). `main.go` builds
  each op's candidate order by concatenating + de-duplicating
  `cfg.<Op>.Priority` ++ `defaultAutoChains[op]` ++ `providerEnv` order (see
  "Chain resolution"). This is an ordered *candidate* list, not the final chain.
- `factory.Search("auto")` / `factory.Scrape("auto")`: detect the `"auto"` name,
  build the meta-provider (`autoSearch` / `autoScrape`) by resolving each
  candidate name through the existing `capability[...]` helper and **keeping only
  those that resolve** (configured + capable), preserving order. The result is
  the final chain. Any other name behaves exactly as today.
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

`init` sets the per-op `provider` field and collects API keys into provider.yaml.
It **never** writes the `priority:` hint — order lives in code
(`defaultAutoChains`) and only appears in config.yaml if a user hand-edits it.

Key idea: when an op is `auto`, **membership comes from which keys the user
configures during init** (provider.yaml), so init must let the user configure
*multiple* providers, not a single one.

- The interactive provider selects and the `--search` / `--scrape` flags gain
  `"auto"` as a selectable value (listed first, the obvious default).
  `validateProvider` must accept `"auto"` for search and scrape.
- `init` leaves `Operation.Priority` untouched (nil). Because it is `omitempty`,
  a config written by `init` contains only `provider: auto` — no priority list —
  and resolution falls through to `defaultAutoChains`. A user's hand-added
  `priority:` is preserved on re-run (init loads existing config first at
  `runConfigInit`); init never creates or edits it.
- **Key prompting with auto.** Today `selectedProviders` returns the single
  provider chosen per op and `runCredsForm` prompts for those keys. With `auto`
  selected for an op, there is no single provider, so:
  - Replace the "literal auto" expansion with a step that prompts the user to
    pick *which capable providers to set up* for that op. Concretely, after the
    settings form, for each op set to `auto`, present a multi-select of that op's
    capable providers (`searchProviders` / `scrapeProviders`), pre-checked for
    any already configured in provider.yaml. The union of picks across ops is
    the set passed to `runCredsForm`.
  - This keeps provider.yaml as the single source of membership: the keys the
    user enters here are exactly the providers `auto` will try. Nothing about the
    set is written to config.yaml.
  - Non-interactive mode: `--key name=value` (repeatable, already exists) is how
    membership is supplied; `--search auto` / `--scrape auto` set the mode. No
    new flag is required for the chain.
- `selectedProviders` is updated so a literal `"auto"` value never reaches the
  credential prompt as a provider name; auto ops contribute their multi-selected
  providers instead.

## Documentation & surface updates

- `notes/providers.md`: document `auto`, that membership comes from configured
  providers (provider.yaml), the code-default order + optional `priority:` hint,
  and the configured-vs-unconfigured rule. Keep the capability matrix in sync.
- `config_cmd.go`: mention `auto` and the optional `priority:` hint where
  providers are described.
- CLI flag usage strings (`providerFlag` in `main.go`) for search and scrape:
  include `auto` in the provider list.

## Testing

- Chain resolution: membership = configured + capable providers only; a
  `priority:` hint moves listed providers to the front without dropping any
  unlisted-but-configured provider; absent hint → `defaultAutoChains` order;
  a configured provider absent from both lists is appended last (via
  `providerEnv` safety net); unconfigured/unsupported names never appear.
- Failover: first provider errors → second serves; first returns empty → second
  serves; all fail → aggregated error names every attempt.
- `NewFactory` skips keyless/hostless providers; self-hosted (Host only) still
  built.
- `AutoReporter.Attempts()` reflects the true attempt order and marks the
  served provider with nil `Err`.
- Cache: scrape via `auto` caches under `"auto"`; a hit skips the chain.

## Open behavior decisions (resolved)

- Chain membership: derived from configured providers in provider.yaml/env
  (filtered by capability) — NOT a list in config.yaml. Order: built-in
  `defaultAutoChains` ranking, optionally reordered by an additive `priority:`
  hint per op that never drops a configured provider. `seek config init` never
  reads or writes `priority:`. ✔
- Failover trigger: errors **and** empty results. ✔
- Activation: `auto` is a selectable value **and** the new default for search +
  scrape. ✔
- Op scope: search + scrape only; crawl unchanged. ✔
- Observability: return the chosen provider (via `AutoReporter`). ✔
- Keyless providers: skipped in `NewFactory` (accepted behavior change). ✔
- Total failure: aggregated error listing all attempts. ✔
