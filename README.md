<p align="center">
  <img src="assets/logo.svg" width="180" alt="seek badger logo">
</p>

<h1 align="center">seek</h1>

<p align="center">
  <em>The OpenRouter for web search. One key dies, the next one answers.</em>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.25-111111?style=flat-square" alt="Go 1.25">
  <img src="https://img.shields.io/github/stars/rishang/seek?style=flat-square&color=111111&label=stars" alt="Stars">
  <img src="https://img.shields.io/github/v/release/rishang/seek?style=flat-square&color=111111&label=release" alt="Release">
  <img src="https://img.shields.io/badge/license-MIT-111111?style=flat-square" alt="MIT license">
</p>

<p align="center">
  <strong>search &middot; scrape &middot; crawl &middot; 7 providers &middot; one interface &middot; automatic failover</strong>
</p>

---

Every web-search provider has its own SDK, its own auth, and its own response shape. You wire up Firecrawl, bolt on Tavily as a backup, write the failover glue, then do it all again for scraping.

seek puts one CLI in front of all of them. You run `seek search`; it picks a provider, and when a key rate-limits or 401s it falls through to the next one — without you noticing.

## Before / after

You want a web search with a fallback when your primary provider is down.

Without seek: two SDKs, two auth flows, a try/catch, and a normalization layer to make their responses line up.

With seek:

```bash
seek search "rust async runtimes"
```

`auto` tries your configured providers in priority order until one returns a result.

## How it works

`search` and `scrape` default to the `auto` provider. It never restricts which providers you can use — it just orders them:

```
seek search "rust async runtimes"
        │
        ├─ take providers.priority (config.yaml, or the built-in default)
        ├─ keep the ones capable of this operation AND holding a key
        │
        ├─ tavily      → 401         → next
        ├─ exa         → rate limited → next
        └─ firecrawl   → 200 ✓        → result
```

Index `0` in `providers.priority` is highest priority. Membership comes from which keys you've configured; a provider with no key is simply skipped. `crawl` defaults to `firecrawl` (no failover).

## Install

seek builds from source. You need Go 1.25+ (and optionally [Task](https://taskfile.dev) for the shortcuts).

```bash
git clone https://github.com/rishang/seek
cd seek
task build            # or: cd src && go build -o ../bin/seek .
./bin/seek --help
```

Put `bin/seek` on your `PATH` and you're done.

## Quickstart

```bash
# 1. Configure providers and keys (interactive form)
seek config init

# 2. Search, scrape, crawl
seek search "best go web frameworks 2026"
seek scrape https://go.dev -f markdown
seek crawl https://go.dev/doc -o json

# 3. See what's configured and which keys are set
seek config view
```

Non-interactive setup works too:

```bash
seek config init --search auto --key firecrawl=fc-xxx --key tavily=tvly-xxx --yes
```

## Commands

| Command | What it does |
|---------|--------------|
| `seek search <query>` | Web search across providers (`auto` by default). |
| `seek scrape <url>` | Extract a page as markdown, html, or json. Prints the raw content. |
| `seek crawl <url>` | Crawl a site and return its pages. |
| `seek config init` | Create or edit `config.yaml` and provider keys — interactive form, or pass flags / pipe input for non-interactive mode. |
| `seek config view` | Show the effective configuration and which API keys are set. |

### Flags

Global: `-v, --verbose` prints debug logs (including each `auto` failover) to stderr.

| Command | Flags |
|---------|-------|
| `search` | `-p/--provider`, `--start DD/MM/YYYY`, `--end DD/MM/YYYY`, `--range N` (last N days), `-o/--output json\|csv`, `--no-cache` |
| `scrape` | `-p/--provider`, `-f/--format markdown\|html\|json`, `--no-cache` |
| `crawl` | `-p/--provider`, `-o/--output json\|csv`, `--no-cache` |
| `config init` | `--search`, `--scrape`, `--crawl`, `--format`, `--ttl <days>`, `--cache`, `--store`, `--key name=value`, `--host name=url`, `--path`, `-y/--yes` |

## Providers

| Provider | search | scrape | crawl | Key env var |
|----------|:------:|:------:|:-----:|-------------|
| firecrawl | ✓ | ✓ | ✓ | `FIRECRAWL_API_KEY` |
| tavily | ✓ | ✓ | ✓ | `TAVILY_API_KEY` |
| spider.cloud | ✓ | ✓ | ✓ | `SPIDER_API_KEY` |
| webcrawlerapi | | ✓ | ✓ | `WEBCRAWLERAPI_API_KEY` |
| lightpanda | | ✓ | | `LIGHTPANDA_API_KEY` |
| brave | ✓ | | | `BRAVE_API_KEY` |
| exa | ✓ | ✓ | | `EXA_API_KEY` |

`firecrawl` and `lightpanda` are self-hostable; set a `host` (via `seek config init --host name=url`) to point at your own instance. An env var, when set, overrides the key stored in `provider.yaml`.

## Configuration

Settings live in `config.yaml`; secrets live in a separate `provider.yaml` (written with `0600` permissions). Both default to `~/.seek/` and are managed by `seek config init`.

`config.yaml`:

```yaml
config:
  search:
    provider: auto
  scrape:
    provider: auto
    cache:
      enabled: true
      ttl: 1296000      # seconds (15 days); omit to use the default
      store: sqlite
    options:
      output_format: markdown
  crawl:
    provider: firecrawl
    cache:
      enabled: true
      ttl: 1296000
      store: sqlite

providers:
  priority:             # auto try-order; index 0 = highest. Omit to use the built-in default.
    - tavily
    - exa
    - firecrawl
    - spider.cloud
    - webcrawlerapi
    - lightpanda
    - brave
```

`provider.yaml`:

```yaml
providers:
  firecrawl:
    api_key: fc-xxx
  tavily:
    api_key: tvly-xxx
```

### Caching

`scrape` and `crawl` results are cached in a local SQLite store; `search` always hits the provider live. Use `--no-cache` to bypass the cache for a single request.

### Environment variables

| Variable | Effect |
|----------|--------|
| `<PROVIDER>_API_KEY` | Override the stored key for a provider (see the table above). |
| `SEEK_CONFIG` | Path to `config.yaml` (default `~/.seek/config.yaml`). |
| `SEEK_PROVIDERS` | Path to `provider.yaml` (default `~/.seek/provider.yaml`). |
| `SEEK_CACHE=off` | Disable all caching. |
| `SEEK_CACHE_DB` | Relocate the SQLite cache store. |
| `SEEK_CACHE_TTL` | Global TTL override (a Go duration, e.g. `72h`). |

## Development

```bash
task build      # build bin/seek
task test       # go test ./...
task vet        # go vet ./...
task lint       # golangci-lint
task run -- search "query"
```

All Go code lives under `src/`. Run `task` with no arguments to list every task.

## FAQ

**Which provider does `auto` pick?**
The first one in `providers.priority` that supports the operation and has a key. Reorder the list to change preference; index `0` wins.

**What if a provider isn't in `providers.priority`?**
If it's capable and configured, it's still tried — appended after the listed ones. A typo in the list never drops a usable provider.

**Do I have to configure all seven?**
No. Configure one. `auto` works with whatever keys it finds.

**Where do my API keys go?**
`provider.yaml`, written `0600`. Never `config.yaml`. An env var overrides the file when set.

## License

MIT.
