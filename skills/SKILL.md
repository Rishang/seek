---
name: web-fetch
description: Search the web and fetch full page content via seek (firecrawl/tavily/brave/spider). Use for research, fact-checking, documentation lookups, or any task requiring current web information.
---
# Web Fetch — `seek`
```bash
seek search -o csv "<query>"   # ranked results as CSV: title,url,snippet,published_date
seek fetch "<url>"             # one page → full markdown on stdout
# seek crawl  — multi-page, OFF by default (see guard below)
```
`seek search -o csv` returns rows of `title,url,snippet,published_date`. The **snippet is substantial** — often several sentences summarizing the page, not a teaser. For many lookups it already *is* the answer. `seek fetch` returns the full page as markdown (default format), including nav/footer boilerplate.

## The loop: search → decide → maybe fetch → stop
1. **Search.** `seek search -o csv "<query>"` → read the snippets + URLs.
2. **Decide against the objective.** Do the snippets already answer it (a fact, a version, a yes/no, the right link)?
   - **Yes → STOP. Don't fetch.** Answer from the snippets — the cheapest path, take it whenever it's enough.
   - **No → fetch the single most relevant URL** for the detail the snippets lack.
3. **Fetch.** `seek fetch "<url>"` → full markdown. Read it; ignore the nav/footer boilerplate.
4. **Stop the moment the objective is met.** Don't fetch a second URL "to be thorough." First page missed → fetch the next best *once*, then re-decide.

## Shortcuts that skip search entirely
- **Have the exact URL** → `seek fetch "https://<host>/<path>"`. Done.
- **Doc site, know the domain** → guess the conventional page first:
  `https://<host>/<topic>/overview` (Mintlify/Docusaurus, e.g. `https://docs.agno.com/workflows/overview`). Fetch it. Real content? Done — and the page's own nav usually exposes every sibling URL you'd want next.
- **Domain known, structure unknown** → fetch the index and grep in ONE piped command:
  `seek fetch "https://<host>/llms.txt" | rg "<keyword>" | head -20`
  Hit → fetch that URL. Miss → fall back to `seek search`.

## Hard limits (prevent token blowup)
- **Snippets first, fetch second.** Every fetch is a full page of tokens — never fetch when the search snippets already answer the objective.
- **One page at a time.** Read a fetch before getting the next; stop as soon as the objective is met. Never batch fetches; never run search + fetch in parallel for the same question.
- **Pipe the index, don't dump it.** Always `| rg ... | head -20` the llms.txt fetch — never let the raw index land in context. Run that grep ONCE; no clear URL → `seek search`.
- **Truncated/huge index = STOP.** If llms.txt says "omitted"/"truncated"/"pages omitted", skip the grep — go straight to `seek search`.
- **Empty fetch → one retry max** (fix a slash, drop a `.md`), then fall back to `seek search`. Never loop fetches.
- **No `seek crawl` unless explicitly asked.** Crawl pulls many pages and blows the budget — for "check the docs"/lookups, one search and at most one fetch is enough.
- Not for APIs or local files — use `curl`/`bash`/`read`.

## Flags worth knowing but not often used(rarely required)
- `seek search -o csv` — CSV instead of JSON (compact; columns `title,url,snippet,published_date`). Prefer it.
- `-p firecrawl|tavily|spider.cloud|brave` (search) / `-p firecrawl|tavily|spider.cloud|webcrawlerapi|lightpanda` (fetch) — pin a provider.
- `--no-cache` — bypass the result cache for one call.
