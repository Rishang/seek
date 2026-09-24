package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rishang/seek/config"
)

// fakeDeps serves three pages (c fails to fetch); the extract model returns a
// shared claim for a and b, plus an unrelated fact that must be dropped.
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
			user := req.Messages[len(req.Messages)-1].Content
			if req.ResponseFormat == nil {
				if req.Model != "big" || req.ReasoningEffort != "high" {
					t.Errorf("synthesis model=%q effort=%q, want big/high", req.Model, req.ReasoningEffort)
				}
				*synthIn = user
				return Message{Content: "answer [1][2]"}, nil
			}
			if req.Model != "small" || req.ReasoningEffort != "" {
				t.Errorf("extract model=%q effort=%q, want small/none", req.Model, req.ReasoningEffort)
			}
			if strings.Contains(user, "https://a") {
				return Message{Content: "```json\n" + `{"facts":[{"claim":"Android 17 adds X.","relevance":3},{"claim":"Buy now!","relevance":0}]}` + "\n```"}, nil
			}
			return Message{Content: `{"facts":[{"claim":"android 17 adds x","relevance":2},{"claim":"Y is faster","relevance":1}]}`}, nil
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

// toolCall builds a model tool call for scripted research turns.
func toolCall(id, name, args string) ToolCall {
	var c ToolCall
	c.ID, c.Type, c.Function.Name, c.Function.Arguments = id, "function", name, args
	return c
}

func TestResearchLoop(t *testing.T) {
	d := fakeDeps(t, new(string))
	extract := d.Complete
	turn := 0
	d.Complete = func(ctx context.Context, req Request) (Message, error) {
		if req.ResponseFormat != nil {
			return extract(ctx, req) // fetch wrapper → extract model
		}
		if req.ReasoningEffort != "high" {
			t.Errorf("research effort = %q", req.ReasoningEffort)
		}
		turn++
		switch turn {
		case 1:
			return Message{Role: "assistant", ToolCalls: []ToolCall{toolCall("s1", "search", `{"query":"android 17"}`)}}, nil
		case 2:
			if last := req.Messages[len(req.Messages)-1]; last.Role != "tool" || !strings.Contains(last.Content, "https://b") {
				t.Errorf("search result not fed back: %+v", last)
			}
			return Message{Role: "assistant", ToolCalls: []ToolCall{
				toolCall("f1", "fetch", `{"url":"https://a"}`),
				toolCall("f2", "fetch", `{"url":"https://b"}`),
				toolCall("f3", "fetch", `{"url":"https://c"}`),
			}}, nil
		default:
			n := len(req.Messages)
			if req.Tools != nil {
				t.Fatalf("turn %d: expected forced answer without tools", turn)
			}
			if !strings.Contains(req.Messages[n-4].Content, "Android 17 adds X.") || !strings.HasPrefix(req.Messages[n-2].Content, "error:") {
				t.Errorf("fetch results not fed back: %+v", req.Messages[n-4:])
			}
			return Message{Content: "X ships [1]"}, nil
		}
	}

	res, err := Research(context.Background(), d, Options{Model: "big", ExtractModel: "small", ReasoningEffort: "high", MaxSteps: 2}, "android 17")
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	if res.Answer != "X ships [1]" || len(res.Sources) != 2 || res.Sources[0].Title == "" {
		t.Fatalf("result = %+v", res)
	}
	// a and b state the same claim: merged into one fact citing both; "Buy now!" (relevance 0) dropped.
	if len(res.Skipped) != 1 || len(res.Facts) != 2 || res.Facts[0].Relevance != 3 || len(res.Facts[0].Sources) != 2 {
		t.Fatalf("skipped=%+v facts=%+v", res.Skipped, res.Facts)
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
		Request{Model: "m", Messages: []Message{{Role: "user", Content: "hi"}}, Tools: researchTools, ReasoningEffort: "low"})
	if err != nil || calls.Load() != 2 || len(msg.ToolCalls) != 1 ||
		msg.ToolCalls[0].Function.Arguments != `{"query":"q"}` || msg.Usage == nil || msg.Usage.CompletionTokens != 2 {
		t.Fatalf("Complete = %+v, %v (calls %d)", msg, err, calls.Load())
	}
	c.FirstTokenTTL = 0
	if msg, err = c.Complete(context.Background(), Request{Model: "m", ReasoningEffort: "low"}); err != nil || calls.Load() != 4 || len(msg.ToolCalls) != 1 {
		t.Fatalf("upstream-error retry = %+v, %v (calls %d)", msg, err, calls.Load())
	}
}
