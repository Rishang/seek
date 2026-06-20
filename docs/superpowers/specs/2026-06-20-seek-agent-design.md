# seek agent — design

**Status:** Draft for review
**Date:** 2026-06-20

## Summary

Add a `seek agent` command: a one-shot autonomous research agent. Given a
question, an OpenAI-compatible LLM drives a tool-calling loop over seek's
existing capabilities (search / scrape / crawl) plus a keyword memory
(recall / remember), then prints a single cited markdown answer and exits.

The agent is an **orchestrator that uses the provider `Factory`** — not a new
provider capability. It reuses all configured providers, caching, and
time-range logic unchanged.

## Goals

- `seek agent "question"` → cited markdown answer, then exit.
- LLM backend: any OpenAI-compatible endpoint (OpenAI, OpenRouter, local
  servers) via configurable base URL + key + model.
- Native function-calling loop: the model chooses which seek tool to call.
- Cross-run memory via SQLite FTS5 (keyword recall), agent-driven through
  `recall` / `remember` tools.
- Stay a **pure-Go static binary** — no CGo, no new heavy dependencies.

## Non-goals (explicitly deferred)

- **Vector / semantic memory.** Considered and rejected for v1: it would force
  a CGo SQLite driver (Turso `go-libsql`) or a remote Turso account, breaking
  seek's pure-Go cross-compiled binary. FTS5 keyword recall covers the v1 need.
  Revisit only if a semantic-recall requirement emerges.
- Interactive REPL / multi-turn chat.
- Pluggable multi-vendor LLM factory (single OpenAI-compatible client for v1).
- Per-agent / multi-tenant databases.

## Architecture

New `src/agent/` package (replaces the current empty stub):

| Unit | Responsibility | Depends on |
|------|----------------|------------|
| `agent.go` | The loop: question → tool-calls → final answer; enforces `max_steps` | LLM client, tool registry |
| `openai.go` | OpenAI-compatible chat-completions client with tool-calling | shared `req/v3` HTTP |
| `tools.go` | Tool JSON-schema specs + dispatch (name → handler) | `provider.Factory`, memory |
| `memory.go` | FTS5-backed `remember` / `recall` over a local SQLite file | `modernc.org/sqlite` |

The CLI gains an `agent` command in `main.go`, wired like `search`/`scrape`/`crawl`.

### Why these boundaries

- **`agent.go`** knows nothing about HTTP or SQL — only "ask the LLM, run the
  tools it requests, repeat." Testable with a fake LLM client and fake tools.
- **`openai.go`** is a thin transport: messages + tool specs in, assistant
  message or tool calls out. Testable against an `httptest` mock server.
- **`tools.go`** is the only place that bridges LLM tool calls to seek
  capabilities and memory. Adding a tool = one spec + one handler here.
- **`memory.go`** is a self-contained store mirroring `cache/sqlite.go` style.

## Data flow (the loop)

```
seek agent "question"
  → build system prompt + user question
  → loop (max_steps):
      POST {base_url}/chat/completions  (messages + tool specs)
      ← assistant message
        ├─ has tool_calls? → run each via tools.go dispatch
        │     search/scrape/crawl → factory.<Cap>()  (uses cache + providers)
        │     recall/remember     → memory.go (FTS5 sqlite)
        │     append each result as a tool message → continue loop
        └─ final answer (no tool_calls)? → done
  → print cited markdown answer to stdout; exit
```

**Tool errors are not fatal.** A failed tool call returns its error text back to
the model as the tool result, so the agent can adapt (retry, try another
provider, or work around it). The loop only fails on LLM transport errors or on
exhausting `max_steps` without a final answer.

## Tools exposed to the model

| Tool | Arguments | Maps to |
|------|-----------|---------|
| `search` | `query`, optional `time_range` | `factory.Search()` |
| `scrape` | `url` | `factory.Scrape()` |
| `crawl` | `url` | `factory.Crawl()` |
| `recall` | `query` | memory FTS5 `MATCH`, top-k by bm25 rank |
| `remember` | `note`, optional `source_url` | memory `INSERT` |

Each tool has a JSON-schema spec sent in the `tools` array of the
chat-completions request.

## Memory store

A dedicated SQLite file (default `~/.config/seek/memory.db`), separate from the
scrape/crawl cache, using `modernc.org/sqlite` (pure-Go).

```sql
CREATE TABLE IF NOT EXISTS findings (
    id         INTEGER PRIMARY KEY,
    question   TEXT NOT NULL,   -- the research question that produced this
    note       TEXT NOT NULL,   -- the finding the agent chose to remember
    source_url TEXT,            -- provenance (optional)
    created_at INTEGER NOT NULL -- unix seconds
);

CREATE VIRTUAL TABLE IF NOT EXISTS findings_fts USING fts5(
    question, note, source_url,
    content='findings', content_rowid='id'
);
```

- `remember(note, source_url)` → `INSERT INTO findings (...)`.
- `recall(query)` →
  ```sql
  SELECT f.note, f.source_url, f.created_at
  FROM findings_fts fts JOIN findings f ON f.id = fts.rowid
  WHERE findings_fts MATCH ?
  ORDER BY rank LIMIT 5;
  ```
  Results are returned to the model with a human-readable age.

**Trade-off (documented):** FTS5 matches words, not meaning. A re-query with
different phrasing may miss a relevant prior finding. Accepted for v1 in
exchange for staying pure-Go.

**Implementation note to verify in the plan:** confirm `modernc.org/sqlite`
compiles in FTS5 (expected yes in current versions). If not, fall back to a
`LIKE`-based recall on the `findings` table — same interface, no schema churn.

## Configuration

The agent's full configuration lives in **`provider.yaml`** (the 0600 secrets
file), not `config.yaml` — since it carries the API key, keeping the whole block
next to it means one read gives the agent everything (key + endpoint + model). A
new typed top-level `agent:` block is added to the `provider.yaml` file struct:

```go
// providersFile is the on-disk shape of provider.yaml.
type providersFile struct {
	Providers map[string]Credential `yaml:"providers"`
	Agent     *AgentConfig          `yaml:"agent,omitempty"` // optional; absent when unset
}

// AgentConfig is the full agent configuration. Stored in provider.yaml because
// it carries the API key. base_url is derived from provider unless set.
type AgentConfig struct {
	Provider string `yaml:"provider"`            // openai|openrouter|gemini|anthropic|ollama
	Model    string `yaml:"model"`
	APIKey   string `yaml:"api_key,omitempty"`   // optional for ollama
	BaseURL  string `yaml:"base_url,omitempty"`  // optional override of the provider default
	MaxSteps int    `yaml:"max_steps,omitempty"` // 0 → built-in default (12)
}

// Built-in OpenAI-compatible endpoints, keyed by provider name. Mirrors the
// providerHostDefaults pattern in config_cmd.go.
var agentBaseURLs = map[string]string{
	"openai":     "https://api.openai.com/v1",
	"openrouter": "https://openrouter.ai/api/v1",
	"gemini":     "https://generativelanguage.googleapis.com/v1beta/openai",
	"anthropic":  "https://api.anthropic.com/v1",
	"ollama":     "http://localhost:11434/v1",
}

// ResolveBaseURL returns the effective endpoint: explicit base_url wins,
// else the provider's built-in default; unknown provider with no base_url is
// an error.
func (a AgentConfig) ResolveBaseURL() (string, error) { /* ... */ }
```

`provider.yaml` (the only file the agent reads):

```yaml
providers:
  brave:
    api_key: bsa-xxxxx
  firecrawl:
    api_key: fc-xxxxx
    host: https://api.firecrawl.dev
agent:                                    # optional top-level block; absent when unset
  provider: openrouter                    # base_url derived from this
  model: anthropic/claude-sonnet-4-6
  api_key: sk-or-v1-xxxxx
  max_steps: 12
# ollama is the override case: local host/port, no API key
# agent:
#   provider: ollama
#   model: llama3.1
#   base_url: http://localhost:11434/v1
```

- `base_url` is **optional** for the known cloud providers (derived from
  `provider` via `agentBaseURLs`). It is the field that matters for **ollama**
  (local host/port).
- **ollama needs no API key** — `api_key` is optional when `provider: ollama`.
- **Key resolution order:** env `SEEK_AGENT_API_KEY` → `agent.api_key` in
  provider.yaml → (for non-ollama) a clear error pointing at `seek config init`.
- The block is optional: a provider.yaml without it is valid; `seek agent`
  errors clearly when it is absent.

### config init (optional agent section)

In `src/config_cmd.go`. The agent block is written to **provider.yaml** (via
`SaveProviders`), not config.yaml:

- **Interactive:** a new `huh.NewGroup` — a `Confirm` "Configure the research
  agent? (optional)". Only when accepted: **Select** `provider`
  (openai/openrouter/gemini/anthropic/ollama) → input `model` → input
  `max_steps` → masked `api_key` (skipped for ollama) → a `base_url` input shown
  **only when provider == ollama** (defaulted to `http://localhost:11434/v1`,
  reusing the `providerHostDefaults` host-prompt mechanism).
- **Non-interactive:** new flags `--agent-provider`, `--agent-model`,
  `--agent-key`, `--agent-base-url`, `--agent-max-steps` (added to
  `initValueFlags`), written into the `agent:` block.
- **config view:** print an `agent` block (`provider` / `model` / effective
  `base_url` / `max_steps`, and key `set`/`missing`) when configured. Show
  nothing when unset.

## CLI

```
seek agent [flags] "question"

Flags:
  --provider, -p   override search/scrape/crawl provider for this run (optional)
  --max-steps      override configured loop cap
  --output         markdown (default) | json   → {answer, sources[]}
```

- **stdout** = the answer (markdown, or JSON when `--output json`). **stderr** =
  `logx` traces only (tool calls at `Debug`, recoverable issues at `Warn`).
  Keeps stdout pipeable, per project rules.

## Error handling (per project logging rules)

- `agent/`, `memory.go` are library code: wrap errors with context and return
  them; never log.
- `main.go` agent action owns logging: `logx.Error` + exit on fatal (LLM
  transport failure, missing agent config, `max_steps` exhausted);
  `logx.Debug` for the resolved config and per-step tool traces.
- Tool-level failures degrade into model feedback (returned as the tool result),
  not loop termination.

## Testing

- **openai.go:** `httptest` mock chat-completions server — assert request shape
  (messages, tool specs) and parse of `tool_calls` vs. final answer.
- **agent.go loop:** scripted fake LLM client (emits a tool call, then a final
  answer) + fake tool handlers — assert the right tools run and the answer
  returns; assert `max_steps` termination.
- **tools.go:** dispatch each tool against a fake `Factory` and in-memory store;
  assert error text is surfaced (not swallowed) on tool failure.
- **memory.go:** remember → recall round-trip on a temp DB, like
  `cache/sqlite_test.go`; assert FTS ranking returns the relevant row and
  excludes unrelated topics.

## Open implementation details (for the plan)

- Exact system prompt and citation format (inline links + trailing sources).
- Whether `crawl` is exposed by default (it is the most expensive/slow tool) or
  gated behind a flag.
- Token/step budgeting beyond `max_steps` (out of scope unless trivial).
