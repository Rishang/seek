# Providers — implementation notes

How each provider is wired in `src/provider/`. For full upstream API request/response
shapes see [`.idea/providers.md`](../.idea/providers.md); this file is the seek-side view.

## Contents

- [Capability matrix](#capability-matrix)
- [The `auto` provider](#the-auto-provider-search-fetch-parse--transcribe)
- [Providers](#providers)
  - [firecrawl](#1-firecrawl) · [tavily](#2-tavily) · [spider.cloud](#3-spidercloud) · [webcrawlerapi](#4-webcrawlerapi) · [lightpanda](#5-lightpanda) · [brave](#6-brave) · [exa](#7-exa) · [perplexity](#8-perplexity) · [supadata](#9-supadata)
- [Auth](#auth)
- [Time range](#time-range)
- [Published date](#published-date)
- [Snippet (search) vs content (fetch)](#snippet-search-vs-content-fetch)
- [Adding a provider](#adding-a-provider)

## Capability matrix

| Provider        | Search | Fetch | Crawl |   Parse    | Transcribe | Time range | Published date | File              |
|-----------------|:------:|:-----:|:-----:|:----------:|:----------:|:----------:|:--------------:|-------------------|
| firecrawl       |   ✓    |   ✓   |   ✓   | PDF+Office |  ✓ (page)  |     ✓      |       —        | `firecrawl.go`    |
| tavily          |   ✓    |   ✓   |   ✓   |    PDF     |  ✓ (page)  |     ✓      |   news only    | `tavily.go`       |
| spider.cloud    |   ✓    |   ✓   |   ✓   |     —      |     —      |     ✓      |       —        | `spider.go`       |
| webcrawlerapi   |   —    |   ✓   |   ✓   |     —      |     —      |     —      |       —        | `webcrawlerapi.go`|
| lightpanda      |   —    |   ✓   |   —   |     —      |     —      |     —      |       —        | `lightpanda.go`   |
| brave           |   ✓    |   —   |   —   |     —      |     —      |     ✓      |   `page_age`   | `brave.go`        |
| exa             |   ✓    |   ✓   |   —   |     —      |     —      |     ✓      | `publishedDate`| `exa.go`          |
| perplexity      |   ✓    |   —   |   —   |     —      |     —      |     ✓      |     `date`     | `perplexity.go`   |
| supadata        |   —    |   ✓   |   ✓   |    PDF     |     ✓      |     —      |       —        | `supadata.go`     |

Capability = which of `SearchProvider` / `FetchProvider` / `CrawlProvider` / `ParseProvider` / `TranscribeProvider` the type implements
(`provider/provider.go`). Time range = also implements `TimeRangeSearcher` (`SupportsTimeRange()`).

## The `auto` provider (search, fetch, parse & transcribe)

`auto` is a meta-provider (`provider/auto.go`) that tries configured providers in
priority order and returns the first non-empty result, failing over on either an
error or an empty result. It is the default for `search`, `fetch`, `parse` and `transcribe`. Crawl does
not support it. `supadata` sits after the page scrapers in `DefaultPriority`, so in
the transcribe chain tavily/firecrawl answer first and a video with no captions still
"succeeds" with just the page — use `-p supadata` (or move it up in `priority:`) to
get its ASR fallback.

- **Membership** comes from provider.yaml (plus env overrides): every provider you
  have a key/host for, that supports the operation, is in the chain. There is no
  provider list in config.yaml — adding a key is the only step needed to include a
  provider. The factory only builds providers that are configured
  (`NewFactory` skips entries with no key and no host), so unconfigured names are
  filtered out of the chain automatically.
- **Order** is the built-in `defaultAutoChains` ranking (in `config_cmd.go`),
  optionally reordered by an additive per-op `priority:` hint in config.yaml. The
  hint only moves listed providers to the front; unlisted-but-configured providers
  still run, after them. `main.go`'s `autoCandidates` composes the final order as
  `priority` ++ `defaultAutoChains[op]` ++ `providerEnv` order, de-duplicated.
- On total failure `auto` returns an aggregated error naming every attempt.
- `SEEK_LOG=debug` shows which provider served (`auto: served by …`); failovers log
  at `warn`. The trail is exposed via the `AutoReporter` interface and logged from
  `main.go` (never from `provider/`).
- `seek config init` never writes `priority:`; when an op is `auto` it multi-selects
  which providers to set up keys for, and those keys (in provider.yaml) are the
  membership.

Example config.yaml priority hint:

    config:
      search:
        provider: auto
        priority: [brave, exa]

## Providers

### 1. firecrawl

**Docs:** https://docs.firecrawl.dev
**Auth:** `Authorization: Bearer <key>`
**Capabilities:** Search, Fetch, Crawl

**Notes:**
- Self-hostable via `host` override.
- `tbs` Google-style time filter.

---

### 2. tavily

**Docs:** https://docs.tavily.com
**Auth:** `Authorization: Bearer <key>`
**Capabilities:** Search, Fetch (extract), Crawl

**Notes:**
- `published_date` only for `topic=news`; empty for general search.
- `start_date` / `end_date` time filter.

---

### 3. spider.cloud

**Docs:** https://spider.cloud/docs/api
**Auth:** `Authorization: Bearer <key>`
**Capabilities:** Search, Fetch, Crawl

**Notes:**
- Search auto-fetchs results (returns content, not just links).
- `tbs` Google-style time filter.

---

### 4. webcrawlerapi

**Docs:** https://webcrawlerapi.com/docs
**Auth:** `Authorization: Bearer <key>`
**Capabilities:** Fetch, Crawl

---

### 5. lightpanda

**Docs:** https://lightpanda.io/docs/usage/api
**Auth:** `Authorization: Bearer <key>`
**Capabilities:** Fetch

**Notes:**
- Lightweight headless browser; self-hostable via `host` override.

---

### 6. brave

**Docs:** https://api-dashboard.search.brave.com/app/documentation/web-search
**Auth:** `X-Subscription-Token: <key>` (not Bearer)
**Capabilities:** Search

**Notes:**
- `page_age` → published date; `freshness` time filter.

---

### 7. exa

**Docs:** https://exa.ai/docs/reference/search
**Auth:** `Authorization: Bearer <key>`
**Capabilities:** Search, Fetch (`/contents`)

**Notes:**
- Neural search returns inline excerpts (search alone often yields usable content).
- `publishedDate` → published date; ISO 8601 time filter.

---

### 8. perplexity

**Docs:** https://docs.perplexity.ai
**Auth:** `Authorization: Bearer <key>`
**Capabilities:** Search (`/search`)

**Notes:**
- Search uses the dedicated Search API; `date` → published date.
- `search_after_date_filter` / `search_before_date_filter` time filter (`M/D/YYYY`, `mdy`).
- Search-only: Perplexity has no page-scraping endpoint. Extracting a page through the
  chat/completions model returned a paraphrased summary rather than the real page content,
  so fetch was dropped — leave fetch to providers that actually scrape.

### 9. supadata

**Docs:** https://docs.supadata.ai (transcript, web/scrape, web/crawl)

**Capabilities:** Fetch, Crawl, Transcribe

- **Fetch:** `GET /v1/web/scrape?url=` → `{url, content (markdown), name, urls[]}`. Markdown
  only; `--format` is ignored.
- **Crawl:** `POST /v1/web/crawl {url, limit: 100}` → `{jobId}`; poll
  `GET /v1/web/crawl/{jobId}` until `status: completed` (`failed|cancelled` → error), then
  follow the absolute `next` URL (`?skip=N`) until absent. Pages are recorded by URL.
- **Transcribe:** `GET /v1/transcript?url=&text=true[&lang=]`. Any video page URL
  (YouTube, TikTok, Instagram, X, Facebook) or direct media file; native captions first, AI
  fallback. Long videos return `202 {jobId}` → poll `GET /v1/transcript/{jobId}` until
  `status: completed|failed`.

All endpoints authenticate with an `x-api-key` header (not bearer).

## Parse (documents)

`Parse` is each provider's page scrape pointed at a document URL. Tested 2026-10-09:

- **firecrawl** — `/v2/scrape` auto-detects PDF (fast text, OCR fallback), docx/doc/odt/rtf, xlsx/xls
  (per docs; not live-tested, no key).
- **tavily** — `/extract` parses PDFs to plain text; docx/xlsx fail with `Failed to fetch url`.
- **supadata** — `/web/scrape` parses PDFs to markdown with tables; docx/xlsx come back as raw zip
  bytes, which `Fetch` rejects via `looksBinary` so `auto` fails over.
- **lightpanda** — returns empty content for a PDF; not a parse provider.
- exa / spider.cloud — undocumented for files; not wired.

Firecrawl's and tavily's `Transcribe` are just a markdown scrape/extract: YouTube pages include
the caption track when one exists (tavily as a timestamped `### Transcript` section), with no ASR
fallback, so prefer supadata.

## Auth

Shared `httpClient` (`client.go`) sends `Authorization: Bearer <key>`. Exceptions:

- **brave** — `X-Subscription-Token: <key>` header, query as GET param; bypasses the shared
  post/get helpers and builds the request directly.
- **exa** — accepts Bearer, so it uses the shared helper as-is.

## Time range

CLI `--start`/`--end` (DD/MM/YYYY) or `--range N` → `config.SearchOptions.TimeRange` →
each provider formats to its own param via helpers in `timerange.go`:

| Provider     | Param                          | Format (helper)                                      |
|--------------|--------------------------------|------------------------------------------------------|
| firecrawl    | `tbs`                          | `cdr:1,cd_min:MM/DD/YYYY,cd_max:...` (`googleTBS`)    |
| spider.cloud | `tbs`                          | same Google `tbs` (`googleTBS`)                       |
| tavily       | `start_date` / `end_date`      | `YYYY-MM-DD` (`ymd`)                                  |
| brave        | `freshness`                    | `YYYY-MM-DDtoYYYY-MM-DD` (`braveFreshness`)           |
| exa          | `startPublishedDate` / `endPublishedDate` | ISO 8601 (`iso8601`)                      |
| perplexity   | `search_after_date_filter` / `search_before_date_filter` | `M/D/YYYY` (`mdy`)         |

Open bounds drop out (`omitempty` / empty string). If a selected provider lacks
`TimeRangeSearcher`, `main.go` emits `logx.Warn` and runs the search without the filter.

## Published date

Maps to `SearchResult.PublishedDate` (JSON `published_date`, `omitempty`):

- **brave** — `page_age` (ISO 8601, when known).
- **exa** — `publishedDate` (ISO 8601, when known).
- **perplexity** — `date` (per search result, when known).
- **tavily** — `published_date`, only for `topic=news`; empty for general search.
- **firecrawl / spider** — none; field omitted.

So a docs/landing page with no date, or a general (non-news) tavily query, yields no
`published_date` — expected, not a bug. Use `-p brave` or `-p exa` on date-bearing pages.

## Snippet (search) vs content (fetch)

- Search returns short `Snippet`s, not full pages. exa/spider pull a small excerpt inline;
  brave/tavily/firecrawl return their description/content field.
- For full page text, `fetch` the URL. exa search excerpts are often enough to skip the fetch.

## Adding a provider

The provider **registry** (`provider/registry.go`) is the single source of truth:
it lists every provider once, with its env var and optional self-host default, and
*derives* capabilities by probing which interfaces the type implements. The CLI's
provider lists, key prompts, host defaults, and auto-chain ordering all read from
it — so a new provider shows up everywhere from one entry.

1. New `provider/<name>.go` embedding `*httpClient`; implement the capability methods.
2. End the file with compile-time checks: `var _ SearchProvider = (*X)(nil)` (one per capability).
3. Add `SupportsTimeRange() bool` if it honors a date window (add a date helper to `timerange.go`).
4. Add **one line** to `registry` in `registry.go`: name, env var, host default (`""` if cloud-only),
   and the constructor. That is the only wiring step — `factory.go` builds it, `main.go`'s
   `providerEnv`, `config_cmd.go`'s capability lists, and the auto chain all derive from it.
5. Optionally place it in `config.DefaultPriority` (`config/config.go`) to rank it for `auto`;
   any capable provider omitted from that list is still appended automatically.
6. Document the upstream API in `.idea/providers.md` and add a row to the matrix above.

The registry's capability matrix is guarded by `registry_test.go`, which fails if a
provider's derived caps ever drift from the interfaces it implements.