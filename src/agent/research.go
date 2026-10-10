package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rishang/seek/config"
)

// Deep research is a fixed pipeline driven by code, not a tool-calling loop:
// the models plan, extract and write, but never decide when to search or
// stop. Every search and read is justified (a gap round runs only for
// checklist parts no fact covers yet), and the answer model sees only facts
// verified against their pages.
//
//	plan    extract model → checklist of parts, each with a search query
//	search  the question and every part's query, in parallel
//	read    best pages per part → extract model → facts with a verbatim quote
//	verify  drop facts whose quote or numbers aren't on the page
//	cover   a part is covered once a kept fact answers it; uncovered parts get
//	        a gap round (search the part's need, read its next best pages)
//	answer  the model writes once, without tools, from the merged facts

const deepPlanPrompt = `Break a web research question into the parts a complete answer must cover: 1 part for a narrow question, up to 5 for a broad or multi-part one.
For each part give "need", what must be found, as a short question, and "query", a focused keyword web search for it. Keep names and versions exactly as written.
Return JSON: {"parts":[{"need":"...","query":"..."}]}.`

const synthPrompt = `You answer a research question using only the facts given. Each fact ends with the ids of the sources that state it, e.g. [2][5].
- Lead with a direct answer, then cover every checklist part that has facts. Merge facts that say the same thing; never repeat one.
- Be concrete: keep names, versions, dates and numbers exactly as the facts state them. Skip filler. Use short headings or bullets.
- Cite after each statement with the ids of the facts you used, e.g. [2] or [1][3].
- Use nothing beyond the facts: no background knowledge and no guessed versions, dates, names or code. For each checklist part marked "not found", say in one line that the sources did not cover it.
Do not add a sources list.`

const (
	deepMaxParts   = 5                // checklist parts kept from the plan
	deepFirstReads = 6                // pages read in the first round
	deepGapReads   = 2                // pages read per uncovered part in a gap round
	deepHostReads  = 2                // pages read per host per run
	deepMaxFacts   = 60               // facts shown to the answer model
	deepPlanTTL    = 15 * time.Second // past it the question is its own single part
	deepFetchTTL   = 20 * time.Second // per-page fetch timeout; a slow page is skipped
)

// part is one checklist item: what a complete answer must cover and the
// search query for it.
type part struct {
	Need  string `json:"need"`
	Query string `json:"query"`

	hits       []config.SearchResult // ranked; unread ones feed gap rounds
	researched bool                  // its need was searched in a gap round
	covered    bool
}

// research is one run's state. Rounds read pages in parallel, but all state
// changes happen between rounds, so it needs no lock.
type research struct {
	d        Deps
	opts     Options
	question string
	parts    []*part
	qHits    []config.SearchResult // the question's own search, ranked
	weights  map[string]float64    // term weights for page excerpts

	sources   []Source // pages that yielded facts, numbered from 1
	perSource [][]extracted
	read      map[string]bool // URLs picked so far
	hosts     map[string]int  // pages picked per host
	skipped   []Skip
}

// Research answers question with the deep pipeline above. It stops reading
// when every part is covered, after MaxRounds rounds, or when a gap round
// covers nothing new, then makes one tool-free call to the model.
func Research(ctx context.Context, d Deps, opts Options, question string) (*Result, error) {
	opts.defaults()
	r := &research{d: d, opts: opts, question: question, read: map[string]bool{}, hosts: map[string]int{}}
	if err := r.plan(ctx); err != nil {
		return nil, fmt.Errorf("agent search: %w", err)
	}
	for round := 1; round <= opts.MaxRounds; round++ {
		gaps := r.cover()
		if len(gaps) == 0 {
			break
		}
		var picks []Source
		if round == 1 {
			picks = r.pickFirst()
		} else {
			picks = r.pickGaps(ctx, gaps)
		}
		if len(picks) == 0 {
			d.trace("agent: round %d: no unread pages left", round)
			break
		}
		r.readPages(ctx, picks)
		left := r.cover()
		d.trace("agent: round %d: read %d page(s), %d/%d part(s) covered", round, len(picks), len(r.parts)-len(left), len(r.parts))
		if round > 1 && len(left) == len(gaps) {
			d.trace("agent: round %d covered nothing new, stopping", round)
			break
		}
	}

	facts := merge(r.sources, r.perSource)
	facts = facts[:min(len(facts), deepMaxFacts)]
	res := &Result{Facts: facts, Sources: r.sources, Skipped: r.skipped}
	if len(facts) == 0 {
		return res, errors.New("agent: no relevant facts found in the pages read")
	}
	d.trace("agent: answering from %d fact(s) across %d source(s)", len(facts), len(r.sources))
	msg, err := d.Complete(ctx, Request{
		Model:           opts.Model,
		ReasoningEffort: opts.ReasoningEffort,
		Messages:        []Message{{Role: "system", Content: synthPrompt + "\n" + today()}, {Role: "user", Content: r.brief(facts)}},
	})
	if err != nil {
		return res, fmt.Errorf("agent answer: %w", err)
	}
	res.Answer = knownCites(cleanAnswer(msg.Content), len(r.sources))
	if res.Answer == "" {
		return res, errors.New("agent: model returned an empty answer")
	}
	return res, nil
}

// plan asks the extract model for the checklist while the question itself is
// searched, then searches every part's query in parallel. A failed or empty
// plan makes the question its own single part. It fails only if every search
// fails.
func (r *research) plan(ctx context.Context) error {
	var qErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.qHits, qErr = r.d.Search(ctx, r.question)
		r.d.trace("agent: search %q → %d hit(s)", r.question, len(r.qHits))
	}()

	pctx, cancel := context.WithTimeout(ctx, deepPlanTTL)
	parts, err := planParts(pctx, r.d, r.opts.ExtractModel, r.question)
	cancel()
	if err != nil {
		r.d.trace("agent: plan failed, researching the question as one part: %v", err)
	}
	if len(parts) == 0 {
		parts = []*part{{Need: r.question, Query: r.question}}
	}
	r.parts = parts

	errs := make([]error, len(parts))
	var wg sync.WaitGroup
	for i, p := range parts {
		if strings.EqualFold(p.Query, r.question) {
			continue // the question's own search covers it
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.hits, errs[i] = r.d.Search(ctx, p.Query)
			r.d.trace("agent: search %q → %d hit(s)", p.Query, len(p.hits))
		}()
	}
	wg.Wait()
	<-done

	all := append([]config.SearchResult(nil), r.qHits...)
	ok := qErr == nil
	for i, p := range parts {
		switch {
		case strings.EqualFold(p.Query, r.question):
			p.hits = r.qHits
		case errs[i] != nil:
			r.d.trace("agent: search %q failed: %v", p.Query, errs[i])
		default:
			ok = true
			all = append(all, p.hits...)
		}
		p.hits = rankHits(p.Query, p.hits)
	}
	if !ok {
		return qErr
	}
	r.qHits = rankHits(r.question, r.qHits)
	var needs []string
	for _, p := range parts {
		needs = append(needs, p.Need, p.Query)
	}
	r.weights = focusWeights(termWeights(queryTerms(r.question), all), strings.Join(needs, " "))
	return nil
}

// planParts asks the extract model for the checklist parts.
func planParts(ctx context.Context, d Deps, model, question string) ([]*part, error) {
	msg, err := d.Complete(ctx, Request{
		Model:          model,
		Messages:       []Message{{Role: "system", Content: deepPlanPrompt}, {Role: "user", Content: question}},
		ResponseFormat: &ResponseFormat{Type: "json_object"},
	})
	if err != nil {
		return nil, err
	}
	var p struct {
		Parts []*part `json:"parts"`
	}
	if err := json.Unmarshal([]byte(stripFence(msg.Content)), &p); err != nil {
		return nil, fmt.Errorf("invalid JSON from model: %w", err)
	}
	var out []*part
	for _, x := range p.Parts {
		if x == nil {
			continue
		}
		x.Need, x.Query = strings.TrimSpace(x.Need), strings.TrimSpace(x.Query)
		if x.Query == "" {
			x.Query = x.Need
		}
		if x.Need != "" && len(out) < deepMaxParts {
			out = append(out, x)
			d.trace("agent: part %d: %s (search %q)", len(out), x.Need, x.Query)
		}
	}
	return out, nil
}

// pickFirst takes the best unread pages round-robin across the question's and
// every part's results, so each part gets a page before any gets a second.
func (r *research) pickFirst() []Source {
	lists := [][]config.SearchResult{r.qHits}
	for _, p := range r.parts {
		lists = append(lists, p.hits)
	}
	var picks []Source
	for k := 0; len(picks) < deepFirstReads; k++ {
		more := false
		for _, l := range lists {
			if k < len(l) && len(picks) < deepFirstReads {
				more = true
				picks = r.take(picks, l[k])
			}
		}
		if !more {
			break
		}
	}
	return picks
}

// pickGaps searches each uncovered part's need once (its keyword query already
// ran) and takes the part's next best unread pages.
func (r *research) pickGaps(ctx context.Context, gaps []*part) []Source {
	var wg sync.WaitGroup
	for _, p := range gaps {
		if p.researched {
			continue
		}
		p.researched = true
		wg.Add(1)
		go func() {
			defer wg.Done()
			hits, err := r.d.Search(ctx, p.Need)
			if err != nil {
				r.d.trace("agent: search %q failed: %v", p.Need, err)
				return
			}
			r.d.trace("agent: gap search %q → %d hit(s)", p.Need, len(hits))
			p.hits = rankHits(p.Need, append(hits, p.hits...))
		}()
	}
	wg.Wait()

	var picks []Source
	for _, p := range gaps {
		taken := 0
		for _, h := range p.hits {
			if taken == deepGapReads {
				break
			}
			next := r.take(picks, h)
			if len(next) > len(picks) {
				taken++
			}
			picks = next
		}
	}
	return picks
}

// take appends h as a page to read unless it was picked already, has no URL,
// is a video or social page, or its host has used its read budget.
func (r *research) take(picks []Source, h config.SearchResult) []Source {
	host := hostOf(h.URL)
	if h.URL == "" || r.read[h.URL] || lowValueHost(h.URL) || r.hosts[host] >= deepHostReads {
		return picks
	}
	r.read[h.URL] = true
	r.hosts[host]++
	return append(picks, Source{Title: h.Title, URL: h.URL})
}

// readPages extracts facts from picks in parallel and numbers each page that
// yielded facts as a source. Failed pages are recorded in skipped.
func (r *research) readPages(ctx context.Context, picks []Source) {
	facts := make([][]extracted, len(picks))
	errs := make([]error, len(picks))
	var wg sync.WaitGroup
	for i, s := range picks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			facts[i], errs[i] = extract(ctx, r.d, r.opts.ExtractModel, r.question, r.parts, r.weights, s.URL)
		}()
	}
	wg.Wait()
	for i, s := range picks {
		switch {
		case errs[i] != nil:
			r.d.trace("agent: skipped %s: %v", s.URL, errs[i])
			r.skipped = append(r.skipped, Skip{URL: s.URL, Err: errs[i].Error()})
		case len(facts[i]) > 0:
			s.ID = len(r.sources) + 1
			r.sources = append(r.sources, s)
			r.perSource = append(r.perSource, facts[i])
		}
	}
}

// cover marks the parts some kept fact answers and returns the rest.
func (r *research) cover() []*part {
	for _, facts := range r.perSource {
		for _, f := range facts {
			for _, i := range f.Parts {
				if i >= 1 && i <= len(r.parts) {
					r.parts[i-1].covered = true
				}
			}
		}
	}
	var gaps []*part
	for _, p := range r.parts {
		if !p.covered {
			gaps = append(gaps, p)
		}
	}
	return gaps
}

// brief is the answer model's input: the question, the checklist with what
// was found, and the merged facts with their source ids.
func (r *research) brief(facts []Fact) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Question: %s\n\nChecklist:\n", r.question)
	for i, p := range r.parts {
		status := "covered"
		if !p.covered {
			status = "not found"
		}
		fmt.Fprintf(&b, "%d. %s (%s)\n", i+1, p.Need, status)
	}
	b.WriteString("\nFacts:\n")
	for _, f := range facts {
		b.WriteString("- " + f.Claim + " ")
		for _, id := range f.Sources {
			fmt.Fprintf(&b, "[%d]", id)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// knownCites removes [n] citations above the last source id, so every
// citation left points at a page that was read. A bracket right after an
// identifier (args[7]) is code, not a citation, and is kept.
func knownCites(answer string, sources int) string {
	var b strings.Builder
	last := 0
	for _, m := range citeRef.FindAllStringSubmatchIndex(answer, -1) {
		id, _ := strconv.Atoi(answer[m[2]:m[3]])
		if id <= sources || m[0] > 0 && isIdent(answer[m[0]-1]) {
			continue
		}
		b.WriteString(answer[last:m[0]])
		last = m[1]
	}
	b.WriteString(answer[last:])
	return b.String()
}

func isIdent(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
