package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
)

const researchPrompt = `You are a deep-research agent. Answer the user's question by investigating the web with your tools.
- search(query): returns result titles, URLs and snippets. Search several angles, follow leads, refine queries.
- fetch(url, focus): reads a page and returns the facts relevant to focus, labelled with a source id [n].
- datetime(): today's date and time; call it first when the question depends on what is current or recent.
Plan, gather from multiple independent sources, cross-check claims that conflict, and stop once the question is well covered.
Then reply without calling tools: one concise, information-dense markdown answer citing [n] after each statement.
Cite only ids returned by fetch. Do not add a sources list and do not invent facts.`

const finalNudge = "Step budget reached. Answer now from the facts gathered so far, without calling tools."

// researchTools are the seek wrappers exposed to the model.
var researchTools = []Tool{
	{Type: "function", Function: ToolFunction{
		Name:        "search",
		Description: "Web search. Returns up to 10 results with title, URL and snippet.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"query": map[string]any{"type": "string", "description": "Search query"}},
			"required":   []string{"query"},
		},
	}},
	{Type: "function", Function: ToolFunction{
		Name:        "fetch",
		Description: "Read a web page. Returns the facts relevant to focus, extracted from the page, labelled with a citable source id.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url":   map[string]any{"type": "string", "description": "Page URL"},
				"focus": map[string]any{"type": "string", "description": "What to look for on the page; defaults to the research question"},
			},
			"required": []string{"url"},
		},
	}},
	datetimeTool,
}

// research is the state shared by concurrent tool calls within one run.
type research struct {
	d        Deps
	opts     Options
	question string

	mu        sync.Mutex
	sources   []Source
	byURL     map[string]int    // url -> index into sources/perSource
	titles    map[string]string // url -> title seen in search results
	perSource [][]extracted
	skipped   []Skip
}

// Research answers question with a tool-calling loop: the model decides what
// to search and fetch, fetched pages are distilled by the extract model before
// the research model sees them, and the loop ends when the model answers or
// MaxSteps is reached (then it is asked to answer from what it has).
func Research(ctx context.Context, d Deps, opts Options, question string) (*Result, error) {
	opts.defaults()
	r := &research{d: d, opts: opts, question: question, byURL: map[string]int{}, titles: map[string]string{}}
	msgs := []Message{{Role: "system", Content: researchPrompt + "\n" + today()}, {Role: "user", Content: question}}

	for step := 1; ; step++ {
		req := Request{Model: opts.Model, Messages: msgs, Tools: researchTools, ReasoningEffort: opts.ReasoningEffort}
		if step > opts.MaxSteps {
			req.Tools = nil
			req.Messages = append(msgs, Message{Role: "user", Content: finalNudge})
		}
		d.trace("agent: step %d → %s (%d message(s), tools=%t)", step, opts.Model, len(req.Messages), req.Tools != nil)
		msg, err := d.Complete(ctx, req)
		if err != nil {
			return r.result(""), fmt.Errorf("agent research step %d: %w", step, err)
		}
		d.trace("agent: step %d ← %d tool call(s), %d chars of text", step, len(msg.ToolCalls), len(msg.Content))
		if len(msg.ToolCalls) == 0 || req.Tools == nil {
			d.trace("agent: answered after %d step(s)", step)
			res := r.result(cleanAnswer(msg.Content))
			if res.Answer == "" {
				return res, errors.New("agent: model returned an empty answer")
			}
			return res, nil
		}
		msgs = append(msgs, msg)
		msgs = append(msgs, r.runTools(ctx, step, msg.ToolCalls)...)
	}
}

// runTools executes one turn's tool calls in parallel, returning the tool
// messages in call order. Tool errors go back to the model as text so it can
// adapt; they never end the run.
func (r *research) runTools(ctx context.Context, step int, calls []ToolCall) []Message {
	out := make([]Message, len(calls))
	var wg sync.WaitGroup
	for i, c := range calls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.d.trace("agent: step %d %s %s", step, c.Function.Name, c.Function.Arguments)
			content, err := r.call(ctx, c)
			if err != nil {
				r.d.trace("agent: step %d %s failed: %v", step, c.Function.Name, err)
				content = "error: " + err.Error()
			} else {
				r.d.trace("agent: step %d %s → %d chars to model", step, c.Function.Name, len(content))
			}
			out[i] = Message{Role: "tool", ToolCallID: c.ID, Content: content}
		}()
	}
	wg.Wait()
	return out
}

func (r *research) call(ctx context.Context, c ToolCall) (string, error) {
	var args struct {
		Query string `json:"query"`
		URL   string `json:"url"`
		Focus string `json:"focus"`
	}
	if err := json.Unmarshal([]byte(c.Function.Arguments), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	switch c.Function.Name {
	case "datetime":
		return now(), nil
	case "search":
		if args.Query == "" {
			return "", errors.New("query is required")
		}
		return r.search(ctx, args.Query)
	case "fetch":
		if args.URL == "" {
			return "", errors.New("url is required")
		}
		return r.fetch(ctx, args.URL, args.Focus)
	}
	return "", fmt.Errorf("unknown tool %q", c.Function.Name)
}

func (r *research) search(ctx context.Context, query string) (string, error) {
	hits, err := r.d.Search(ctx, query)
	if err != nil {
		return "", err
	}
	r.d.trace("agent: search %q → %d hit(s)", query, len(hits))
	if len(hits) == 0 {
		return "no results", nil
	}
	r.mu.Lock()
	for _, h := range hits {
		if h.Title != "" {
			r.titles[h.URL] = h.Title
		}
	}
	r.mu.Unlock()
	var b strings.Builder
	for i, h := range hits[:min(len(hits), 10)] {
		fmt.Fprintf(&b, "%d. %s\n   %s\n", i+1, h.Title, h.URL)
		if h.Snippet != "" {
			fmt.Fprintf(&b, "   %s\n", h.Snippet)
		}
	}
	return b.String(), nil
}

// fetch distills url through the extract model and records it as a source.
// A URL is fetched once per run; repeats return the recorded facts.
func (r *research) fetch(ctx context.Context, url, focus string) (string, error) {
	r.mu.Lock()
	if i, ok := r.byURL[url]; ok {
		s, facts := r.sources[i], r.perSource[i]
		r.mu.Unlock()
		r.d.trace("agent: %s already fetched as [%d], reusing facts", url, s.ID)
		return formatFacts(s, facts), nil
	}
	r.mu.Unlock()

	if focus == "" {
		focus = r.question
	}
	facts, err := extract(ctx, r.d, r.opts.ExtractModel, focus, url)

	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		r.skipped = append(r.skipped, Skip{URL: url, Err: err.Error()})
		return "", err
	}
	if i, ok := r.byURL[url]; ok { // a parallel call won the race
		return formatFacts(r.sources[i], r.perSource[i]), nil
	}
	s := Source{ID: len(r.sources) + 1, Title: r.titles[url], URL: url}
	r.byURL[url] = len(r.sources)
	r.sources = append(r.sources, s)
	r.perSource = append(r.perSource, facts)
	r.d.trace("agent: recorded source [%d] %s", s.ID, url)
	return formatFacts(s, facts), nil
}

// formatFacts renders one source's relevant facts for the research model.
func formatFacts(s Source, facts []extracted) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Source [%d] %s\n", s.ID, s.URL)
	n := 0
	for _, f := range facts {
		if f.Relevance > 0 && strings.TrimSpace(f.Claim) != "" {
			fmt.Fprintf(&b, "- %s\n", strings.TrimSpace(f.Claim))
			n++
		}
	}
	if n == 0 {
		b.WriteString("(no relevant facts on this page)\n")
	}
	return b.String()
}

// result snapshots the gathered sources and merged facts.
func (r *research) result(answer string) *Result {
	r.mu.Lock()
	defer r.mu.Unlock()
	return &Result{
		Answer:  answer,
		Facts:   merge(r.sources, r.perSource),
		Sources: r.sources,
		Skipped: r.skipped,
	}
}
