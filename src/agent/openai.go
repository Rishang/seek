package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/imroc/req/v3"
)

// Client is a minimal OpenAI Chat Completions client. Any endpoint that
// follows the OpenAI spec works; there is no vendor-specific code.
type Client struct {
	baseURL string
	apiKey  string
	http    *req.Client

	// FirstTokenTTL is how long a request may go without its first streamed
	// chunk before it counts as stuck and is retried once. Zero disables it.
	FirstTokenTTL time.Duration
	// Trace is an optional progress hook (e.g. retries); nil disables it.
	Trace func(format string, args ...any)
}

// DefaultFirstTokenTTL separates a stuck request (no chunk at all) from a slow
// but streaming one: gateways sometimes park a request on an unresponsive
// backend, and a retry then usually answers in seconds, while a long answer
// that is already streaming must never be cut off.
// ponytail: fixed threshold; tune if a model's prefill regularly exceeds it.
const DefaultFirstTokenTTL = 8 * time.Second

// errStalled marks an attempt cancelled for sending no first chunk in time.
var errStalled = errors.New("no response chunk before the first-token deadline")

// errUpstream marks an error event inside the stream: the gateway's backend
// failed mid-generation (e.g. it could not parse the model's tool call), which
// a fresh attempt usually avoids.
var errUpstream = errors.New("upstream stream error")

// NewClient returns a client for baseURL (e.g. "https://host/v1"). An empty
// apiKey omits the Authorization header, for servers that need none.
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http:    req.C().SetTimeout(300 * time.Second),

		FirstTokenTTL: DefaultFirstTokenTTL,
	}
}

// Message is one chat message. Assistant messages may carry ToolCalls; tool
// results set Role "tool" and ToolCallID.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Usage      *Usage     `json:"-"` // set on replies only; never sent back
}

// Usage is the token accounting reported with a reply, when the endpoint sends it.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// ToolCall is a function call requested by the model. Arguments is a JSON
// object encoded as a string, per the OpenAI spec.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// Tool declares a function the model may call.
type Tool struct {
	Type     string       `json:"type"` // always "function"
	Function ToolFunction `json:"function"`
}

// ToolFunction is a tool's name, description and JSON-schema parameters.
type ToolFunction struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

// ResponseFormat constrains the reply shape ({"type":"json_object"}).
type ResponseFormat struct {
	Type string `json:"type"`
}

// Request is a chat-completions request body. ReasoningEffort is sent only
// when set (e.g. "low", "medium", "high"); the endpoint validates the value.
type Request struct {
	Model           string          `json:"model"`
	Messages        []Message       `json:"messages"`
	Tools           []Tool          `json:"tools,omitempty"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
	ResponseFormat  *ResponseFormat `json:"response_format,omitempty"`
	Stream          bool            `json:"stream,omitempty"`
	StreamOptions   *streamOptions  `json:"stream_options,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// streamChunk is one server-sent event of a streamed chat completion.
type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *Usage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Complete sends one chat-completions request and returns the assistant
// message. It streams the reply so a stuck request (no first chunk within
// FirstTokenTTL) can be told apart from a long one: a stuck attempt, or one
// that ends in an upstream error event, is retried once, and the retry has no
// first-token deadline.
func (c *Client) Complete(ctx context.Context, body Request) (Message, error) {
	msg, err := c.stream(ctx, body, c.FirstTokenTTL)
	if (!errors.Is(err, errStalled) && !errors.Is(err, errUpstream)) || ctx.Err() != nil {
		return msg, err
	}
	if c.Trace != nil {
		c.Trace("llm: %s attempt failed (%v), retrying once", body.Model, err)
	}
	return c.stream(ctx, body, 0)
}

// stream runs one streamed attempt; firstTTL > 0 arms the first-token watchdog.
func (c *Client) stream(ctx context.Context, body Request, firstTTL time.Duration) (Message, error) {
	body.Stream = true
	body.StreamOptions = &streamOptions{IncludeUsage: true}

	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var stalled atomic.Bool
	var watchdog *time.Timer
	if firstTTL > 0 {
		watchdog = time.AfterFunc(firstTTL, func() { stalled.Store(true); cancel() })
		defer watchdog.Stop()
	}
	fail := func(err error) (Message, error) {
		if stalled.Load() {
			return Message{}, errStalled
		}
		return Message{}, err
	}

	r := c.http.R().SetContext(sctx).SetBody(body).DisableAutoReadResponse()
	if c.apiKey != "" {
		r.SetBearerAuthToken(c.apiKey)
	}
	resp, err := r.Post(c.baseURL + "/chat/completions")
	if err != nil {
		return fail(fmt.Errorf("agent %s chat request failed: %w", body.Model, err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fail(fmt.Errorf("agent %s chat returned status %d: %s", body.Model, resp.StatusCode, b))
	}

	msg := Message{Role: "assistant"}
	var content strings.Builder
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	gotChunk := false
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") { // blank lines and ": keep-alive" comments
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		if !gotChunk {
			gotChunk = true
			if watchdog != nil {
				watchdog.Stop()
			}
		}
		var ch streamChunk
		if err := json.Unmarshal([]byte(data), &ch); err != nil {
			return Message{}, fmt.Errorf("agent %s chat stream: bad chunk: %w", body.Model, err)
		}
		if ch.Error != nil {
			return Message{}, fmt.Errorf("agent %s chat %w: %s", body.Model, errUpstream, ch.Error.Message)
		}
		if ch.Usage != nil {
			msg.Usage = ch.Usage
		}
		for _, choice := range ch.Choices {
			content.WriteString(choice.Delta.Content)
			for _, tc := range choice.Delta.ToolCalls {
				for len(msg.ToolCalls) <= tc.Index {
					msg.ToolCalls = append(msg.ToolCalls, ToolCall{Type: "function"})
				}
				t := &msg.ToolCalls[tc.Index]
				if tc.ID != "" {
					t.ID = tc.ID
				}
				if tc.Type != "" {
					t.Type = tc.Type
				}
				if tc.Function.Name != "" {
					t.Function.Name = tc.Function.Name
				}
				t.Function.Arguments += tc.Function.Arguments
			}
		}
	}
	if err := sc.Err(); err != nil {
		return fail(fmt.Errorf("agent %s chat stream: %w", body.Model, err))
	}
	if !gotChunk {
		return fail(errors.New("agent " + body.Model + " chat returned an empty stream"))
	}
	msg.Content = content.String()
	return msg, nil
}
