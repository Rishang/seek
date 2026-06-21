# seek

**The OpenRouter for web search.** One CLI, pluggable providers for `search`, `fetch`, and `crawl`. Pick a provider per operation; swap them without touching call sites.

Module `github.com/rishang/seek`, rooted in `src/`. Go 1.25, `urfave/cli/v3`, `imroc/req/v3`.

## Layout

- `src/main.go` — CLI commands (`search`, `fetch`, `crawl`, `config`), flag parsing, wiring.
- `src/provider/` — one file per provider + the capability interfaces and shared HTTP client.
- `src/config/` — config.yaml + provider.yaml schemas and the value types shared across providers.
- `src/cache/` — SQLite result cache (fetch/crawl only; search always hits the provider).
- `src/logx/` — leveled stderr logger (`SEEK_LOG`). Keep stdout clean for piping.
- `src/output.go` — `--output json|csv` rendering.
- `notes/` — seek-side implementation notes. Read before changing providers:
  [`notes/providers.md`](notes/providers.md) has the capability matrix, auth/time-range/published-date
  mapping, and the add-a-provider checklist. Keep it in sync when you change provider behavior.
  Full upstream API shapes live in `.idea/providers.md`.

## Architecture

- **Provider registry.** `provider/registry.go` is the single source of truth for which providers
  exist, their env var, and their optional self-host default. Capabilities are *derived* by probing
  which interfaces each provider implements, so the matrix never drifts from the code. The CLI's
  capability lists (`config_cmd.go`), env mapping (`providerEnv` in `main.go`), `--provider` help
  text, and auto-chain ordering all read from it via `Providers()`, `NamesFor(cap)`, and
  `HostDefault(name)`.
- **Factory pattern.** `provider.Factory` (`provider/factory.go`) constructs every configured
  provider once (looking each up in the registry's `byName` map) and hands them out by name. Call
  sites never `new` a provider — they ask the factory for a capability.
- **The base type the factory is built on is `Provider`** (`provider/provider.go`), an alias for
  `any`: a provider need not satisfy any method. Each provider opts into the capability interfaces
  it supports — `SearchProvider`, `FetchProvider`, `CrawlProvider`.
  The factory stores providers as the base `Provider` and the generic `capability[T]` helper
  type-asserts to the requested capability, returning a descriptive error when a provider is
  unconfigured or doesn't implement that capability. Add a capability → new interface in
  `provider.go` + new accessor on `Factory`.
- **Capabilities, not classes.** A provider implements whichever of `SearchProvider` /
  `FetchProvider` / `CrawlProvider` it supports; there is no provider base class, only the
  `Provider` interface plus the capability interfaces.
- **Add a provider:** new `provider/<name>.go` embedding `*httpClient`; implement the capability
  methods; end the file with compile-time checks (`var _ SearchProvider = (*X)(nil)`); then add
  **one line** to `registry` in `provider/registry.go`. Optionally rank it in
  `config.DefaultPriority`. Everything else (factory, env mapping, init form, help text, auto chain)
  derives from the registry.
- **Shared HTTP** lives in `provider/client.go` (`post`/`get`/`expectOK`, Bearer auth). Only bypass
  it when a provider needs a different auth scheme (e.g. Brave's `X-Subscription-Token` header).
- **Options flow** CLI → `config.SearchOptions`/`FetchOptions` → each provider formats to its own
  API params. Cross-provider differences (e.g. time-range param formats) live in small helpers
  (`provider/timerange.go`), never inline.
- **Keys:** provider.yaml (0600) is the store; the matching env var overrides it.

## Code style — simple, clean, concise

- Small functions, early returns, no needless abstraction. Match the surrounding code's idiom.
- Comments explain *why*, not *what*. Every exported symbol gets a doc comment; keep it to a line or two.
- Errors: wrap with context (`fmt.Errorf("%s %s: %w", ...)`) and return up the stack — the top
  level in `main.go` logs them via `logx.Error`. Don't log-and-return the same error. For non-fatal
  degradations (cache off, bad config, unsupported time range), `logx.Warn` and continue — never
  swallow silently.
- `omitempty` on optional JSON/YAML fields so absent data drops out cleanly.
- stdout = data (for piping); stderr = logs via `logx`. Never mix them.
- Keep diffs minimal and focused; don't reformat untouched code.

### Where to use the logger (`logx`)

- **`main.go` / command actions only.** This is the top layer that decides what's fatal vs.
  recoverable, so it owns logging. `logx.Error` then `os.Exit(1)` for fatal; `logx.Warn` for
  recoverable degradations (cache disabled, bad config, ignored time range); `logx.Debug` for
  resolved-input traces (e.g. the computed time range).
- **`provider/`, `config/`, `cache/`, `output.go` — never log.** Library code returns errors;
  the caller decides whether to log, warn, or fail. A package that both logs and returns the same
  error makes it appear twice.
- **Three levels only:** `Debug` (input traces), `Warn` (recoverable degradations), `Error`
  (fatal, paired with exit). Controlled by `SEEK_LOG` (`debug`/`warn`/`error`/`off`); default `warn`.

## Workflow

Use the Taskfile: `task build`, `task test`, `task vet`, `task fmt`, `task run -- search "query"`.
Before finishing: `task fmt && task vet && task test`.
