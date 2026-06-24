<p align="center">
  <img src="assets/logo.svg" width="180" alt="seek logo">
</p>

<h1 align="center">seek</h1>

<p align="center">
  <strong>Live web access for CLI coding agents — 8 providers, one command, automatic failover.</strong>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.25-111111?style=flat-square" alt="Go 1.25">
  <img src="https://img.shields.io/github/stars/rishang/seek?style=flat-square&color=111111&label=stars" alt="Stars">
  <img src="https://img.shields.io/github/v/release/rishang/seek?style=flat-square&color=111111&label=release" alt="Release">
  <img src="https://img.shields.io/badge/license-MIT-111111?style=flat-square" alt="MIT license">
</p>

<p align="center">
  <a href="#-quick-start">Quick Start</a> ·
  <a href="#-the-skill--how-the-agent-learns-to-use-seek">The SKILL</a> ·
  <a href="#-install-in-one-shot--let-the-agent-do-it">One-Shot Install</a> ·
  <a href="#-manual-setup--per-agent">Per-Agent Setup</a> ·
  <a href="#-providers">Providers</a> ·
  <a href="#-commands">Commands</a> ·
  <a href="#-faq">FAQ</a>
</p>

---

> **seek is a tool *for* coding agents, not for humans.**  
> You install it once. Claude Code, Cline, OpenCode, Antigravity CLI — your agent calls it on every web lookup. You never type `seek` again.

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

**Step 1 — Install seek** (one time, choose one)

```sh
# Install script — Linux / macOS
curl -fsSL https://raw.githubusercontent.com/Rishang/seek/main/install.sh | sh

# install-release (ir)
ir get https://github.com/Rishang/seek

# mise
mise use -g github:Rishang/seek
```

**Step 2 — Add your provider keys**

```sh
seek config init   # interactive form — paste in your API keys
```

You need at least one key from any of the [7 supported providers](#-providers). Free tiers work.

**Step 3 — Wire it into your agent** _(easiest: just ask your agent to do it)_

```sh
# Claude Code — run in terminal (one-shot; needs shell access)
claude -p "install seek from https://github.com/Rishang/seek and register its web-fetch skill" \
  --permission-mode acceptEdits

# OpenCode — run in terminal
opencode run "install seek and wire it as an MCP server — ref https://github.com/Rishang/seek"

# Antigravity CLI (agy) — run in terminal (one-shot; needs shell access)
agy -p "install seek from https://github.com/Rishang/seek and configure it as an MCP server" \
  --dangerously-skip-permissions

# Cline / Cursor / any MCP agent — paste in chat (no shell wrapper):
# install seek from https://github.com/Rishang/seek and add it as an MCP server

# Pi — native pi-package with auto-install
pi install git:github.com/Rishang/seek
```

The agent reads this README, runs the install, and wires itself up. You just supply the API keys.

→ Need to do it manually? See [per-agent setup](#-manual-setup--per-agent).

---

## 🧠 The SKILL — How the Agent Learns to Use seek

[`skills/SKILL.md`](https://github.com/Rishang/seek/blob/main/skills/SKILL.md) is a plain Markdown file you drop into your agent's skills folder. The agent reads it once and learns everything — what commands to run, when to stop, how to stay cheap. No prompting, no wrapping, no custom code required.

The file has a metadata header that agents use to discover and load it:

```
name:        web-fetch
description: Search the web and fetch full page content via seek
             (firecrawl/tavily/brave/spider). Use for research,
             fact-checking, documentation lookups, or any task
             requiring current web information.
```

---

### The Loop: search → decide → maybe fetch → stop

```
seek search -o csv "<query>"
# output: title,url,snippet,published_date  (one row per result)
```

The snippet is substantial — often several sentences, not a teaser. **For many lookups the snippet already is the answer.**

```
Step 1 — Search
  seek search -o csv "<query>"  →  read snippets + URLs

Step 2 — Decide
  Snippets answer the objective?
    YES → STOP. Answer from snippets. Don't fetch.
    NO  → fetch the single most relevant URL.

Step 3 — Fetch (only if needed)
  seek fetch "<url>"  →  full page as markdown

Step 4 — Stop the moment the objective is met.
  First page missed → fetch the next best ONCE, then re-decide.
  Never fetch a second URL "to be thorough."
```

---

### Shortcuts That Skip Search Entirely

The SKILL also teaches the agent smarter paths when search isn't needed:

**Have the exact URL?**
```sh
seek fetch "https://<host>/<path>"
# Done. No search round-trip.
```

**Know the doc site domain but not the path?**
```sh
# Guess the conventional structure first (Mintlify/Docusaurus pattern)
seek fetch "https://docs.example.com/<topic>/overview"
# Real content? Done — the page's own nav exposes every sibling URL.
```

**Know the domain but not the structure?**
```sh
# Fetch the index and grep — one piped command
seek fetch "https://<host>/llms.txt" | rg "<keyword>" | head -20
# Hit  → fetch that URL
# Miss → fall back to seek search
```

> If `llms.txt` returns "omitted" / "truncated" / "pages omitted" — skip the grep, go straight to `seek search`.

---

### Hard Limits Built Into the SKILL

These guards are baked in so the agent never burns your token budget:

| Rule | Why |
|---|---|
| Snippets before fetches | Every fetch is a full page of tokens |
| One page at a time | Never batch-fetch; read before getting the next |
| Pipe `llms.txt` through `rg`, never dump it raw | Indexes can be enormous |
| No `seek crawl` unless explicitly asked | Crawl pulls many pages — for lookups, one search + one fetch is enough |
| Empty fetch → one retry max, then fall back to `seek search` | Never loop fetches |
| Not for APIs or local files | Use `curl` / `bash` / `read` instead |

---

### SKILL vs MCP — Which Should You Use?

| | SKILL (Markdown file) | MCP Server (`seek mcp`) |
|---|---|---|
| How the agent gets it | Reads a `.md` file from its skills dir | Registered as a stdio MCP server |
| Token efficiency | Agent follows the built-in loop + guards | Agent decides when/how to call tools |
| Works with | Claude Code, Kilo Code, any skills-dir agent (incl. [Pi](https://github.com/earendil-works/pi) addon) | Cline, Cursor, OpenCode, Antigravity CLI, any MCP agent |
| Setup | Drop one file | Add one JSON block |
| Recommended for | Claude Code users | Everyone else |

Both use the same seek binary, same providers, same failover underneath.

---

## 🚀 Install in One Shot — Let the Agent Do It

This is the fastest path. **You don't need to manually copy files or edit configs.** Run one command in your terminal, or paste a prompt into your agent's chat. It reads this README, runs the install, drops the skill file, and reports back. You only need to supply your provider API keys afterwards (`seek config init`).

### Claude Code

```sh
claude -p "install seek from https://github.com/Rishang/seek and register its web-fetch skill so you can search the web in future tasks" \
  --permission-mode acceptEdits
```

Or start an interactive session with the same prompt: `claude "install seek …"`.

Claude reads the README, runs the install script, and copies `SKILL.md` into `~/.claude/skills/web-fetch/` automatically.

### OpenCode

```sh
opencode run "install seek and wire it as an MCP server — ref https://github.com/Rishang/seek/blob/main/README.md"
```

### Cline (VS Code)

In the Cline chat panel:

```
install seek from https://github.com/Rishang/seek and add it as an MCP server to my Cline config
```

### Cursor

In Cursor's composer:

```
install seek (https://github.com/Rishang/seek) and register it as an MCP server in my .cursor/mcp.json
```

### Antigravity CLI (agy)

```sh
agy -p "install seek from https://github.com/Rishang/seek and configure it as an MCP server" \
  --dangerously-skip-permissions
```

Or start an interactive session with the same prompt: `agy -i "install seek …"`.

### Pi

If you use [Pi](https://github.com/earendil-works/pi) as your agent harness, register the skill (Pi's native integration — no MCP needed):

```sh
pi -p "install seek from https://github.com/Rishang/seek and register its web-fetch skill so you can search the web in future tasks"
```

Or start an interactive session: `pi "install seek …"`.

### Any other agent

The pattern is always the same — point the agent at this README and tell it what to do (in chat, no shell wrapper):

```
install seek from https://github.com/Rishang/seek and wire it up so you can search the web
```

Any agent that can read a URL and run shell commands can set itself up.

---

## 🔌 Manual Setup — Per Agent

Prefer to wire it up yourself? Here are the exact steps per agent.

### Claude Code — Skill (recommended)

The Skill is the native integration for Claude Code. Drop it in once; Claude reads it on every session.

```sh
# Global (all projects)
mkdir -p ~/.claude/skills/web-fetch
curl -fsSL https://raw.githubusercontent.com/Rishang/seek/main/skills/SKILL.md \
  -o ~/.claude/skills/web-fetch/SKILL.md

# Project-only
mkdir -p .claude/skills/web-fetch
curl -fsSL https://raw.githubusercontent.com/Rishang/seek/main/skills/SKILL.md \
  -o .claude/skills/web-fetch/SKILL.md
```

That's it. No config file, no restart. Claude picks up the skill on the next session.

### Pi — Native pi-package (recommended)

seek ships as a native [pi package](https://pi.dev/packages). One install — extension + skill, everything wired automatically.

```sh
pi install git:github.com/Rishang/seek
```

**What you get:**
- **Auto-install extension** — on session start, checks if `seek` is on PATH. If not, prompts for one-click install (downloads the right binary for your OS/arch from GitHub releases). Non-interactive mode shows the manual install command.
- **`/seek-install` command** — manual (re)install anytime.
- **`web-fetch` skill** — registered automatically via `resources_discover`. Invoke with `/skill:web-fetch` or let Pi load it when relevant.

**Manual fallback** — drop the skill file directly:

```sh
# Global (all projects)
mkdir -p ~/.pi/agent/skills/web-fetch
curl -fsSL https://raw.githubusercontent.com/Rishang/seek/main/skills/SKILL.md \
  -o ~/.pi/agent/skills/web-fetch/SKILL.md

# Project-only
mkdir -p .pi/skills/web-fetch
curl -fsSL https://raw.githubusercontent.com/Rishang/seek/main/skills/SKILL.md \
  -o .pi/skills/web-fetch/SKILL.md
```

### OpenCode — MCP Server

Add to `~/.config/opencode/config.json` (or your project's `opencode.json`):

```json
{
  "mcp": {
    "seek": {
      "command": "seek",
      "args": ["mcp"]
    }
  }
}
```

### Cline (VS Code) — MCP Server

Open the Cline MCP settings (gear icon → MCP Servers) and add:

```json
{
  "seek": {
    "command": "seek",
    "args": ["mcp"],
    "disabled": false
  }
}
```

Or edit `~/Library/Application Support/Code/User/globalStorage/saoudrizwan.claude-dev/settings/cline_mcp_settings.json` directly.

### Cursor — MCP Server

Create or edit `~/.cursor/mcp.json`:

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

Restart Cursor. seek will appear in the available tools list.

### Antigravity CLI (agy) — MCP Server

Add to `~/.gemini/antigravity-cli/mcp_config.json` (or `.agents/mcp_config.json` in your project):

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

Use `/mcp` inside `agy` to verify the server is connected.

### Kilo Code / Aider / Any MCP-native agent

The MCP config block is the same for every agent that speaks MCP (JSON-RPC 2.0 over stdio):

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

`seek mcp` exposes three tools — `search`, `fetch`, `crawl` — with the same provider failover as the CLI. The agent sees tools, not providers.

### HTTP API — Any custom tool or pipeline

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

Swagger UI at `GET /docs` · OpenAPI spec at `GET /openapi.json` · Liveness at `GET /healthz`

> ⚠️ Without `--token`, the API is unauthenticated — anyone who can reach the port can spend your provider keys. Always set a token, or bind to loopback only.

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

8 providers, one interface. Configure one or all — `auto` uses whatever has a key.

| Provider | search | fetch | crawl | Key env var |
|---|---|---|---|---|
| firecrawl | ✓ | ✓ | ✓ | `FIRECRAWL_API_KEY` |
| tavily | ✓ | ✓ | ✓ | `TAVILY_API_KEY` |
| spider.cloud | ✓ | ✓ | ✓ | `SPIDER_API_KEY` |
| exa | ✓ | ✓ | — | `EXA_API_KEY` |
| perplexity | ✓ | — | — | `PERPLEXITY_API_KEY` |
| brave | ✓ | — | — | `BRAVE_API_KEY` |
| webcrawlerapi | — | ✓ | ✓ | `WEBCRAWLERAPI_API_KEY` |
| lightpanda | — | ✓ | — | `LIGHTPANDA_API_KEY` |

`firecrawl` and `lightpanda` are **self-hostable** — set a custom host via `seek config init --host name=url` to point at your own instance. An env var always overrides a stored key.

**Auto priority order:** `tavily → exa → firecrawl → spider.cloud → webcrawlerapi → lightpanda → brave → perplexity`. Reorder in `config.yaml` to change preference; index 0 wins. `perplexity` is last by default since its search quality is more variable for general queries.

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
# Pi native package (extension + skill, auto-install)
pi install git:github.com/Rishang/seek

# Install script — Linux / macOS
curl -fsSL https://raw.githubusercontent.com/Rishang/seek/main/install.sh | sh

# Pin a version or install dir
curl -fsSL .../install.sh | SEEK_VERSION=v0.1.0 SEEK_BIN_DIR=~/.local/bin sh

# mise
mise use -g github:Rishang/seek          # latest
mise use -g github:Rishang/seek@0.1.0    # pinned

# install-release (ir)
ir get https://github.com/Rishang/seek

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

**Do I need all 8 providers?**  
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