# seek agent

`seek agent "question"` (issue #4) has two modes. Both go through `opSearch` / `opFetch`, so auto failover and the fetch cache apply.

## Default: shallow (`agent.Run`)

Built for speed. seek searches (splitting a multi-part question into 2–3 focused queries via `extract_model`) and ranks the hits without an LLM. The model then sees the top N results (`agent.sources` / `--sources`, default 8) and either answers from the snippets or reads a few of them first.

- **Tools:** `read` (up to 3 result ids per call, 4 pages per run, verbatim excerpts focused on the question) and one extra `search`. Both are budgeted in code.
- **Turns:** at most 2 tool turns, then a tool-free answer. Simple questions cost one LLM call and no fetches.
- **Output:** seek appends `---` + `sources:` with `[n] [title](url)` lines (never model-generated).

## `--deep`: deep research (`agent.Research`)

A fixed pipeline driven by code, not a tool-calling loop. The models plan, extract and write, but never decide when to search or stop, so there are no wasted tool calls. The answer model sees only facts verified against their pages.

```
plan    extract_model → checklist of 1–5 parts, each {need, query}   (the question is searched meanwhile)
search  each part's query, in parallel
read    up to 6 pages, round-robin across the question's and each part's results (≤2 per host, no video/social)
        → cleanPage + focusPage (30k-char excerpt) → extract_model → facts {claim, quote, parts, relevance}
verify  keep a fact only if relevance ≥ 2, its quote is on the page (whole words; "..." splits pieces)
        and every number in the claim is on the page
cover   a part is covered once a kept fact names it
gap     uncovered parts: search each part's `need` once, read its next 2 best unread pages
answer  model, once, no tools: question + checklist (covered / not found) + merged facts with source ids
```

- **Stopping:** reading stops when every part is covered, after `max_steps` rounds (default 2: the first read plus one gap round), or when a gap round covers nothing new. So a run costs 1 plan call, 1–(parts+1) searches plus at most one gap search per part, one extract call per page (parallel), and **one** answer-model call.
- **Merge:** claims are duplicates when their word sets overlap ≥ 0.8 (Jaccard) *and* they state the same numbers, so "SDK 35" and "SDK 36" never merge. Facts rank by relevance, then by how many sources agree; the top 60 go to the answer model.
- **Sources:** only pages that yielded a kept fact are numbered, so every id the model can cite is a real source. `knownCites` strips `[n]` above the last id (but not `args[7]`-style code).
- **Failures:** if every search fails, or no page yields a fact, the run errors without calling the answer model. Pages that fail to fetch (20s timeout) or extract are listed in `skipped`.

## Shared

- **LLM backend:** only the OpenAI Chat Completions API (`POST {base_url}/chat/completions`, with `tools` / `tool_calls` for `--deep`). There is no vendor-specific code.
- **Reasoning effort:** `reasoning_effort` (config) or `-e/--reasoning-effort` is sent as the top-level `reasoning_effort` field on the main `model`'s requests only. The extraction model never gets it, because small models often reject it. When unset it is left out, and seek doesn't check the value; the endpoint does.
- **Config:** the `agent:` block in `provider.yaml` holds `base_url`, `api_key`, `model`, `extract_model`, `reasoning_effort`, `sources` and `max_steps`. `SEEK_AGENT_API_KEY` overrides the key, and an empty key sends no `Authorization` header. `SaveProviders` keeps the block when rewriting the file; `seek config init` edits it in an optional last step (or via `--agent-*` flags) through `config.SaveAgent`.
- **Failures:** shallow mode fails when search returns nothing, or on an LLM error or an empty answer. `--deep` fails on an LLM transport error or an empty final answer.
- **Code:**
  - `src/agent/openai.go`: the client
  - `src/agent/shallow.go`: shallow mode
  - `src/agent/agent.go`: shared ranking and page trimming, plus extraction, grounding and merge
  - `src/agent/research.go`: the deep pipeline
  - `src/agent_cmd.go`: CLI wiring. `-v` traces each step and ends with the source list (`[n] url title`) through `Deps.Trace` → `logx.Debug`, so the library itself never logs.

## Known limits (ponytail)

- **`--deep` dedupe is lexical.** Paraphrases with little word overlap stay separate; the answer prompt merges them. Next step if needed: embedding or small-model clustering.
- **`--deep` reads one 30k-char excerpt per page.** `focusPage` keeps the most relevant sections, but facts outside it are lost. Next step: chunk long pages.
- **`--deep` doesn't follow links.** Gap rounds search again rather than following a page's links (e.g. to a changelog). Next step if needed: let the extractor suggest one URL per gap.
- **The quote check costs recall.** A fact whose quote the extractor paraphrased is dropped; `-v` shows how many per page.
- **Not in v1:** time-range flags, cross-run memory.
