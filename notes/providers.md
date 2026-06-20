# Providers — implementation notes

How each provider is wired in `src/provider/`. For full upstream API request/response
shapes see [`.idea/providers.md`](../.idea/providers.md); this file is the seek-side view.

## Contents

- [Capability matrix](#capability-matrix)
- [Providers](#providers)
  - [firecrawl](#1-firecrawl) · [tavily](#2-tavily) · [spider.cloud](#3-spidercloud) · [webcrawlerapi](#4-webcrawlerapi) · [lightpanda](#5-lightpanda) · [brave](#6-brave) · [exa](#7-exa)
- [Auth](#auth)
- [Time range](#time-range)
- [Published date](#published-date)
- [Snippet (search) vs content (scrape)](#snippet-search-vs-content-scrape)
- [Adding a provider](#adding-a-provider)

## Capability matrix

| Provider        | Search | Scrape | Crawl | Time range | Published date | File              |
|-----------------|:------:|:------:|:-----:|:----------:|:--------------:|-------------------|
| firecrawl       |   ✓    |   ✓    |   ✓   |     ✓      |       —        | `firecrawl.go`    |
| tavily          |   ✓    |   ✓    |   ✓   |     ✓      |   news only    | `tavily.go`       |
| spider.cloud    |   ✓    |   ✓    |   ✓   |     ✓      |       —        | `spider.go`       |
| webcrawlerapi   |   —    |   ✓    |   ✓   |     —      |       —        | `webcrawlerapi.go`|
| lightpanda      |   —    |   ✓    |   —   |     —      |       —        | `lightpanda.go`   |
| brave           |   ✓    |   —    |   —   |     ✓      |   `page_age`   | `brave.go`        |
| exa             |   ✓    |   ✓    |   —   |     ✓      | `publishedDate`|  `exa.go`         |

Capability = which of `SearchProvider` / `ScrapeProvider` / `CrawlProvider` the type implements
(`provider/provider.go`). Time range = also implements `TimeRangeSearcher` (`SupportsTimeRange()`).

## Providers

### 1. firecrawl

**Docs:** https://docs.firecrawl.dev
**Auth:** `Authorization: Bearer <key>`
**Capabilities:** Search, Scrape, Crawl

**Notes:**
- Self-hostable via `host` override.
- `tbs` Google-style time filter.

---

### 2. tavily

**Docs:** https://docs.tavily.com
**Auth:** `Authorization: Bearer <key>`
**Capabilities:** Search, Scrape (extract), Crawl

**Notes:**
- `published_date` only for `topic=news`; empty for general search.
- `start_date` / `end_date` time filter.

---

### 3. spider.cloud

**Docs:** https://spider.cloud/docs/api
**Auth:** `Authorization: Bearer <key>`
**Capabilities:** Search, Scrape, Crawl

**Notes:**
- Search auto-scrapes results (returns content, not just links).
- `tbs` Google-style time filter.

---

### 4. webcrawlerapi

**Docs:** https://webcrawlerapi.com/docs
**Auth:** `Authorization: Bearer <key>`
**Capabilities:** Scrape, Crawl

---

### 5. lightpanda

**Docs:** https://lightpanda.io/docs/usage/api
**Auth:** `Authorization: Bearer <key>`
**Capabilities:** Scrape

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
**Capabilities:** Search, Scrape (`/contents`)

**Notes:**
- Neural search returns inline excerpts (search alone often yields usable content).
- `publishedDate` → published date; ISO 8601 time filter.

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

Open bounds drop out (`omitempty` / empty string). If a selected provider lacks
`TimeRangeSearcher`, `main.go` emits `logx.Warn` and runs the search without the filter.

## Published date

Maps to `SearchResult.PublishedDate` (JSON `published_date`, `omitempty`):

- **brave** — `page_age` (ISO 8601, when known).
- **exa** — `publishedDate` (ISO 8601, when known).
- **tavily** — `published_date`, only for `topic=news`; empty for general search.
- **firecrawl / spider** — none; field omitted.

So a docs/landing page with no date, or a general (non-news) tavily query, yields no
`published_date` — expected, not a bug. Use `-p brave` or `-p exa` on date-bearing pages.

## Snippet (search) vs content (scrape)

- Search returns short `Snippet`s, not full pages. exa/spider pull a small excerpt inline;
  brave/tavily/firecrawl return their description/content field.
- For full page text, `scrape` the URL. exa search excerpts are often enough to skip the scrape.

## Adding a provider

1. New `provider/<name>.go` embedding `*httpClient`; implement the capability methods.
2. End the file with compile-time checks: `var _ SearchProvider = (*X)(nil)` (one per capability).
3. Add `SupportsTimeRange() bool` if it honors a date window.
4. Register the `case` in `factory.go`; add the env var to `providerEnv` in `main.go`.
5. Add it to the provider lists in `config_cmd.go` and the usage strings in `main.go`.
6. Document the upstream API in `.idea/providers.md` and add a row here.
