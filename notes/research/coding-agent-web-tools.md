# How coding agents expose web search and web fetch

Research for `seek agent` v5 default mode. Researched 2026-09-23, using official docs and source code
at the commits listed under Sources. "Sourced" means the source says it directly. "Inference" means it
is my reading of the evidence.

## 1. TL;DR

- **The model decides what to read, in every agent that has web tools.** The only exception is Aider,
  where the user decides. No agent reads the top N results automatically. Search returns short
  results, and the model then calls fetch on the one or two URLs it wants.
- **Two shapes of fetch exist.** (a) *Prompt-driven fetch:* `url` + `prompt` go to a small, fast model,
  and the main model sees only that model's answer. Claude Code WebFetch and Gemini CLI `web_fetch`
  work this way, and Cline's `fetch_web_content` takes the same parameters. (b) *Raw fetch:* HTML is
  converted to markdown or text and cut to a fixed size. OpenCode (`webfetch`), Continue
  (`fetch_url_content`, 20k chars), the MCP reference `fetch` server (5k chars, paginated with
  `start_index`), Cline's default executor (50k chars) and Gemini's "direct" mode work this way.
- **Claude Code's docs call the prompt-driven fetch "lossy by design."** The prompt decides what
  reaches the main model. So if the result says a page doesn't mention X, that may only mean the
  prompt didn't ask about X.
- **Search results are small.** Claude Code returns titles + URLs only, and fetch is the follow-up.
  Gemini returns a grounded summary with `[n]` markers and a source list. Continue returns 5 results,
  each capped at 8k chars. OpenCode/Exa returns 8 results with up to 10k chars of context.
- **Common safeguards:** fetch only URLs already seen in context (Anthropic API), block private IPs
  and rate-limit each host (Gemini), return cross-host redirects to the model instead of following
  them (Claude Code), cache for 15 minutes (Claude Code), and wrap page text as untrusted (Gemini).
  Codex defaults to a pre-indexed search *cache* to reduce prompt-injection exposure.
- **Guidance to the model is short and says when to use the tool, not how often.** "Use this tool
  sparingly … Common programming questions do not require web search" (Continue). "Use this when you
  don't have a specific URL. If a search result requires deeper analysis, follow up by using
  web_fetch" (Gemini 3). Nobody puts a number of fetches in the prompt. Hard caps live in code
  (`max_uses`; Claude Code allows 200 searches per session).
- **What seek should copy:** seek runs search in code, then asks the main model once. That model
  either answers from the snippets or asks, in one parallel turn, to `read` specific result ids with a
  focus string. seek reads those pages (cached) and returns focused excerpts, then the model answers.
  The model decides what to read, the budget is fixed in code, and a snippet-only answer costs
  exactly one LLM call.

## 2. Comparison

| Agent | Search tool | Fetch tool | Fetch takes a prompt? | Secondary model extracts? | Size limit | Cache | Who decides | Guidance on when / how much |
|---|---|---|---|---|---|---|---|---|
| Claude Code | `WebSearch(query, allowed_domains?, blocked_domains?)` → titles + URLs; up to 8 backend searches per call | `WebFetch(url, prompt)` | Yes, required | Yes, a "small, fast model"; main model sees its answer | "fixed character limit" (value not documented) | 15 min per URL | Model | Search: "end with a Sources: list". Cap: 200 searches per session |
| Anthropic API server tools | `web_search` (`max_uses`, domains, `user_location`) → url, title, `page_age`, encrypted content | `web_fetch` (`max_uses`, domains, `citations`, `max_content_tokens`, `use_cache`) | No (`url` only) | No; optional "dynamic filtering" (model writes code to filter) | `max_content_tokens` | Yes, `use_cache` defaults to true | Model | Docs: fetch only when a specific page is named; search for current or changing facts |
| Gemini CLI | `google_web_search(query)` → grounded answer with `[n]` + source list (flash model + `googleSearch`) | `web_fetch(prompt)`: up to 20 URLs inside the prompt | Yes (URLs are inside it) | Yes, flash model + `urlContext`; fallback: raw fetch → html-to-text → flash | 250k chars total, split evenly across URLs; 10 MB body; 10 s timeout | None; 10 requests/min per host | Model | "Use this when you don't have a specific URL … follow up by using web_fetch" |
| Codex CLI | Hosted Responses `web_search` (`cached` by default, `indexed`, `live`) | None of its own; hosted actions `search` / `open_page` / `find_in_page` | n/a | n/a (server-side) | n/a | Cached index is the default mode | Model (inside OpenAI's tool) | Config only |
| OpenCode | `websearch(query, numResults=8, type, livecrawl, contextMaxCharacters=10000)` via Exa or Parallel MCP | `webfetch(url, format=markdown\|text\|html, timeout)` | No | No | 5 MB body; generic output cap of 2000 lines / 50 KB, with the full text saved to a file | None found | Model | "Results may be summarized if the content is very large"; include the current year in queries |
| Cline (SDK) | Provider-native `web_search` (`maxUses`, domains) | `fetch_web_content(requests[{url, prompt}])`, batched | Yes | Default executor: no. It returns the text with the prompt appended | 5 MB body; 50k chars | None found | Model | "Fetch independent URLs together in one call" |
| Continue | `search_web(query)` → 5 results, each ≤ 8k chars | `fetch_url_content(url)` | No | No | 20k chars | None found | Model | "Use this tool sparingly … Common programming questions do not require web search" |
| Aider | none | `/web <url>` command (Playwright or httpx → pandoc markdown) | No | No | None found | No | User | Offers "Add URL to the chat?" for URLs in the user's message |
| Goose | none built in (MCP extensions) | none built in; shell or MCP `fetch` | n/a | n/a | n/a | n/a | Model | "Use the shell … for accessing web sites or APIs" |
| Cursor | `web_search` (closed source) | `@Browser` / browser tool | Unknown | Unknown | Unknown | Unknown | Model | Docs: "Generate search queries and perform web searches" |
| MCP `fetch` server (reference) | n/a | `fetch(url, max_length=5000, start_index, raw)` | No | No | 5000 chars per call; paginated | No | Model | Truncated output says "Call the fetch tool with a start_index of N" |

## 3. Per-agent notes

### Claude Code (closed source; official docs + the tool descriptions shipped to the model)

- **WebFetch.** "WebFetch takes a URL and a prompt describing what to extract. It fetches the page,
  converts the response to Markdown when the server returns HTML, and runs the prompt against the
  content using a small, fast model. For most fetches, Claude receives that model's answer, not the
  raw page … This makes WebFetch lossy by design." [CC tools reference, "WebFetch tool behavior"].
  Which fetches are exempt from extraction ("most") is not documented.
- **Limits and behavior** [same section]:
  - HTTP is upgraded to HTTPS.
  - "Large pages are truncated to a fixed character limit before processing." The value isn't given.
  - Responses are cached for 15 minutes (`CLAUDE_CODE_WEBFETCH_CACHE_TTL_MS`).
  - The download deadline is 5 minutes.
  - A cross-host redirect comes back as a text result naming the target, and Claude makes a second
    call.
  - The `Accept` header prefers Markdown, so servers that support it can skip HTML conversion.
  - Domain permission rules (`WebFetch(domain:…)`), a preapproved set of documentation domains, and a
    "domain safety check" that runs first.
- **WebSearch.** "runs a query against Anthropic's web search backend and returns result titles and
  URLs. It doesn't fetch the result pages. To read a page Claude finds in search results, it follows up
  with WebFetch. The tool may issue up to eight backend searches per call." `allowed_domains` and
  `blocked_domains` can't be combined. There are at most 200 searches per session, and past the cap a
  call returns a notice to "continue with the information it already gathered" rather than an error
  [CC tools reference, "WebSearch tool behavior", "Session search limit"].
- **Tool description text** (as shipped to the model in this session):
  - WebSearch: "Returns result blocks with titles and URLs", "The current month is … use this when
    searching for recent information", and "After answering from results, end with a 'Sources:' list
    of the URLs you used".
  - WebFetch: "Fetches a URL, converts the page to markdown, and answers `prompt` against it using a
    small fast model", "Cross-host redirects are returned to you rather than followed", and "Responses
    are cached for 15 minutes per URL".
- **Parallelism:** the tools are independent calls, and the model can issue several in one turn
  (inference from the general tool model; no web-specific doc).

### Anthropic API server tools (`web_search`, `web_fetch`)

- **When it fetches:** "Claude fetches when the request points at a specific page or document … does
  **not** fetch for general-knowledge or open-ended questions" [web fetch tool, "When Claude fetches"].
- **When it searches:** search is for "information that is current, changing, or outside its training
  data" (news, prices, scores, organizations that may have changed). Claude answers directly for
  stable knowledge, and "triggering is steerable through your system prompt … For a hard constraint,
  use `max_uses`" [web search tool, "When Claude searches"].
- **Fetch returns the full document, not a summary.** It comes back as a `document` block (text or
  base64 PDF) with `retrieved_at`. `max_content_tokens` truncates it. Citations are optional and off
  by default [web fetch tool, "Content limits", "Citations", "Response"]. `use_cache` defaults to
  true, and the docs say to disable it only for fresh or rapidly changing sources, "because bypassing
  the cache increases latency" [web fetch tool, "Cache bypass"].
- **URL validation:** "the web fetch tool can only fetch URLs that have previously appeared in the
  conversation context" (user messages, client tool results, earlier search/fetch results). A URL
  that appears only in Claude's own output fails with `url_not_in_prior_context` [web fetch tool,
  "URL validation"]. This is the key defense against hallucinated URLs and exfiltration.
- **Search results:** each has `url`, `title`, `page_age` and `encrypted_content`. Citations are
  always on, and `cited_text` is ≤ 150 chars [web search tool, "Search results", "Citations"].
- **Dynamic filtering** (`web_search_20260209+`, `web_fetch_20260209+`): the model "writes and runs
  code that filters the results first, so only relevant content reaches the context window"
  [web search tool, "Dynamic filtering"]. Anthropic's second answer to page bloat, next to Claude
  Code's small-model extraction.
- **Budgets:** the combined example uses `web_search` `max_uses: 3` + `web_fetch` `max_uses: 5`
  [web fetch tool, "Combined search and fetch"].

### Gemini CLI (open source, `packages/core/src/tools/`)

- **`google_web_search(query)`.** It calls a utility model alias `web-search`, which is
  `gemini-3-flash-base` with `tools: [{googleSearch: {}}]` [`config/defaultModelConfigs.ts` L240-247].
  The flash model's grounded answer is returned with `[n]` markers placed at grounding-segment byte
  offsets, followed by `Sources:\n[1] title (uri)` [`web-search.ts` L90-180]. The main model gets a
  *summary*, never raw results.
- **Search description (Gemini 3 set):** "Returns a synthesized answer with citations … Use this when
  you don't have a specific URL. If a search result requires deeper analysis, follow up by using
  'web_fetch' on the provided URI." [`definitions/model-family-sets/gemini-3.ts` L396-410].
- **`web_fetch(prompt)`.** It takes a single `prompt` holding up to 20 URLs and instructions
  ("Summarize the breaking changes") [`gemini-3.ts` L412-427; `default-legacy.ts` L429-444].
  - The primary path sends the prompt plus an `<authorized_urls>` list to alias `web-fetch`, which is
    flash + `urlContext` [`web-fetch.ts` L770-810; `defaultModelConfigs.ts` L248-255]. Google fetches
    the page server-side, and a small model answers.
  - The fallback path fetches directly (10 s timeout, 10 MB cap), turns GitHub `blob` URLs into raw
    URLs, and converts HTML with `html-to-text`. It splits a 250,000-char budget evenly across the
    URLs, then asks flash "Follow the user's instructions below using the provided webpage content"
    [`web-fetch.ts` L41-46, L147-160, L289-510].
- **"Direct web fetch" (experimental).** The tool becomes `web_fetch(url)`: "Fetch content from a URL
  directly. Send multiple requests for this tool if multiple URL fetches are needed." It returns
  converted text wrapped by `wrapUntrusted(...)` with no model step [`web-fetch.ts` L595-760, schema
  override near L968-990].
- **Safeguards:** a private-IP/blocked-host check, a rate limit of 10 requests per host per minute
  (LRU), URL normalization and dedupe, and `sanitizeXml` on the user prompt [`web-fetch.ts` L48-80,
  L270-285, L360-385].
- **Cache:** none found in the tool itself.

### OpenAI Codex CLI (open source, `codex-rs/`)

- **No local fetch tool.** Codex exposes OpenAI's *hosted* Responses `web_search` tool (`ToolSpec::
  WebSearch` with `external_web_access`, `filters`, `user_location`, `search_context_size`)
  [`core/src/tools/hosted_spec.rs` L14-45].
- **Actions inside the hosted tool:** `Search{query|queries}`, `OpenPage{url}` and
  `FindInPage{url, pattern}` [`protocol/src/models.rs` L1953-1977]. So "fetch" and "find in page"
  happen inside OpenAI's search tool, and the client can't see or tune them.
- **Modes:** `WebSearchMode = Disabled | Cached (default) | Indexed | Live` [`protocol/src/
  config_types.rs` L376-381]. The docs say cached mode "returns pre-indexed results instead of
  fetching live pages. This reduces exposure to prompt injection" [Codex config basics, "Web search
  mode"].
- **What seek can use:** nothing directly. seek's constraint is Chat Completions, which has no
  equivalent hosted tool that works across vendors. The `find_in_page` idea is still useful: a cheap
  way to target a page without a model.

### OpenCode (open source, `packages/opencode/src/tool/`)

- **`webfetch(url, format, timeout)`.** Format defaults to `markdown` (Turndown, removing
  script/style/meta/link). The `Accept` header prefers `text/markdown`. Limits are 5 MB and a 30 s
  timeout (max 120 s). On a Cloudflare challenge it retries with UA `opencode`. There is no secondary
  model [`webfetch.ts` L9-22, L52-104, L182-192].
- **Webfetch description:** "Fetches the URL content, converts to requested format (markdown by
  default)" and "if another tool is present that offers better web fetching capabilities … prefer
  using that tool" [`webfetch.txt`].
- **`websearch(query, numResults, livecrawl, type, contextMaxCharacters)`.** It calls Exa MCP
  `web_search_exa` (default 8 results) or Parallel's `web_search` (`objective` +
  `search_queries`), with a 25 s timeout. It returns the provider's LLM-ready text, which already
  contains page content [`websearch.ts` L10-97; `mcp-websearch.ts`].
- **Websearch description:** "returns the content from the most relevant websites" and "The current
  year is {{year}}. You MUST use this year when searching for recent information"
  [`websearch.txt`].
- **Truncation (all tools):** 2000 lines / 50 KB head. The full output goes to a file, with the hint
  "Use Grep to search the full content or Read with offset/limit" [`truncate.ts` L14-15, L85-140].

### Cline (open source, `sdk/packages/core/src/extensions/tools/`)

- **`fetch_web_content({requests: [{url, prompt}]})`.** "Fetch content from URLs and analyze them
  using the provided prompts … Fetch independent URLs together in one call, and call this tool in the
  same response as other independent tool calls." The requests run in parallel (`Promise.all`), with
  a 30 s timeout and 2 retries [`definitions.ts` L549-600; `schemas.ts` L177-189].
- **The default executor has no model step.** It uses regex HTML→text, a 5 MB cap and a 50,000-char
  slice, then *appends* `--- Analysis Request --- Prompt: …` so the main model does the analysis
  [`executors/web-fetch.ts` L54-78, L98-242]. The `prompt` parameter still makes the model say why it
  is reading, even without a second model. Hosts can plug in their own executor.
- **Search:** delegated to the provider's native `web_search` (`maxUses`, `allowedDomains`,
  `blockedDomains`, `userLocation`) [`shared/src/llms/model-tools.ts` L1-21]. Cline also ships a
  browser tool (`browser_action` UI in `apps/vscode`), not covered here.

### Aider, Continue, Goose, Cursor (brief)

- **Aider:** only the user fetches. `/web <url>` means "Scrape a webpage, convert to markdown and send
  in a message" (Playwright if available, else httpx; BeautifulSoup slimdown + pypandoc). URLs in the
  user's message trigger "Add URL to the chat?" [`aider/commands.py` L219-247;
  `aider/scrape.py` L98-260; `coders/base_coder.py` L964-978]. There is no search.
- **Continue:**
  - `search_web(query)`: "Use this tool sparingly - only for questions that require specialized,
    external, and/or up-to-date knowledege. Common programming questions do not require web search."
    It returns 5 results, each truncated to 8000 chars, with a warning to refine the query
    [`core/tools/definitions/searchWeb.ts`; `implementations/searchWeb.ts` L5-37].
  - `fetch_url_content(url)`: raw content, 20,000 chars, with a warning to fetch "specific sections"
    [`implementations/fetchUrlContent.ts` L5-37].
- **Goose:** no built-in web tools in current source. The computercontroller instructions say "Use the
  shell … for accessing web sites or APIs", and web access otherwise comes from MCP extensions
  [`crates/goose-mcp/src/computercontroller/mod.rs` ~L347-356].
- **Cursor:** closed source. The docs list a web-search tool ("Generate search queries and perform web
  searches"), a browser tool, `@Browser` context, and "no limit on the number of tool calls"
  [cursor.com/docs/agent/tools; /docs/context/mentions]. Fetch internals are unknown.
- **MCP `fetch` reference server:** Readability + markdownify, `max_length` default 5000, and
  `start_index` pagination, with the notice "Content truncated. Call the fetch tool with a start_index
  of N". It respects robots.txt for autonomous fetches [`src/fetch/src/mcp_server_fetch/server.py`
  L30-45, L154-176, L240-255].

## 4. Implications for seek v5 default mode

### What the evidence says (sourced)

1. **The model picks pages from search results**, which are titles, URLs and short snippets. The
   pipeline never reads the top N (all tool-based agents above).
2. **Fetch takes a purpose.** It is either an explicit extraction prompt (Claude Code, Gemini, Cline)
   or the model's own follow-up reading of raw text (OpenCode, Continue).
3. **Budgets are enforced in code, not in prompts** (`max_uses`, the per-session cap, fixed character
   limits), and a spent budget returns a gentle "continue with what you have" (Claude Code).
4. **Fetch only URLs the conversation has already seen** (Anthropic URL validation).

### Proposed design (inference: mapped from the evidence, not tested)

**Loop: pre-search → decide → (read) → answer. At most 2 LLM calls in the common path.**

1. **Search in code (no LLM).** Run `opSearch(question)` and keep 8 unique results: id, title, host,
   URL, snippet, and date if known. This skips the "model writes a query" round trip that every tool
   agent pays (roughly 1-2 s saved). The trade-off is a worse query for chatty questions, which the
   model can repair with `search`.
2. **Call 1: the main model with tools, `tool_choice: auto`, parallel tool calls on.** It sees the
   question plus the numbered results and can take one of three paths:
   - **Answer directly** from snippets (weather, a version number, a date). One LLM call in total,
     which fixes the weather over-fetch.
   - **`read`** specific results:

     ```
     read(ids: [int] (1-3), focus: string)
     "Read search results by id and return the parts relevant to `focus`. Read only when the snippets
      don't already answer the question. Pick the fewest pages that cover it (usually 1, at most 3),
      preferring official docs and primary sources. Pick pages for the exact language, framework and
      SDK the question names; skip pages about a different ecosystem even if the words match."
     ```

     Using ids instead of free URLs copies Anthropic's URL validation: the model can't invent URLs,
     and injected text can't steer it to arbitrary hosts. Also allow `url` when that URL appears in
     the user's question.
   - **`search(query)`** at most once, when the results are off-topic. It returns more numbered
     results. The Gemini 3 description pattern applies: search when you don't have a URL, read when
     you do.
3. **Execute reads in parallel through `opFetch`**, reusing the fetch cache (Claude Code: 15 min, so
   reuse is expected). By default, return a **focused raw excerpt**, not model-written facts: seek's
   existing `cleanPage` + `focusPage` keyed on `focus`, about 6-8k chars per page. Prefix it with a
   header like `Source [3] <title> <url> (excerpt, N of M chars)`, and wrap it as untrusted page
   content (Gemini's `wrapUntrusted`). Why raw by default:
   - It removes a second model round trip, which is the latency target.
   - It keeps code and import lines verbatim. Paraphrase by an extract model is a plausible source of
     the invented package (inference).
   - Claude Code's docs themselves flag model extraction as lossy.
4. **Use the extract model only when a page is much larger than the excerpt** (for example, > 40k
   chars after cleaning). Prompt it with Claude Code/Gemini-style instructions: "Answer `focus` from
   this page. Quote code, package names, versions verbatim; state which language/SDK the page is
   for." Run it in parallel with the other reads. `--deep` keeps its current fact distillation.
5. **Call 2: answer.** Send `tool_choice: "none"`, or leave the tools out, once the budget is spent.
   The budget is **2 tool turns and 4 reads**. If the model asks for more, reply "budget reached;
   answer from what you have" (Claude Code's cap notice) instead of an error.
6. **Citations:** keep today's `[n]` ids = result ids. seek appends the source list (as it does now,
   and like Gemini's appended `Sources:`), so URLs are never model-generated.

### The two observed problems

- **Over-fetching (weather read 3 pages, used 1).** The root cause is sourced: seek reads in the
  pipeline, and no surveyed agent does that. The fix is to let the model answer from snippets or pick
  `ids`, with the description saying "fewest pages, usually 1". Expected effect (inference):
  snippet-answerable questions drop to 1 LLM call and 0 fetches.
- **Mixing ecosystems (a JavaScript SDK page combined with a Python framework's docs, plus an invented
  package).** The mitigations are partly sourced and partly inference:
  1. The model chooses pages after seeing titles and URLs, and the `read` description tells it to
     match the question's language, framework and SDK.
  2. The `focus` string carries the target stack, for example "Python, framework X, how to add
     provider Y".
  3. Excerpts are verbatim, so imports can be checked.
  4. Keep the rule from the current `shallowPrompt` and tighten it: "every import, package and
     function in code must appear in a read excerpt for that same ecosystem; otherwise use a
     placeholder and say so."
  5. Optionally, the extract-model path labels each page with "applies to: <language/SDK>".

  Nothing in the surveyed agents solves this directly. They rely on the main model's judgment plus
  primary-source preference. So treat this as a hypothesis to check against the failing query.

### Latency and token budget (estimates, inference)

| Path | LLM calls | Rough wall time |
|---|---|---|
| Snippets suffice | 1 | search ~1 s + LLM ~1.5-2.5 s ≈ **3 s** |
| Read 1-2 pages | 2 | + parallel fetch ~1-2 s (cached: ~0) + answer ~2-3 s ≈ **6-8 s** |
| Re-search, then read | 3 | ≈ 9-12 s; rare, and capped by the 2-turn budget |
| Huge page via extract model | 2 + parallel extract | + ~1.5-2 s |

- **Compared with the current default** (always 3 reads + 1 call): one extra round trip when reading,
  but fewer input tokens (1-2 focused excerpts instead of 14k + 8k + 6k) and a faster, cheaper
  snippet-only path.
- **Reasoning effort:** keep `reasoning_effort` low for call 1 (a routing decision). Consider medium
  for call 2 only (inference).
- **Risk:** some OpenAI-compatible endpoints handle parallel tool calls or `tool_choice` poorly. Keep
  the existing "tool errors return as text" behavior, and fall back to today's pipeline read if call 1
  returns an invalid tool call.

## 5. Sources

Commits read: gemini-cli `62364cb`, opencode (anomalyco/opencode, `dev`) `18ef3cc`, codex `ec4d27a`,
cline `9c0e4aa`, goose `201837d`, aider `5dc9490`, continue `5522c6f`, modelcontextprotocol/servers
`f46d957`.

- Claude Code tools reference, "WebFetch tool behavior", "WebSearch tool behavior", "Session search
  limit": https://code.claude.com/docs/en/tools-reference
- Claude Code WebSearch/WebFetch tool descriptions as delivered to the model (observed in this
  research session, 2026-09).
- Anthropic web fetch tool: https://platform.claude.com/docs/en/agents-and-tools/tool-use/web-fetch-tool
- Anthropic web search tool: https://platform.claude.com/docs/en/agents-and-tools/tool-use/web-search-tool
- Gemini CLI:
  - https://github.com/google-gemini/gemini-cli/blob/main/packages/core/src/tools/web-fetch.ts
  - https://github.com/google-gemini/gemini-cli/blob/main/packages/core/src/tools/web-search.ts
  - https://github.com/google-gemini/gemini-cli/blob/main/packages/core/src/tools/definitions/model-family-sets/gemini-3.ts
  - https://github.com/google-gemini/gemini-cli/blob/main/packages/core/src/tools/definitions/model-family-sets/default-legacy.ts
  - https://github.com/google-gemini/gemini-cli/blob/main/packages/core/src/config/defaultModelConfigs.ts
- Codex:
  - https://github.com/openai/codex/blob/main/codex-rs/core/src/tools/hosted_spec.rs
  - https://github.com/openai/codex/blob/main/codex-rs/protocol/src/models.rs
  - https://github.com/openai/codex/blob/main/codex-rs/protocol/src/config_types.rs
  - Config basics, "Web search mode": https://learn.chatgpt.com/docs/config-file/config-basic
- OpenCode (`webfetch.ts`, `webfetch.txt`, `websearch.ts`, `websearch.txt`, `mcp-websearch.ts`,
  `truncate.ts`): https://github.com/anomalyco/opencode/tree/dev/packages/opencode/src/tool
- Cline:
  - https://github.com/cline/cline/blob/main/sdk/packages/core/src/extensions/tools/definitions.ts
  - https://github.com/cline/cline/blob/main/sdk/packages/core/src/extensions/tools/schemas.ts
  - https://github.com/cline/cline/blob/main/sdk/packages/core/src/extensions/tools/executors/web-fetch.ts
  - https://github.com/cline/cline/blob/main/sdk/packages/shared/src/llms/model-tools.ts
- Aider:
  - https://github.com/Aider-AI/aider/blob/main/aider/commands.py
  - https://github.com/Aider-AI/aider/blob/main/aider/scrape.py
  - https://github.com/Aider-AI/aider/blob/main/aider/coders/base_coder.py
- Continue (`definitions/searchWeb.ts`, `definitions/fetchUrlContent.ts`, `implementations/*`):
  https://github.com/continuedev/continue/tree/main/core/tools
- Goose: https://github.com/block/goose/blob/main/crates/goose-mcp/src/computercontroller/mod.rs
- Cursor: https://cursor.com/docs/agent/tools, https://cursor.com/docs/context/mentions
- MCP fetch server: https://github.com/modelcontextprotocol/servers/blob/main/src/fetch/src/mcp_server_fetch/server.py
