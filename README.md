<p align="center">
  <img src="assets/logo.svg" width="180" alt="seek logo">
</p>

<h1 align="center">seek</h1>

<p align="center">
  <strong>Live web access for CLI coding agents — 7 providers, one command, automatic failover.</strong>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.25-111111?style=flat-square" alt="Go 1.25">
  <img src="https://img.shields.io/github/stars/rishang/seek?style=flat-square&color=111111&label=stars" alt="Stars">
  <img src="https://img.shields.io/github/v/release/rishang/seek?style=flat-square&color=111111&label=release" alt="Release">
  <img src="https://img.shields.io/badge/license-MIT-111111?style=flat-square" alt="MIT license">
</p>

<p align="center">
  <a href="#-quick-start">Quick Start</a> ·
  <a href="#-wire-into-your-agent">Wire Into Your Agent</a> ·
  <a href="#-providers">Providers</a> ·
  <a href="#-commands">Commands</a> ·
  <a href="#-faq">FAQ</a>
</p>

---

> **seek is a tool *for* coding agents, not for humans.**  
> You install it once. Claude Code, Cline, OpenCode, Gemini CLI — your agent calls it on every web lookup. You never type `seek` again.

---

## The Problem

Your coding agent needs the current version of a library. The docs that changed last week. A breaking API update that happened after its training cutoff. Without real web access, it hallucinates from stale data.

So you hand it a search provider key — and then it rate-limits. Or expires. Or the agent stalls mid-task, waiting on a 401 it doesn't know how to recover from.

**seek fixes this.** It sits in front of 7 search/fetch/crawl providers and fails over silently. Your agent calls `seek search`. If Tavily is rate-limited, Exa answers. If Exa is down, Firecrawl answers. The agent sees a result, never an error.

---

## How Failover Works

```
seek search "rust async runtimes 2026"
     │
     ├─ tavily      → 401 (key expired)   → next
     ├─ exa         → 429 (rate limited)  → next
     └─ firecrawl   → 200 ✓               → result
```

The agent never knows. It called `seek search`. It got an answer. It kept working.

---

## ⚡ Quick Start

**Step 1 — Install seek** (one time, done by you)

```sh
curl -fsSL https://raw.githubusercontent.com/Rishang/seek/main/install.sh | sh
```

**Step 2 — Add your provider keys**

```sh
seek config init   # interactive form — paste in your API keys
```

You need at least one key from any of the [7 supported providers](#-providers). Free tiers work.

**Step 3 — Wire it into your agent**

```sh
# Claude Code
mkdir -p ~/.claude/skills/web-fetch
cp skills/SKILL.md ~/.claude/skills/web-fetch/

# Any MCP agent (Cline, Cursor, OpenCode, Gemini CLI...)
# Add to your agent's MCP config:
{ "command": "seek", "args": ["mcp"] }
```

That's it. Your agent now has live web access with automatic failover across providers.

---

## 🔌 Wire Into Your Agent

seek has three integration modes — pick the one your agent speaks.

### Skill  _(Claude Code · any skills-directory agent)_

Drop `skills/SKILL.md` into the agent's skills folder:

```sh
mkdir -p ~/.claude/skills/web-fetch
cp skills/SKILL.md ~/.claude/skills/web-fetch/
```

The agent reads the skill and learns the **cheap search loop**:

1. `seek search -o csv "<query>"` — read the snippets. Often the snippet *is* the answer. Stop here.
2. Only if a detail is missing → `seek fetch "<best-url>"` — one page, full markdown.
3. Stop the moment the objective is met.

Token-budget guards are baked into the skill so research stays cheap — snippets before fetches, one page at a time, no crawling unless asked.

### MCP Server  _(Cline · Cursor · OpenCode · Gemini CLI · anything MCP-native)_

Register `seek mcp` as a stdio MCP server in your agent's config:

```json
{
  "mcpServers": {
    "seek": {
      "command": "seek",
      "args": ["mcp"]
    }
  }
}
```

Exposes `search`, `fetch`, and `crawl` as MCP tools — same providers, same failover, same output.

### HTTP API  _(any other tool · webhooks · custom pipelines)_

```sh
seek serve --addr 127.0.0.1:8787 --token "$SEEK_SERVE_TOKEN"
```

```sh
curl -s localhost:8787/search \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"query": "latest golang release notes"}'

curl -s localhost:8787/fetch \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"url": "https://go.dev/doc/devel/release", "format": "markdown"}'
```

Swagger UI at `GET /docs`, OpenAPI spec at `GET /openapi.json`.

### Let the agent set itself up

```sh
claude "read https://github.com/Rishang/seek/blob/main/README.md, install seek, and register its web-fetch skill"
# works the same for any agent that can read a URL and run shell commands
```

---

## Why seek instead of a provider SDK?

| | Hardcode one SDK | seek |
|---|---|---|
| Provider goes down | Task fails | Silent failover to next provider |
| Rate limit hit | Task fails | Silent failover to next provider |
| Key expires | Task fails | Silent failover to next provider |
| Add a new provider | Code changes | Add one key to config |
| Agent integration | Custom glue code per agent | One skill file or `seek mcp` |
| Token efficiency | Raw HTML response | CSV snippets by default |
| Output format | Provider-specific | Normalized across all providers |

---

## 🌐 Providers

7 providers, one interface. Configure one or all — `auto` uses whatever has a key.

| Provider | search | fetch | crawl | Key env var |
|---|---|---|---|---|
| firecrawl | ✓ | ✓ | ✓ | `FIRECRAWL_API_KEY` |
| tavily | ✓ | ✓ | ✓ | `TAVILY_API_KEY` |
| spider.cloud | ✓ | ✓ | ✓ | `SPIDER_API_KEY` |
| exa | ✓ | ✓ | — | `EXA_API_KEY` |
| brave | ✓ | — | — | `BRAVE_API_KEY` |
| webcrawlerapi | — | ✓ | ✓ | `WEBCRAWLERAPI_API_KEY` |
| lightpanda | — | ✓ | — | `LIGHTPANDA_API_KEY` |

`firecrawl` and `lightpanda` are **self-hostable** — set a custom host via `seek config init --host name=url` to point at your own instance. An env var always overrides a stored key.

**Auto priority order:** `tavily → exa → firecrawl → spider.cloud → webcrawlerapi → lightpanda → brave`. Reorder in `config.yaml` to change preference; index 0 wins.

---

## 📋 Commands

| Command | What it does |
|---|---|
| `seek search <query>` | Web search with auto-failover across providers |
| `seek fetch <url>` | Fetch a page as markdown, html, or json |
| `seek crawl <url>` | Crawl a site and return its pages |
| `seek mcp` | Start MCP server over stdio (JSON-RPC 2.0) |
| `seek serve` | Start HTTP API with Swagger at `/docs` |
| `seek config init` | Configure providers and API keys (interactive or `--yes` for scripting) |
| `seek config view` | Show current config and which keys are set |
| `seek version` | Print the seek version |

### Key Flags

**Global:** `-v, --verbose` — prints debug logs including each failover, HTTP request, and MCP message to stderr.

| Command | Flags |
|---|---|
| `search` | `-p/--provider`, `--start DD/MM/YYYY`, `--end DD/MM/YYYY`, `--range N`, `-o json\|csv`, `--no-cache` |
| `fetch` | `-p/--provider`, `-f/--format markdown\|html\|json`, `--no-cache` |
| `crawl` | `-p/--provider`, `-o json\|csv`, `--no-cache` |
| `serve` | `--addr host:port` (default `127.0.0.1:8787`), `--token` |
| `config init` | `--search`, `--fetch`, `--crawl`, `--format`, `--ttl <days>`, `--key name=value`, `--host name=url`, `-y/--yes` |

---

## ⚙️ Configuration

Settings live in `~/.seek/config.yaml`. API keys live in `~/.seek/provider.yaml` (written `0600` — never committed, never logged).

```yaml
# ~/.seek/config.yaml
config:
  search:
    provider: auto
  fetch:
    provider: auto
    cache:
      enabled: true
      ttl: 1296000      # 15 days in seconds
      store: sqlite
    options:
      output_format: markdown
  crawl:
    provider: firecrawl

providers:
  priority:             # index 0 = highest priority
    - tavily
    - exa
    - firecrawl
    - spider.cloud
    - webcrawlerapi
    - lightpanda
    - brave
```

Non-interactive setup (great for CI or dotfiles):

```sh
seek config init --key firecrawl=fc-xxx --key tavily=tvly-xxx --yes
```

### Caching

`fetch` and `crawl` results cache locally in SQLite. `search` always hits live. Bypass per-request with `--no-cache`.

### Environment Variables

| Variable | Effect |
|---|---|
| `<PROVIDER>_API_KEY` | Override stored key for that provider |
| `SEEK_CONFIG` | Path to `config.yaml` (default `~/.seek/config.yaml`) |
| `SEEK_PROVIDERS` | Path to `provider.yaml` (default `~/.seek/provider.yaml`) |
| `SEEK_CACHE=off` | Disable all caching globally |
| `SEEK_CACHE_DB` | Relocate the SQLite cache file |
| `SEEK_CACHE_TTL` | Global TTL override (Go duration, e.g. `72h`) |

---

## 📦 Install Options

All methods drop a `seek` binary on your `PATH`.

```sh
# Install script — Linux / macOS
curl -fsSL https://raw.githubusercontent.com/Rishang/seek/main/install.sh | sh

# Pin a version or install dir
curl -fsSL .../install.sh | SEEK_VERSION=v0.1.0 SEEK_BIN_DIR=~/.local/bin sh

# mise
mise use -g ubi:Rishang/seek          # latest
mise use -g ubi:Rishang/seek@0.1.0    # pinned

# ubi
ubi --project Rishang/seek --in ~/.local/bin

# From source (Go 1.25+)
git clone https://github.com/rishang/seek && cd seek
task build   # or: cd src && go build -o ../bin/seek .
```

**Supported targets:** Linux and macOS (`amd64` / `arm64`), Windows `.zip` → [Releases](https://github.com/Rishang/seek/releases)

---

## 🛠 Development

```sh
task build      # build bin/seek
task test       # go test ./...
task vet        # go vet ./...
task lint       # golangci-lint
task run -- search "query"
```

All Go source lives in `src/`. Run `task` with no arguments to list every available task.

Contributions are welcome — open an issue to discuss before a large PR.

---

## ❓ FAQ

**Which provider does `auto` pick?**  
The first one in `providers.priority` that supports the operation (search/fetch/crawl) and has a key configured. Reorder the list in `config.yaml` to change preference.

**What if a provider isn't in my priority list?**  
It's still tried — appended after the listed ones. A typo in the list never silently drops a usable provider.

**Do I need all 7 providers?**  
No. One key is enough. `auto` works with whatever it finds. More keys = more resilience.

**Where do my API keys go?**  
`~/.seek/provider.yaml` with `0600` permissions. Never in `config.yaml`. Set env vars to override stored keys at runtime.

**Can I self-host the search backend?**  
Yes — `firecrawl` and `lightpanda` are self-hostable. Set a custom host via `seek config init --host firecrawl=http://your-host`.

**Is seek useful without an agent?**  
It works fine from the CLI, but the design is optimized for agents — CSV output, token-efficient snippets, skill files, MCP server. For human use, the provider UIs are probably friendlier.

---

## License

MIT — [Rishang](https://github.com/Rishang)