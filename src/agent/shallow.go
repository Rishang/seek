package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// The shallow (default) mode follows the pattern coding agents use for web
// tools (see notes/research/coding-agent-web-tools.md): seek searches and
// ranks without an LLM, then the model sees the numbered results and decides
// whether to answer from the snippets, read a few of them, or search once
// more. Budgets are enforced here in code, not in the prompt.

// answerRules is how to write the answer; shared by the tool turns and the
// tool-free final turn.
const answerRules = `When you answer:
- Lead with a direct, complete answer to the question as asked (for "migrate X to Y", the migration steps; for "how to build X", the setup and code). Put edge cases and caveats after.
- Be concrete: names, APIs, flags, versions, dates, numbers. Skip marketing and filler. Use short headings or bullets, roughly 150-400 words.
- For how-to questions, include a short runnable example built only from imports, classes, functions and model IDs shown in the results or pages, for the language the question implies. Every import or package must appear in a page for that same language or ecosystem; never combine SDKs from different languages. If no model ID or key name is shown, use a placeholder like <model-id>.
- Cite inline as [n] after each statement, using only the given ids. Never guess versions, dates or names the sources don't state. Pages may be excerpts; don't claim something is absent just because an excerpt omits it. If the sources don't cover part of the question, say so in one line.
Do not add a sources list and do not invent facts.`

const shallowPrompt = `You answer a developer's question using web search results. You get numbered results with title, host, URL and snippet, plus any pages already read.
Decide first:
- If the results and pages already answer the question, answer right away without tools.
- Otherwise call read with the fewest result ids that likely contain the answer (usually 1, at most 3). Prefer official docs and primary sources, and pages for the exact language, framework or SDK the question names. Set focus to what you need from the pages. Never re-read a page shown under "Pages read".
- If the question has one exact, changing answer (a version, number, price or current status), read the most authoritative result (the official or primary source) to confirm it before answering; snippets and secondary pages are often out of date.
- Call search only if no result looks relevant (at most once).
- If the answer depends on today's date (current, latest, today, recent), treat results older than the question needs as stale and say their date.
` + answerRules

const finalPrompt = `You answer a developer's question from the numbered web search results and pages given. Write the answer now; no tools are available.
` + answerRules

const (
	shallowToolTurns = 2               // model turns that may call tools before a forced answer
	shallowMaxReads  = 4               // pages read per run
	shallowReadIDs   = 3               // ids per read call
	shallowReadChars = 16000           // excerpt budget per read call, split across its ids
	shallowPageMax   = 12000           // excerpt cap for a single page
	shallowFetchTTL  = 8 * time.Second // per-page fetch timeout; a slow page returns an error to the model
)

const budgetSpent = "Tool budget used up. Answer now from the results and pages you have."

// today is the system-prompt line that dates every request, so "latest" and
// "today" answers aren't anchored to the model's training date.
func today() string {
	return "Current date and time: " + time.Now().Format("Monday, 2006-01-02 15:04 MST (UTC-07:00)") + "."
}

var shallowTools = []Tool{
	{Type: "function", Function: ToolFunction{
		Name: "read",
		Description: "Read search results in full. Use only when the snippets don't answer the question. " +
			"Pick the fewest ids (usually 1, max 3) for the exact language/framework/SDK asked about. " +
			"Returns a verbatim excerpt of each page focused on `focus`.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"ids":   map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "Result ids to read, e.g. [2]"},
				"focus": map[string]any{"type": "string", "description": "What you need from the pages, incl. language/framework if relevant"},
			},
			"required": []string{"ids"},
		},
	}},
	{Type: "function", Function: ToolFunction{
		Name:        "search",
		Description: "Run one more web search when no result is relevant. Returns new numbered results. At most once.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"query": map[string]any{"type": "string", "description": "Search query"}},
			"required":   []string{"query"},
		},
	}},
}

// shallow is one run's state; tool calls within a turn run in parallel.
type shallow struct {
	d        Deps
	question string

	mu       sync.Mutex
	sources  []Source
	weights  map[string]float64
	reads    int
	searched bool
	read     map[int]bool
}

// Run is the shallow agent: seek searches (splitting multi-part questions)
// and ranks results, then the model answers from the snippets or first reads
// a few chosen results (see shallowPrompt). Simple questions cost one LLM call
// and no fetches. Research is the deep, multi-turn mode.
func Run(ctx context.Context, d Deps, opts Options, question string) (*Result, error) {
	opts.defaults()
	hits, err := gather(ctx, d, opts.ExtractModel, question)
	if err != nil {
		return nil, fmt.Errorf("agent search: %w", err)
	}
	s := &shallow{
		d:        d,
		question: question,
		sources:  pickSources(rankHits(question, hits), opts.Sources),
		weights:  termWeights(queryTerms(question), hits),
		read:     map[int]bool{},
	}
	d.trace("agent: %d hit(s), showing %d result(s)", len(hits), len(s.sources))
	if len(s.sources) == 0 {
		return nil, errors.New("agent: search returned no results")
	}

	// Each turn is one self-contained request: tool calls are used only for
	// the model's decision, and their results come back as plain text in the
	// next user message. Some gateway backends drop or ignore role:"tool"
	// messages, and a history of tool calls makes some models keep calling
	// tools even when none are offered.
	var log []string // tool results so far, oldest first
	for turn := 1; ; turn++ {
		final := turn > shallowToolTurns
		req := s.request(opts, log, final)
		msg, err := d.Complete(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("agent answer: %w", err)
		}
		if len(msg.ToolCalls) > 0 && !final {
			log = append(log, s.runTools(ctx, turn, msg.ToolCalls)...)
			continue
		}
		answer := cleanAnswer(msg.Content)
		if answer == "" && !final {
			// Some models stop with an empty reply mid-loop; ask once, tool-free.
			d.trace("agent: empty reply on turn %d, asking for the answer without tools", turn)
			if msg, err = d.Complete(ctx, s.request(opts, log, true)); err != nil {
				return nil, fmt.Errorf("agent answer: %w", err)
			}
			answer = cleanAnswer(msg.Content)
		}
		res := &Result{Answer: answer, Sources: s.sources}
		if answer == "" {
			return res, errors.New("agent: model returned an empty answer")
		}
		d.trace("agent: answered on turn %d after %d read(s)", turn, s.reads)
		return res, nil
	}
}

// request builds one turn: the question, every result so far and the tool
// results, as a single user message. final drops the tools and uses the
// answer-only prompt.
func (s *shallow) request(opts Options, log []string, final bool) Request {
	s.mu.Lock()
	results := formatResults(s.sources)
	s.mu.Unlock()
	var b strings.Builder
	fmt.Fprintf(&b, "Question: %s\n\nSearch results:\n%s", s.question, results)
	if len(log) > 0 {
		b.WriteString("Pages read and tool results:\n\n")
		b.WriteString(strings.Join(log, "\n\n===\n\n"))
	}
	req := Request{Model: opts.Model, ReasoningEffort: opts.ReasoningEffort, Tools: shallowTools}
	sys := shallowPrompt
	if final {
		req.Tools, sys = nil, finalPrompt
		b.WriteString("\n\n" + budgetSpent)
	}
	req.Messages = []Message{{Role: "system", Content: sys + "\n" + today()}, {Role: "user", Content: b.String()}}
	return req
}

// formatResults numbers sources for the model.
func formatResults(sources []Source) string {
	var b strings.Builder
	for _, s := range sources {
		fmt.Fprintf(&b, "[%d] %s\n%s | %s\n%s\n\n", s.ID, s.Title, hostOf(s.URL), s.URL, strings.TrimSpace(s.Snippet))
	}
	return b.String()
}

// runTools executes one turn's tool calls in parallel, returning each call's
// result as text in call order. Errors and spent budgets go back to the model
// as text so it can adapt; they never end the run.
func (s *shallow) runTools(ctx context.Context, turn int, calls []ToolCall) []string {
	out := make([]string, len(calls))
	var wg sync.WaitGroup
	for i, c := range calls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.d.trace("agent: turn %d %s %s", turn, c.Function.Name, c.Function.Arguments)
			content, err := s.call(ctx, c)
			if err != nil {
				s.d.trace("agent: turn %d %s failed: %v", turn, c.Function.Name, err)
				content = "error: " + err.Error()
			}
			out[i] = fmt.Sprintf("%s(%s):\n%s", c.Function.Name, c.Function.Arguments, content)
		}()
	}
	wg.Wait()
	return out
}

func (s *shallow) call(ctx context.Context, c ToolCall) (string, error) {
	var args struct {
		IDs   []int  `json:"ids"`
		Focus string `json:"focus"`
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(c.Function.Arguments), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	switch c.Function.Name {
	case "read":
		return s.readIDs(ctx, args.IDs, args.Focus)
	case "search":
		return s.search(ctx, args.Query)
	}
	return "", fmt.Errorf("unknown tool %q", c.Function.Name)
}

// readIDs fetches the requested results in parallel and returns verbatim
// excerpts focused on the question plus focus. Unknown, already-read and
// over-budget ids are reported back instead of fetched.
func (s *shallow) readIDs(ctx context.Context, ids []int, focus string) (string, error) {
	s.mu.Lock()
	var todo []Source
	var notes []string
	for _, id := range ids {
		switch {
		case len(todo) == shallowReadIDs:
			notes = append(notes, fmt.Sprintf("[%d] skipped: at most %d ids per read", id, shallowReadIDs))
		case id < 1 || id > len(s.sources):
			notes = append(notes, fmt.Sprintf("[%d] is not a result id", id))
		case s.read[id]:
			notes = append(notes, fmt.Sprintf("[%d] was already read; its excerpt is under \"Pages read\" above", id))
		case s.reads == shallowMaxReads:
			notes = append(notes, fmt.Sprintf("[%d] skipped: %s", id, budgetSpent))
		default:
			s.read[id] = true
			s.reads++
			todo = append(todo, s.sources[id-1])
		}
	}
	weights := focusWeights(s.weights, focus)
	s.mu.Unlock()
	if len(todo) == 0 {
		return strings.Join(notes, "\n"), nil
	}

	limit := min(shallowPageMax, shallowReadChars/len(todo))
	pages := make([]string, len(todo))
	var wg sync.WaitGroup
	for i, src := range todo {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fctx, cancel := context.WithTimeout(ctx, shallowFetchTTL)
			defer cancel()
			page, err := s.d.Fetch(fctx, src.URL)
			if err != nil {
				pages[i] = fmt.Sprintf("[%d] %s\nerror: could not read page: %v", src.ID, src.URL, err)
				s.d.trace("agent: read [%d] %s failed: %v", src.ID, src.URL, err)
				return
			}
			raw := len(page)
			page = focusPage(cleanPage(page), weights, limit)
			s.d.trace("agent: read [%d] %s (%d chars → %d kept)", src.ID, src.URL, raw, len(page))
			pages[i] = fmt.Sprintf("[%d] %s\n%s\n\n%s", src.ID, src.Title, src.URL, page)
		}()
	}
	wg.Wait()
	return strings.Join(append(pages, notes...), "\n\n---\n\n"), nil
}

// search runs one extra query and appends its new results as fresh ids.
func (s *shallow) search(ctx context.Context, query string) (string, error) {
	if query == "" {
		return "", errors.New("query is required")
	}
	s.mu.Lock()
	if s.searched {
		s.mu.Unlock()
		return "Only one extra search is allowed. " + budgetSpent, nil
	}
	s.searched = true
	s.mu.Unlock()

	hits, err := s.d.Search(ctx, query)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]bool{}
	for _, src := range s.sources {
		seen[src.URL] = true
	}
	var fresh []Source
	for _, h := range rankHits(query, hits) {
		if h.URL == "" || seen[h.URL] || len(fresh) == DefaultSources {
			continue
		}
		seen[h.URL] = true
		src := Source{ID: len(s.sources) + 1, Title: h.Title, URL: h.URL, Snippet: h.Snippet}
		s.sources = append(s.sources, src)
		fresh = append(fresh, src)
	}
	for t, w := range termWeights(queryTerms(query), hits) {
		if _, ok := s.weights[t]; !ok {
			s.weights[t] = w
		}
	}
	s.d.trace("agent: extra search %q → %d new result(s)", query, len(fresh))
	if len(fresh) == 0 {
		return "no new results", nil
	}
	return formatResults(fresh), nil
}

// focusWeights adds the read call's focus terms to the question's term
// weights, so excerpts favour what the model asked for. Focus terms the hits
// never mention get a neutral weight of 1.
func focusWeights(base map[string]float64, focus string) map[string]float64 {
	w := make(map[string]float64, len(base))
	for t, v := range base {
		w[t] = v
	}
	for _, t := range queryTerms(focus) {
		if _, ok := w[t]; !ok {
			w[t] = 1
		}
	}
	return w
}
