// Package agent answers questions from the web. Run is the fast, shallow
// mode (one search, answer from snippets); Research is deep research, where
// the model drives search and fetch as tools and fetched pages are distilled
// into deduped facts by a small extract model.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/rishang/seek/config"
)

// DefaultSources is how many ranked search results the shallow mode shows the
// model when Options.Sources is 0.
const DefaultSources = 8

// DefaultMaxSteps caps deep-research model turns when Options.MaxSteps is 0.
const DefaultMaxSteps = 12

// maxPageChars caps each page sent to the extract model.
// ponytail: blunt char cap; chunk pages instead if long sources lose facts.
const maxPageChars = 30000

// Deps are the operations the agent needs. Function fields keep the package
// independent of the CLI's runners and trivial to fake in tests.
type Deps struct {
	Search   func(ctx context.Context, query string) ([]config.SearchResult, error)
	Fetch    func(ctx context.Context, url string) (string, error)
	Complete func(ctx context.Context, req Request) (Message, error)
	Trace    func(format string, args ...any) // optional progress hook (the CLI maps it to logx.Debug)
}

func (d Deps) trace(format string, args ...any) {
	if d.Trace != nil {
		d.Trace(format, args...)
	}
}

// Options selects the models and limits for a run.
type Options struct {
	Model           string // synthesis / research model
	ExtractModel    string // per-source extraction model; Model when empty
	ReasoningEffort string // sent with Model's requests only; "" omits it
	Sources         int    // Run: ranked results shown to the model; DefaultSources when 0
	MaxSteps        int    // Research: model turns before a forced answer; DefaultMaxSteps when 0
}

func (o *Options) defaults() {
	if o.ExtractModel == "" {
		o.ExtractModel = o.Model
	}
	if o.Sources <= 0 {
		o.Sources = DefaultSources
	}
	if o.MaxSteps <= 0 {
		o.MaxSteps = DefaultMaxSteps
	}
}

// Source is one citable page, numbered from 1. Snippet is set in shallow mode.
type Source struct {
	ID      int    `json:"id"`
	Title   string `json:"title,omitempty"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

// Fact is a deduped claim with the sources that stated it.
type Fact struct {
	Claim     string `json:"claim"`
	Relevance int    `json:"relevance"`
	Sources   []int  `json:"sources"`
}

// Skip records a source dropped because its fetch or extraction failed.
type Skip struct {
	URL string `json:"url"`
	Err string `json:"error"`
}

// Result is the agent output. Facts and Skipped are filled by Research only.
type Result struct {
	Answer  string   `json:"answer"`
	Facts   []Fact   `json:"facts,omitempty"`
	Sources []Source `json:"sources,omitempty"`
	Skipped []Skip   `json:"skipped,omitempty"`
}

const extractPrompt = `You extract facts from one web page to help answer a question.
Return JSON: {"facts":[{"claim":"...","relevance":0-3}]}.
Each claim is one short, self-contained, factual statement taken from the page.
Skip navigation, ads, marketing language, SEO filler and boilerplate.
relevance: 3 = directly answers the question, 2 = useful context, 1 = tangential, 0 = unrelated.
Return {"facts":[]} if the page has nothing relevant.`

const planPrompt = `Decide whether a web research question combines distinct parts (several tools, libraries, APIs or subtopics) that one search would cover only partly.
If it does, return 2 or 3 focused keyword queries, one per part, each pairing that part with the question's goal (e.g. "<tool A> <tool B> integration", "<tool A> <task> example"), never a bare name on its own.
If it is about a single topic, return no queries.
Keep names exactly as written and don't repeat the full question. Return JSON: {"queries":[...]}.`

// multiPart reports whether question likely combines several topics, which a
// single search tends to cover only partly: it joins parts ("+", "via", "with",
// "and", "using") or has many terms.
func multiPart(question string) bool {
	q := " " + strings.ToLower(question) + " "
	for _, sep := range []string{"+", " via ", " with ", " and ", " using ", " vs "} {
		if strings.Contains(q, sep) {
			return true
		}
	}
	return len(queryTerms(question)) >= 5
}

// planQueries asks the extract model for 2-3 focused sub-queries.
func planQueries(ctx context.Context, d Deps, model, question string) ([]string, error) {
	msg, err := d.Complete(ctx, Request{
		Model:          model,
		Messages:       []Message{{Role: "system", Content: planPrompt}, {Role: "user", Content: question}},
		ResponseFormat: &ResponseFormat{Type: "json_object"},
	})
	if err != nil {
		return nil, err
	}
	var p struct {
		Queries []string `json:"queries"`
	}
	if err := json.Unmarshal([]byte(stripFence(msg.Content)), &p); err != nil {
		return nil, fmt.Errorf("invalid JSON from model: %w", err)
	}
	var out []string
	for _, q := range p.Queries {
		if q = strings.TrimSpace(q); q != "" && !strings.EqualFold(q, question) && len(out) < 3 {
			out = append(out, q)
		}
	}
	d.trace("agent: split into %q", out)
	return out, nil
}

// planTTL caps the query-split call; past it the run searches the question alone.
const planTTL = 5 * time.Second

// gather searches the question and, for multi-part questions, 2-3 focused
// sub-queries. The question's own search runs while the sub-queries are being
// planned, so splitting costs no extra wall time beyond the plan call. Hits are
// interleaved (1st of each list, then 2nd, ...) so every query's best results
// stay near the top before ranking. It fails only if every search fails.
func gather(ctx context.Context, d Deps, model, question string) ([]config.SearchResult, error) {
	var orig []config.SearchResult
	var origErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		orig, origErr = d.Search(ctx, question)
		d.trace("agent: search %q → %d hit(s)", question, len(orig))
	}()

	var sub []string
	if multiPart(question) {
		pctx, cancel := context.WithTimeout(ctx, planTTL)
		var err error
		if sub, err = planQueries(pctx, d, model, question); err != nil {
			d.trace("agent: query split failed, using the question only: %v", err)
		}
		cancel()
	}
	lists := make([][]config.SearchResult, len(sub)+1)
	errs := make([]error, len(sub)+1)
	var wg sync.WaitGroup
	for i, q := range sub {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lists[i+1], errs[i+1] = d.Search(ctx, q)
			d.trace("agent: search %q → %d hit(s)", q, len(lists[i+1]))
		}()
	}
	wg.Wait()
	<-done
	lists[0], errs[0] = orig, origErr

	failed := 0
	for i, err := range errs {
		if err != nil {
			failed++
			d.trace("agent: search %d failed: %v", i, err)
		}
	}
	if failed == len(errs) {
		return nil, origErr
	}
	var out []config.SearchResult
	for k := 0; ; k++ {
		added := false
		for _, l := range lists {
			if k < len(l) {
				out = append(out, l[k])
				added = true
			}
		}
		if !added {
			return out, nil
		}
	}
}

// heading starts a markdown section.
var heading = regexp.MustCompile(`^#{1,6}\s`)

// focusPage trims page to limit chars by keeping its most relevant sections
// rather than just its beginning: the page is split at markdown headings (or
// into ~1500-char paragraph groups when it has none), each section is scored by
// the rarity-weighted question terms it contains, and the intro plus the best
// sections are kept in their original order, with "[…]" marking gaps.
// ponytail: lexical scoring; swap for embeddings if relevant sections get dropped.
func focusPage(page string, weights map[string]float64, limit int) string {
	if len(page) <= limit {
		return page
	}
	secs := splitSections(page)
	if len(secs) < 2 {
		return page[:limit]
	}
	type scored struct {
		i     int
		score float64
	}
	ranked := make([]scored, len(secs))
	for i, sec := range secs {
		low := strings.ToLower(sec)
		sc := 0.0
		for t, w := range weights {
			if hasWord(low, t) {
				sc += w
			}
		}
		ranked[i] = scored{i, sc / math.Sqrt(float64(len(sec))/1000+1)} // mild length normalization
	}
	sort.SliceStable(ranked, func(a, b int) bool { return ranked[a].score > ranked[b].score })

	keep := make([]bool, len(secs))
	used := 0
	if len(secs[0]) <= limit/4 { // the intro usually names the page's subject
		keep[0], used = true, len(secs[0])
	}
	for _, r := range ranked {
		if keep[r.i] || r.score == 0 || used+len(secs[r.i]) > limit {
			continue
		}
		keep[r.i], used = true, used+len(secs[r.i])
	}
	var b strings.Builder
	gap := false
	for i, sec := range secs {
		if !keep[i] {
			gap = true
			continue
		}
		if gap && b.Len() > 0 {
			b.WriteString("\n[…]\n\n")
		}
		gap = false
		b.WriteString(sec)
	}
	if b.Len() == 0 {
		return page[:limit]
	}
	return b.String()
}

// splitSections splits page at markdown headings; with no headings it groups
// paragraphs into ~1500-char chunks.
func splitSections(page string) []string {
	var secs []string
	var cur strings.Builder
	flush := func() {
		if strings.TrimSpace(cur.String()) != "" {
			secs = append(secs, cur.String())
		}
		cur.Reset()
	}
	lines := strings.SplitAfter(page, "\n")
	hasHeadings := false
	for _, l := range lines {
		if heading.MatchString(l) {
			hasHeadings = true
			break
		}
	}
	for _, l := range lines {
		if hasHeadings && heading.MatchString(l) {
			flush()
		} else if !hasHeadings && strings.TrimSpace(l) == "" && cur.Len() > 1500 {
			flush()
		}
		cur.WriteString(l)
	}
	flush()
	return secs
}

// navLine matches markdown lines that are only links, images or list bullets
// of links: menus, breadcrumbs and footers that waste the page budget.
var navLine = regexp.MustCompile(`^\s*(?:[-*+|]\s*)*(?:!?\[[^\]]*\]\([^)]*\)[\s|·•,/>-]*)+$`)

// cleanPage drops navigation-only lines and collapses blank runs so the page
// budget is spent on content.
func cleanPage(page string) string {
	var b strings.Builder
	blank := false
	for _, line := range strings.Split(page, "\n") {
		t := strings.TrimSpace(line)
		if navLine.MatchString(t) {
			continue
		}
		if t == "" {
			if !blank && b.Len() > 0 {
				b.WriteString("\n")
			}
			blank = true
			continue
		}
		blank = false
		b.WriteString(strings.TrimRight(line, " \t"))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// lowValueHosts are video and social sites whose pages fetch providers rarely
// turn into readable text, whatever the topic; the shallow mode never spends a
// read on them and ranks them last.
var lowValueHosts = []string{
	"youtube.com", "youtu.be", "linkedin.com", "facebook.com", "instagram.com",
	"tiktok.com", "x.com", "twitter.com", "pinterest.com",
}

// hostOf returns the lowercased hostname without "www.", or "" if unparsable.
func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}

func lowValueHost(rawURL string) bool {
	host := hostOf(rawURL)
	if host == "" {
		return true
	}
	for _, h := range lowValueHosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}

// stopwords are dropped from the question before scoring results.
var stopwords = map[string]bool{
	"a": true, "an": true, "the": true, "and": true, "or": true, "of": true, "in": true, "on": true,
	"to": true, "for": true, "with": true, "is": true, "are": true, "what": true, "whats": true,
	"how": true, "does": true, "do": true, "new": true, "about": true, "vs": true,
}

// queryTerms lowercases the question into scoring terms (keeping dotted
// versions like "1.25" intact).
func queryTerms(q string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(q), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '.'
	}) {
		w = strings.Trim(w, ".")
		if w != "" && !stopwords[w] {
			out = append(out, w)
		}
	}
	return out
}

// rankHits orders search hits by relevance to the question, keeping the
// provider's order on ties. Each question term counts once per field (title and
// URL weigh 2, snippet 1) and is weighted by rarity across the hits, so specific
// terms that few results mention outweigh generic ones most results repeat. A
// host that itself contains a question term is likely the first-party site for
// the thing asked about and gets a boost; docs/developer subdomains get a small
// one. Video and social hosts are ranked last.
// ponytail: lexical heuristic; swap for a reranker model if it misranks.
func rankHits(question string, hits []config.SearchResult) []config.SearchResult {
	terms := queryTerms(question)
	weights := termWeights(terms, hits)
	scores := make([]float64, len(hits))
	for i, h := range hits {
		f := struct{ title, url, snip, host string }{strings.ToLower(h.Title), strings.ToLower(h.URL), strings.ToLower(h.Snippet), hostOf(h.URL)}
		var sc float64
		ownHost := false
		for _, t := range terms {
			w, ok := weights[t]
			if !ok {
				continue
			}
			if hasWord(f.title, t) {
				sc += 2 * w
			}
			if hasWord(f.url, t) {
				sc += 2 * w
			}
			if hasWord(f.snip, t) {
				sc += w
			}
			if hasWord(f.host, t) {
				ownHost = true
			}
		}
		if ownHost {
			sc += 3
		}
		if strings.HasPrefix(f.host, "docs.") || strings.HasPrefix(f.host, "developer.") || strings.HasPrefix(f.host, "developers.") {
			sc++
		}
		if lowValueHost(hits[i].URL) {
			sc -= 100
		}
		scores[i] = sc
	}
	idx := make([]int, len(hits))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return scores[idx[a]] > scores[idx[b]] })
	out := make([]config.SearchResult, len(hits))
	for k, i := range idx {
		out[k] = hits[i]
	}
	return out
}

// termWeights gives each question term a rarity weight across the hits
// (log(1 + hits/df)): terms few results mention outweigh ones most repeat.
// Terms no hit mentions are left out.
func termWeights(terms []string, hits []config.SearchResult) map[string]float64 {
	w := make(map[string]float64, len(terms))
	for _, t := range terms {
		df := 0
		for _, h := range hits {
			if hasWord(strings.ToLower(h.Title+" "+h.URL+" "+h.Snippet), t) {
				df++
			}
		}
		if df > 0 {
			w[t] = math.Log(1 + float64(len(hits))/float64(df))
		}
	}
	return w
}

// hasWord reports whether term occurs in s bounded by non-alphanumerics, so
// "go" matches "go.dev" and "Go 1.25" but not "google". A term starting with a
// digit may follow letters, so "1.25" still matches "go1.25".
func hasWord(s, term string) bool {
	isAlnum := func(b byte) bool { return b >= 'a' && b <= 'z' || b >= '0' && b <= '9' }
	digitLed := term[0] >= '0' && term[0] <= '9'
	for i := 0; ; {
		j := strings.Index(s[i:], term)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(term)
		before := start == 0 || !isAlnum(s[start-1]) || (digitLed && s[start-1] >= 'a' && s[start-1] <= 'z')
		after := end == len(s) || !isAlnum(s[end])
		if before && after {
			return true
		}
		i = start + 1
	}
}

// pickSources keeps the first n results with a unique, non-empty URL.
func pickSources(hits []config.SearchResult, n int) []Source {
	var out []Source
	seen := map[string]bool{}
	for _, h := range hits {
		if h.URL == "" || seen[h.URL] {
			continue
		}
		seen[h.URL] = true
		out = append(out, Source{ID: len(out) + 1, Title: h.Title, URL: h.URL, Snippet: h.Snippet})
		if len(out) == n {
			break
		}
	}
	return out
}

type extracted struct {
	Claim     string `json:"claim"`
	Relevance int    `json:"relevance"`
}

// extract fetches url and asks the extract model for the facts relevant to
// question. The extract model never gets a reasoning effort: small models
// often reject the parameter, and extraction doesn't need it.
func extract(ctx context.Context, d Deps, model, question, url string) ([]extracted, error) {
	page, err := d.Fetch(ctx, url)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(page) == "" {
		return nil, errors.New("empty page")
	}
	d.trace("agent: fetched %s (%d chars)", url, len(page))
	if len(page) > maxPageChars {
		d.trace("agent: truncating %s to %d chars", url, maxPageChars)
		page = page[:maxPageChars]
	}
	msg, err := d.Complete(ctx, Request{
		Model: model,
		Messages: []Message{
			{Role: "system", Content: extractPrompt},
			{Role: "user", Content: "Question: " + question + "\n\nPage (" + url + "):\n" + page},
		},
		ResponseFormat: &ResponseFormat{Type: "json_object"},
	})
	if err != nil {
		return nil, err
	}
	out := msg.Content
	var parsed struct {
		Facts []extracted `json:"facts"`
	}
	if err := json.Unmarshal([]byte(stripFence(out)), &parsed); err != nil {
		return nil, fmt.Errorf("extract: invalid JSON from model: %w (reply starts %q)", err, out[:min(len(out), 200)])
	}
	d.trace("agent: extracted %d fact(s) from %s", len(parsed.Facts), url)
	return parsed.Facts, nil
}

// stripFence removes a ```json ... ``` wrapper some models add despite JSON mode.
func stripFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimPrefix(s, "json")
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "```"))
}

// merge drops unrelated facts, dedupes claims that normalize identically
// (collecting every source that stated them), and ranks by relevance, then by
// how many sources agree.
// ponytail: exact match after normalization; the synthesis prompt merges
// near-duplicates. Add embedding/small-model clustering if that falls short.
func merge(sources []Source, perSource [][]extracted) []Fact {
	var facts []Fact
	index := map[string]int{}
	for i, list := range perSource {
		id := sources[i].ID
		for _, e := range list {
			claim := strings.TrimSpace(e.Claim)
			if e.Relevance <= 0 || claim == "" {
				continue
			}
			key := normalize(claim)
			j, ok := index[key]
			if !ok {
				index[key] = len(facts)
				facts = append(facts, Fact{Claim: claim, Relevance: e.Relevance, Sources: []int{id}})
				continue
			}
			f := &facts[j]
			f.Relevance = max(f.Relevance, e.Relevance)
			if f.Sources[len(f.Sources)-1] != id {
				f.Sources = append(f.Sources, id)
			}
		}
	}
	sort.SliceStable(facts, func(a, b int) bool {
		if facts[a].Relevance != facts[b].Relevance {
			return facts[a].Relevance > facts[b].Relevance
		}
		return len(facts[a].Sources) > len(facts[b].Sources)
	})
	return facts
}

// normalize lowercases s, drops punctuation and collapses whitespace so
// trivially different phrasings of the same claim share a key.
func normalize(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// fullwidthCite matches citations some models emit as 【n】 instead of [n].
var fullwidthCite = regexp.MustCompile(`【(\d+)(?:†[^】]*)?】`)

// cleanAnswer trims the model's answer and normalizes 【n】 citations to [n],
// so they match the ids in the sources footer.
func cleanAnswer(s string) string {
	return strings.TrimSpace(fullwidthCite.ReplaceAllString(s, "[$1]"))
}

// Markdown renders the answer followed by a "---" separator and the sources
// the answer cites as "[n] [title](url)". When the answer cites no ids, every
// source is listed.
func (r *Result) Markdown() string {
	var b strings.Builder
	b.WriteString(r.Answer)
	b.WriteString("\n\n---\nsources:\n")
	for _, s := range citedSources(r.Answer, r.Sources) {
		title := s.Title
		if title == "" {
			title = s.URL
		}
		fmt.Fprintf(&b, "[%d] [%s](%s)\n", s.ID, title, s.URL)
	}
	return b.String()
}

// citeRef matches an inline [n] citation.
var citeRef = regexp.MustCompile(`\[(\d+)\]`)

// citedSources keeps the sources whose ids appear as [n] in answer, in id
// order; with no citations it returns all sources.
func citedSources(answer string, sources []Source) []Source {
	cited := map[string]bool{}
	for _, m := range citeRef.FindAllStringSubmatch(answer, -1) {
		cited[m[1]] = true
	}
	var out []Source
	for _, s := range sources {
		if cited[fmt.Sprint(s.ID)] {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return sources
	}
	return out
}
