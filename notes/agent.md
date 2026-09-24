# seek agent

`seek agent "question"` (issue #4) has two modes. Both go through `opSearch` / `opFetch`, so auto failover and the fetch cache apply.

## Default: shallow (`agent.Run`)

Built for speed: one search and one LLM call, with no page fetches.

```
opSearch(question)  → top N unique results (agent.sources / --sources, default 5)
model               → one markdown answer from the numbered title/URL/snippet list, citing [n]
seek                → appends `---` + `sources:` with `[n] [title](url)` lines (never model-generated)
```

## `--deep`: deep research (`agent.Research`)

A tool-calling loop in which `model` decides what to do. This is where issue #4's extract → dedupe work happens. The model gets two tools that wrap seek:

| Tool | Args | What the model gets back |
|---|---|---|
| `search` | `query` | Up to 10 results: title, URL, snippet |
| `fetch` | `url`, optional `focus` | The page distilled by `extract_model` against `focus` (the question by default), labelled `Source [n]` |

- **Tool calls in one turn run in parallel.** A URL is fetched only once per run; asking for it again returns the facts already recorded.
- **Tool errors don't end the run.** They go back to the model as `error: ...` text so it can adapt. Failed fetches are listed in `skipped`.
- **Step cap:** after `max_steps` turns (default 12), the model is called once more without tools and told to answer from what it has.
- **Output:** `sources` holds only pages that were fetched, `facts` is the merged set, and seek appends the `---` / `sources:` URL list.

## Shared

- **LLM backend:** only the OpenAI Chat Completions API (`POST {base_url}/chat/completions`, with `tools` / `tool_calls` for `--deep`). There is no vendor-specific code.
- **Reasoning effort:** `reasoning_effort` (config) or `-e/--reasoning-effort` is sent as the top-level `reasoning_effort` field on the main `model`'s requests only. The extraction model never gets it, because small models often reject it. When unset it is left out, and seek doesn't check the value; the endpoint does.
- **Config:** the `agent:` block in `provider.yaml` holds `base_url`, `api_key`, `model`, `extract_model`, `reasoning_effort`, `sources` and `max_steps`. `SEEK_AGENT_API_KEY` overrides the key, and an empty key sends no `Authorization` header. `SaveProviders` keeps the block when rewriting the file; `seek config init` edits it in an optional last step (or via `--agent-*` flags) through `config.SaveAgent`.
- **Failures:** shallow mode fails when search returns nothing, or on an LLM error or an empty answer. `--deep` fails on an LLM transport error or an empty final answer.
- **Code:**
  - `src/agent/openai.go`: the client
  - `src/agent/agent.go`: shallow mode, extraction and merge
  - `src/agent/research.go`: the deep loop
  - `src/agent_cmd.go`: CLI wiring. `-v` traces each step and ends with the source list (`[n] url title`) through `Deps.Trace` → `logx.Debug`, so the library itself never logs.

## Known limits (ponytail)

- **`--deep` dedupe only catches exact matches after normalization.** Near-duplicates are left for the research model to merge. Next step if needed: embedding or small-model clustering.
- **`--deep` cuts pages off at 30k characters.** Next step: chunk long pages.
- **`--deep` keeps the whole conversation in context.** The research model only sees distilled facts, but a very long run still grows. Next step: summarize earlier turns.
- **Not in v1:** time-range flags, an `agent` tool in MCP/serve, cross-run memory.
