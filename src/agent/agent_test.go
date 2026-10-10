package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rishang/seek/config"
)

// fakeDeps serves search results for the shallow mode; the model answers
// from the snippets.
func fakeDeps(t *testing.T, synthIn *string) Deps {
	return Deps{
		Search: func(context.Context, string) ([]config.SearchResult, error) {
			return []config.SearchResult{
				{Title: "A", URL: "https://a"}, {URL: "https://a"}, // duplicate URL skipped
				{Title: "B", URL: "https://b", Snippet: "b snippet"}, {Title: "C", URL: "https://c"},
			}, nil
		},
		Fetch: func(_ context.Context, url string) (string, error) {
			if url == "https://c" {
				return "", errors.New("boom")
			}
			return "page " + url, nil
		},
		Complete: func(_ context.Context, req Request) (Message, error) {
			if req.Model != "big" || req.ReasoningEffort != "high" {
				t.Errorf("answer model=%q effort=%q, want big/high", req.Model, req.ReasoningEffort)
			}
			*synthIn = req.Messages[len(req.Messages)-1].Content
			return Message{Content: "answer [1][2]"}, nil
		},
	}
}

var testOpts = Options{Model: "big", ExtractModel: "small", ReasoningEffort: "high"}

func TestRunShallow(t *testing.T) {
	var synthIn string
	d := fakeDeps(t, &synthIn)
	d.Fetch = func(context.Context, string) (string, error) {
		t.Fatal("snippets answered: nothing should be read")
		return "", nil
	}
	res, err := Run(context.Background(), d, testOpts, "android 17")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Answer != "answer [1][2]" || len(res.Sources) != 3 {
		t.Fatalf("result = %+v", res)
	}
	if !strings.Contains(synthIn, "[2] B\nb | https://b\nb snippet") {
		t.Fatalf("model input:\n%s", synthIn)
	}
	// Footer lists only cited ids.
	if md := res.Markdown(); !strings.Contains(md, "sources:\n[1] [A](https://a)\n[2] [B](https://b)\n") || strings.Contains(md, "https://c") {
		t.Fatalf("markdown sources:\n%s", md)
	}
}

func TestRunShallowReads(t *testing.T) {
	d := fakeDeps(t, new(string))
	turn := 0
	d.Complete = func(_ context.Context, req Request) (Message, error) {
		turn++
		if !strings.Contains(req.Messages[0].Content, "Current date and time: "+time.Now().Format("Monday, 2006-01-02")) {
			t.Error("system prompt lacks today's date")
		}
		switch turn {
		case 1:
			return Message{Role: "assistant", ToolCalls: []ToolCall{toolCall("r1", "read", `{"ids":[2,9],"focus":"x"}`)}}, nil
		case 2:
			return Message{Role: "assistant", ToolCalls: []ToolCall{toolCall("r2", "read", `{"ids":[1]}`)}}, nil
		}
		if req.Tools != nil {
			t.Error("tool budget: expected the final turn to be tool-free")
		}
		last := req.Messages[len(req.Messages)-1].Content
		if !strings.Contains(last, "page https://b") || !strings.Contains(last, "[9] is not a result id") {
			t.Errorf("read result = %q", last)
		}
		return Message{Content: "from page [2]"}, nil
	}
	res, err := Run(context.Background(), d, testOpts, "android 17")
	if err != nil || res.Answer != "from page [2]" || turn != 3 {
		t.Fatalf("Run = %+v, %v (turns %d)", res, err, turn)
	}
}

func TestRankHits(t *testing.T) {
	hits := []config.SearchResult{
		{Title: "Agno Pharma staff", URL: "https://leadiq.com/c/agno-pharma"},
		{Title: "Agno tutorial", URL: "https://www.youtube.com/watch?v=1"},
		{Title: "Workflows - Agno", URL: "https://docs.agno.com/workflows/overview"},
	}
	got := rankHits("agno workflows", hits)
	if got[0].URL != "https://docs.agno.com/workflows/overview" || got[2].URL != "https://www.youtube.com/watch?v=1" {
		t.Fatalf("rank = %+v", got)
	}
	goHits := []config.SearchResult{
		{Title: "Launch features release homes", URL: "https://finance.yahoo.com/news/x", Snippet: "go release features release features good google"},
		{Title: "Go 1.25 Release Notes", URL: "https://go.dev/doc/go1.25"},
	}
	if got := rankHits("go 1.25 release new features", goHits); got[0].URL != "https://go.dev/doc/go1.25" {
		t.Fatalf("go rank = %+v", got)
	}
	for _, c := range []struct {
		s, term string
		want    bool
	}{{"go.dev/doc", "go", true}, {"google", "go", false}, {"go1.25", "1.25", true}, {"1.250", "1.25", false}} {
		if hasWord(c.s, c.term) != c.want {
			t.Errorf("hasWord(%q, %q) != %t", c.s, c.term, c.want)
		}
	}
	if !lowValueHost("https://m.youtube.com/x") || lowValueHost("https://docs.agno.com") {
		t.Fatal("lowValueHost misclassified")
	}
}

func TestRunNoResults(t *testing.T) {
	d := fakeDeps(t, new(string))
	d.Search = func(context.Context, string) ([]config.SearchResult, error) { return nil, nil }
	if _, err := Run(context.Background(), d, testOpts, "q"); err == nil {
		t.Fatal("expected error when search returns nothing")
	}
}

// toolCall builds a model tool call for scripted turns.
func toolCall(id, name, args string) ToolCall {
	var c ToolCall
	c.ID, c.Type, c.Function.Name, c.Function.Arguments = id, "function", name, args
	return c
}

// TestResearch scripts a deep run: part 1 (features) is covered in round 1,
// part 2 (release date) only by the gap round's page. Invented facts and wrong
// numbers are dropped before the answer model sees anything.
func TestResearch(t *testing.T) {
	pages := map[string]string{
		"https://a.dev/new":  "# Android 17\n\nAndroid 17 adds [desktop windowing](https://a.dev/w) on tablets.\n",
		"https://b.com/x":    "Android 17 adds desktop windowing.\n",
		"https://c.org/f":    "Buy our phones today.\n",
		"https://d.dev/date": "Android 17 ships in June 2026.\n",
	}
	results := map[string][]config.SearchResult{
		"android 17 whats new":         {{Title: "Android 17 new", URL: "https://a.dev/new"}, {Title: "Android 17", URL: "https://b.com/x"}},
		"android 17 features":          {{URL: "https://a.dev/new"}, {Title: "Phones", URL: "https://c.org/f"}},
		"android 17 release date":      {{URL: "https://b.com/x"}},
		"When is Android 17 released?": {{Title: "Android 17 date", URL: "https://d.dev/date"}},
	}
	extracts := map[string]string{
		"https://a.dev/new": `{"facts":[
			{"claim":"Android 17 adds desktop windowing.","quote":"Android 17 adds desktop windowing on tablets","parts":[1],"relevance":3},
			{"claim":"Android 17 launched in 2025.","quote":"Android 17 adds","parts":[2],"relevance":3},
			{"claim":"Android 17 has a new camera app.","quote":"a brand new camera app","parts":[1],"relevance":3}]}`,
		"https://b.com/x":    `{"facts":[{"claim":"android 17 adds desktop windowing","quote":"Android 17 adds desktop windowing.","parts":[1],"relevance":2}]}`,
		"https://c.org/f":    `{"facts":[{"claim":"Buy phones","quote":"Buy our phones today","parts":[],"relevance":0}]}`,
		"https://d.dev/date": `{"facts":[{"claim":"Android 17 ships in June 2026.","quote":"Android 17 ships in June 2026","parts":[2],"relevance":3}]}`,
	}
	var mu sync.Mutex
	var searches []string
	var brief string
	d := Deps{
		Search: func(_ context.Context, q string) ([]config.SearchResult, error) {
			mu.Lock()
			searches = append(searches, q)
			mu.Unlock()
			return results[q], nil
		},
		Fetch: func(_ context.Context, url string) (string, error) { return pages[url], nil },
		Complete: func(_ context.Context, req Request) (Message, error) {
			sys, user := req.Messages[0].Content, req.Messages[1].Content
			switch sys {
			case deepPlanPrompt:
				return Message{Content: `{"parts":[{"need":"What is new in Android 17?","query":"android 17 features"},` +
					`{"need":"When is Android 17 released?","query":"android 17 release date"}]}`}, nil
			case extractPrompt:
				if req.Model != "small" || req.ReasoningEffort != "" {
					t.Errorf("extract model=%q effort=%q, want small/none", req.Model, req.ReasoningEffort)
				}
				for url, out := range extracts {
					if strings.Contains(user, "Page ("+url+")") {
						return Message{Content: out}, nil
					}
				}
				t.Errorf("unexpected extract input:\n%s", user)
				return Message{Content: `{"facts":[]}`}, nil
			}
			if req.Model != "big" || req.ReasoningEffort != "high" || req.Tools != nil {
				t.Errorf("answer model=%q effort=%q tools=%v, want big/high/none", req.Model, req.ReasoningEffort, req.Tools)
			}
			brief = user
			return Message{Content: "Windowing [1][2]; ships June 2026 [3][9]."}, nil
		},
	}

	res, err := Research(context.Background(), d, testOpts, "android 17 whats new")
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	if len(searches) != 4 {
		t.Errorf("searches = %q, want the question, 2 part queries and 1 gap search", searches)
	}
	var urls []string
	for _, s := range res.Sources {
		urls = append(urls, s.URL)
	}
	if strings.Join(urls, " ") != "https://a.dev/new https://b.com/x https://d.dev/date" {
		t.Fatalf("sources = %v (pages without facts must not be numbered)", urls)
	}
	for _, want := range []string{
		"1. What is new in Android 17? (covered)\n2. When is Android 17 released? (covered)\n",
		"- Android 17 adds desktop windowing. [1][2]\n- Android 17 ships in June 2026. [3]\n",
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("brief lacks %q:\n%s", want, brief)
		}
	}
	for _, bad := range []string{"2025", "camera", "Buy"} {
		if strings.Contains(brief, bad) {
			t.Errorf("brief contains dropped fact %q:\n%s", bad, brief)
		}
	}
	if res.Answer != "Windowing [1][2]; ships June 2026 [3]." || len(res.Facts) != 2 {
		t.Fatalf("answer=%q facts=%+v", res.Answer, res.Facts)
	}
}

func TestResearchNoFacts(t *testing.T) {
	d := Deps{
		Search: func(context.Context, string) ([]config.SearchResult, error) {
			return []config.SearchResult{{URL: "https://x.dev"}}, nil
		},
		Fetch: func(context.Context, string) (string, error) { return "nothing useful", nil },
		Complete: func(_ context.Context, req Request) (Message, error) {
			if req.ResponseFormat == nil {
				t.Fatal("the answer model must not run without facts")
			}
			return Message{Content: `{"facts":[]}`}, nil // also an empty plan: the question is its own part
		},
	}
	if _, err := Research(context.Background(), d, testOpts, "q"); err == nil || !strings.Contains(err.Error(), "no relevant facts") {
		t.Fatalf("err = %v", err)
	}
}

func TestGroundedMergeCites(t *testing.T) {
	page := "Go 1.25 ships [the new GC](https://go.dev/gc) by default... Tests run faster."
	squashed, lower := " "+normalize(mdLink.ReplaceAllString(page, "$1"))+" ", strings.ToLower(page)
	for _, c := range []struct {
		claim, quote string
		want         bool
	}{
		{"Go 1.25 ships the new GC by default.", "Go 1.25 ships the new GC by default", true},
		{"Go 1.25 defaults to the new GC and faster tests.", "ships the new GC ... Tests run faster", true},
		{"Go 1.26 ships the new GC.", "Go 1.25 ships the new GC", false}, // number not on the page
		{"Go has generics.", "Go has generics", false},                   // quote not on the page
		{"Tests are fast.", "ests run fast", false},                      // partial words don't match
		{"Go ships it.", "", false},
	} {
		if got := grounded(extracted{Claim: c.claim, Quote: c.quote}, squashed, lower); got != c.want {
			t.Errorf("grounded(%q, %q) = %t", c.claim, c.quote, got)
		}
	}

	facts := merge([]Source{{ID: 1}, {ID: 2}}, [][]extracted{
		{{Claim: "Android 17 adds desktop windowing.", Relevance: 2}, {Claim: "Apps must target SDK 35 or higher.", Relevance: 3}},
		{{Claim: "android 17 adds desktop windowing", Relevance: 3}, {Claim: "Apps must target SDK 36 or higher.", Relevance: 3}},
	})
	if len(facts) != 3 || facts[0].Claim != "Android 17 adds desktop windowing." || !slices.Equal(facts[0].Sources, []int{1, 2}) || facts[0].Relevance != 3 {
		t.Fatalf("merge = %+v (same claim merges; SDK 35 vs 36 must not)", facts)
	}

	if got := knownCites("a [1] b [4] args[7] c[2]", 2); got != "a [1] b  args[7] c[2]" {
		t.Fatalf("knownCites = %q", got)
	}
}

func TestClientComplete(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer k" ||
			body["reasoning_effort"] != "low" || body["stream"] != true {
			t.Errorf("path=%s auth=%q body=%v", r.URL.Path, r.Header.Get("Authorization"), body)
		}
		switch calls.Add(1) {
		case 1: // stuck: only a keep-alive comment, then silence
			_, _ = w.Write([]byte(": PROCESSING\n\n"))
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		case 3: // upstream failure mid-stream
			_, _ = w.Write([]byte(`data: {"error":{"message":"Parsing failed"}}` + "\n\n"))
			return
		}
		for _, ev := range []string{
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"1","type":"function","function":{"name":"search","arguments":"{\"que"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ry\":\"q\"}"}}]}}]}`,
			`{"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":2}}`,
			`[DONE]`,
		} {
			_, _ = w.Write([]byte("data: " + ev + "\n\n"))
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL+"/v1/", "k")
	c.FirstTokenTTL = 100 * time.Millisecond
	msg, err := c.Complete(context.Background(),
		Request{Model: "m", Messages: []Message{{Role: "user", Content: "hi"}}, Tools: shallowTools, ReasoningEffort: "low"})
	if err != nil || calls.Load() != 2 || len(msg.ToolCalls) != 1 ||
		msg.ToolCalls[0].Function.Arguments != `{"query":"q"}` || msg.Usage == nil || msg.Usage.CompletionTokens != 2 {
		t.Fatalf("Complete = %+v, %v (calls %d)", msg, err, calls.Load())
	}
	c.FirstTokenTTL = 0
	if msg, err = c.Complete(context.Background(), Request{Model: "m", ReasoningEffort: "low"}); err != nil || calls.Load() != 4 || len(msg.ToolCalls) != 1 {
		t.Fatalf("upstream-error retry = %+v, %v (calls %d)", msg, err, calls.Load())
	}
}

func TestCleanAnswer(t *testing.T) {
	if got := cleanAnswer(" a【1†L2-L3】 b[4†L9-L11] c[2] "); got != "a[1] b[4] c[2]" {
		t.Fatalf("cleanAnswer = %q", got)
	}
}
