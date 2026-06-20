---
name: web-fetch
description: Search the web and fetch full page content via seek (firecrawl/tavily/brave/spider). Use for research, fact-checking, documentation lookups, or any task requiring current web information.
---
# Web Fetch — `seek`
```bash
seek scrape "<url>"            # one page → markdown
seek search -o csv "<query>"   # search → CSV results (content + summary)
# NOT used by default: seek crawl  (multi-page — see guard below)
```
Output is JSON on stdout. `seek search` results carry per-result `content` (often the full page in markdown, provider-dependent) and a summary/`answer` if the provider returns one.

## Flow (stop at first that works)
- **Have the exact URL** → `seek scrape "<url>" -f markdown`. Done.
- **Doc site, know the domain** → guess the conventional page first:
  `<domain>/<topic>/overview` (Mintlify/Docusaurus, e.g. `docs.agno.com/workflows/overview`). Scrape it. Real content? Done.
- **Guess missed, or structure unknown** → scrape the index and grep in ONE piped command:
  `seek scrape "<domain>/llms.txt" -f markdown | rg "<keyword>" | head -20`
  Hit → scrape that URL. Done.
- **No domain, or index unusable** → `seek search "<query>"`, then apply the search→scrape rule below.

## Search already gives you content — scrape only to fill a gap
Read each result's `content` and the summary FIRST.
- Results already answer the objective → **STOP. Do not scrape.** Use the `content` you have.
- Results are snippets/truncated, or miss a needed detail → scrape the single most relevant URL to fill *that specific gap*. One page, then re-check.
Scraping a URL whose content the search already returned in full is wasted tokens. (For content-heavy searches, `firecrawl` tends to return the fullest `content`.)

## Hard limits (prevent token blowup)
- **Pipe the index, don't dump it.** Always `| rg ... | head -20` the llms.txt scrape — never let the raw index land in context. Run that grep ONCE; if it doesn't surface a clear URL, fall back to `seek search`.
- **Truncated/huge index = STOP.** If llms.txt says "omitted"/"truncated"/"pages omitted", skip the grep — go straight to `seek search`.
- **Empty scrape → one retry max** (fix the slash, drop a `.md`), then fall back to `seek search`. Never loop scrapes.
- **One page at a time.** Read it before fetching the next; stop as soon as the objective is met. Never batch scrapes; never run search + scrape together.
- **No `seek crawl` unless explicitly asked.** Crawl pulls many pages and blows the budget — for "check the docs"/lookup tasks, one scrape or one search is enough.
- **Ignore boilerplate** (nav/footer/social); don't re-print content you've already shown.
- Not for APIs or local files — use `curl`/`bash`/`read`.
