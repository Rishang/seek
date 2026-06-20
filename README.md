<p align="center">
  <img src="assets/logo.svg" width="180" alt="seek badger logo">
</p>

<h1 align="center">seek</h1>

<p align="center">
  <em>The web tool for CLI coding agents. The OpenRouter for web search — one key dies, the next one answers.</em>
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

**seek is a web tool for CLI coding agents — not for humans.**

Coding agents running open-source models — OpenCode, pi, Kilo Code, and friends — usually ship without web access. They can't pull the current version of a library, a breaking API change, or today's doc page; they answer from stale training data instead.

seek is the missing tool you hand the agent. One command in front of seven search / scrape / crawl providers, with automatic failover so a rate-limited or dead key never stalls the agent mid-task. You install it and point your agent at the bundled **web-fetch skill**; the agent does the rest — a cheap *search → decide → scrape* loop for up-to-date docs, run entirely by the model, not by you.

The human's job is one-time setup (`seek config init`, drop in the skill). After that every `seek` call is made by an agent. That framing drives every design choice below: CSV-first output to save tokens, snippet-before-scrape guards in the skill, invisible failover, and an MCP/HTTP surface so *any* agent — including the one reading this — can call it.

## For coding agents

This is the whole point. Most open-source-model agents have no built-in web access; seek gives them one, and ships a skill that teaches the cheap path instead of burning tokens.

[`skills/SKILL.md`](skills/SKILL.md) is a `web-fetch` skill. Put `seek` on the agent's `PATH` and point the agent at the skill (copy or symlink it into the agent's skills directory), and it learns the loop:

1. `seek search -o csv "<query>"` → read the snippets. The snippet is substantial — often it already *is* the answer, so it stops there.
2. Only if a detail is missing, `seek scrape "<url>"` the single best result → full markdown.
3. Stop the moment the objective is met.

The skill encodes hard token-budget guards — snippets before scrapes, one page at a time, pipe large `llms.txt` indexes through `rg`, no `crawl` unless explicitly asked — so research stays cheap. Failover is invisible to the agent: it calls `seek search`, and whichever provider answers first wins.

The CLI skill is the MVP for terminal coding agents. seek also runs as a server for everything else: [`seek mcp`](#seek-mcp--mcp-server) speaks the Model Context Protocol over stdio so any MCP-capable agent can call the same search/scrape/crawl tools, and [`seek serve`](#seek-serve--http-api) exposes them as an HTTP+JSON API.

## Why not just give the agent a provider SDK?

Because keys rate-limit and die mid-task, and one SDK is one point of failure. Wiring a fallback yourself means two SDKs, two auth flows, a try/catch, and a normalization layer to make their responses line up — then teaching the agent all of it.

With seek the agent learns one command:

```bash
seek search "rust async runtimes"
```

`auto` tries the configured providers in priority order until one returns a result. The agent never sees the 401, the rate-limit, or the dead key — it just gets an answer and keeps working.

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

## Install (one-time, by you)

This is the human part. seek builds from source — you need Go 1.25+ (and optionally [Task](https://taskfile.dev) for the shortcuts).

```bash
git clone https://github.com/rishang/seek
cd seek
task build            # or: cd src && go build -o ../bin/seek .
./bin/seek --help
```

Put `bin/seek` on the agent's `PATH` and you're done.

## Setup & wiring the agent

```bash
# 1. Configure providers and keys (interactive form) — one time
seek config init

# 2. Hand the agent the skill so it learns the cheap loop
mkdir -p <your-agent>/skills/web-fetch
cp skills/SKILL.md <your-agent>/skills/web-fetch/   # or symlink it

# 3. (Optional) sanity-check the commands the agent will run
seek search "best go web frameworks 2026"
seek scrape https://go.dev -f markdown
seek config view                                  # what's configured, which keys are set
```

After this, you stop typing `seek` — the agent runs it. Non-interactive setup works too, for scripting the install:

```bash
seek config init --search auto --key firecrawl=fc-xxx --key tavily=tvly-xxx --yes
```

## Commands

| Command | What it does |
|---------|--------------|
| `seek search <query>` | Web search across providers (`auto` by default). |
| `seek scrape <url>` | Extract a page as markdown, html, or json. Prints the raw content. |
| `seek crawl <url>` | Crawl a site and return its pages. |
| `seek serve` | Run an HTTP API (JSON) for search/scrape/crawl, with Swagger docs at `/docs`. |
| `seek mcp` | Run an MCP server over stdio so MCP-capable agents can call the same tools. |
| `seek config init` | Create or edit `config.yaml` and provider keys — interactive form, or pass flags / pipe input for non-interactive mode. |
| `seek config view` | Show the effective configuration and which API keys are set. |

### Flags

Global: `-v, --verbose` prints debug logs (including each `auto` failover, every HTTP request, and every MCP message) to stderr.

| Command | Flags |
|---------|-------|
| `search` | `-p/--provider`, `--start DD/MM/YYYY`, `--end DD/MM/YYYY`, `--range N` (last N days), `-o/--output json\|csv`, `--no-cache` |
| `scrape` | `-p/--provider`, `-f/--format markdown\|html\|json`, `--no-cache` |
| `crawl` | `-p/--provider`, `-o/--output json\|csv`, `--no-cache` |
| `serve` | `--addr host:port` (default `127.0.0.1:8787`), `--token` (or `SEEK_SERVE_TOKEN`) |
| `config init` | `--search`, `--scrape`, `--crawl`, `--format`, `--ttl <days>`, `--cache`, `--store`, `--key name=value`, `--host name=url`, `--path`, `-y/--yes` |

## Serve & MCP

Beyond the CLI, seek can run as a long-lived service so other programs and agents reuse the same providers and failover.

### `seek serve` — HTTP API

```bash
seek serve --addr 127.0.0.1:8787 --token "$SEEK_SERVE_TOKEN"
```

JSON endpoints (each request is handled concurrently by `net/http`):

```bash
curl -s localhost:8787/search -H "Authorization: Bearer $TOKEN" \
  -d '{"query":"rust async runtimes","range":7}'
curl -s localhost:8787/scrape -H "Authorization: Bearer $TOKEN" \
  -d '{"url":"https://go.dev","format":"markdown"}'
curl -s localhost:8787/crawl  -H "Authorization: Bearer $TOKEN" \
  -d '{"url":"https://go.dev/doc"}'
```

- `GET /healthz` — liveness check.
- `GET /docs` — Swagger UI; `GET /openapi.json` — the OpenAPI 3 spec. Both are public; the operation endpoints require the token.
- **Auth:** set `--token` (or `SEEK_SERVE_TOKEN`) to require `Authorization: Bearer <token>`. Without a token the API is **unauthenticated** — anyone who can reach the address can spend your provider keys, so only bind a tokenless server to loopback. seek warns at startup when no token is set.

### `seek mcp` — MCP server

```bash
seek mcp
```

Speaks the Model Context Protocol over stdio (newline-delimited JSON-RPC 2.0): `initialize`, `tools/list`, `tools/call`. It exposes three tools — `search`, `scrape`, `crawl` — backed by the same provider factory and failover. Requests are handled concurrently; logs go to stderr so stdout stays a clean protocol channel. Point any MCP-capable agent at the `seek mcp` command.

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
