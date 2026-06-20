package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/rishang/seek/config"
	"github.com/rishang/seek/logx"
	"github.com/urfave/cli/v3"
)

// seek speaks the Model Context Protocol over stdio: newline-delimited JSON-RPC
// 2.0 messages on stdin/stdout. Logs go to stderr (see package logx), keeping
// stdout a clean protocol channel.
//
// ponytail: assumes this protocol revision. initialize echoes the client's
// requested protocolVersion when present, so a newer client still negotiates.
const (
	mcpProtocolVersion = "2025-06-18"
	mcpServerVersion   = "0.1.0"
)

type obj = map[string]any

func mcpCmd() *cli.Command {
	return &cli.Command{
		Name:      "mcp",
		Usage:     "Run seek as an MCP server (stdio)",
		UsageText: "seek mcp",
		Description: "Speak the Model Context Protocol over stdio so MCP-capable agents can\n" +
			"call seek's search, fetch, and crawl tools. stdout carries the JSON-RPC\n" +
			"stream; logs go to stderr. Requests are handled concurrently.",
		Action: func(ctx context.Context, _ *cli.Command) error {
			return runMCP(ctx)
		},
	}
}

// runMCP reads JSON-RPC requests line by line and dispatches each in its own
// goroutine, so a slow fetch never blocks other in-flight calls. Writes are
// serialized by mcpConn; responses may arrive out of order (each carries its
// request id, as JSON-RPC allows).
func runMCP(ctx context.Context) error {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024) // allow large request lines

	conn := &mcpConn{enc: json.NewEncoder(os.Stdout)}
	var wg sync.WaitGroup

	logx.Debug("mcp: ready, reading JSON-RPC from stdin")
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		msg := append([]byte(nil), line...) // copy: Scanner reuses its buffer
		wg.Add(1)
		go func() {
			defer wg.Done()
			handleMCPMessage(ctx, msg, conn)
		}()
	}
	wg.Wait()
	logx.Debug("mcp: stdin closed, shutting down")
	return sc.Err()
}

// mcpConn serializes writes to stdout. json.Encoder.Encode appends a newline,
// which is exactly the stdio framing MCP expects.
type mcpConn struct {
	mu  sync.Mutex
	enc *json.Encoder
}

func (c *mcpConn) send(v any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.enc.Encode(v)
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"` // absent on notifications
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func handleMCPMessage(ctx context.Context, line []byte, conn *mcpConn) {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		logx.Debug("mcp: parse error: %v", err)
		conn.send(rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"),
			Error: &rpcError{Code: -32700, Message: "parse error"}})
		return
	}
	logx.Debug("mcp: <- method=%s id=%s", req.Method, string(req.ID))
	if resp := dispatchMCP(ctx, &req); resp != nil {
		conn.send(resp)
	}
}

// dispatchMCP routes a request to its handler. It returns nil for notifications
// (requests without an id), which get no response.
func dispatchMCP(ctx context.Context, req *rpcRequest) *rpcResponse {
	switch req.Method {
	case "initialize":
		return rpcOK(req.ID, initializeResult(req.Params))
	case "ping":
		return rpcOK(req.ID, obj{})
	case "tools/list":
		return rpcOK(req.ID, obj{"tools": mcpTools})
	case "tools/call":
		return toolsCall(ctx, req)
	default:
		if len(req.ID) == 0 {
			return nil // unknown notification (e.g. notifications/initialized)
		}
		return rpcErr(req.ID, -32601, "method not found: "+req.Method)
	}
}

func initializeResult(params json.RawMessage) obj {
	version := mcpProtocolVersion
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if json.Unmarshal(params, &p) == nil && p.ProtocolVersion != "" {
		version = p.ProtocolVersion
	}
	return obj{
		"protocolVersion": version,
		"capabilities":    obj{"tools": obj{}},
		"serverInfo":      obj{"name": "seek", "version": mcpServerVersion},
	}
}

// mcpTools is the tools/list payload. The three tools mirror the CLI commands;
// their argument shapes reuse the serve request structs.
var mcpTools = []obj{
	{
		"name":        "search",
		"description": "Web search across providers with automatic failover. Returns ranked results (title, url, snippet, published_date) as JSON.",
		"inputSchema": obj{
			"type": "object",
			"properties": obj{
				"query":    obj{"type": "string", "description": "Search query"},
				"provider": obj{"type": "string", "description": "Override provider; defaults to the configured one (auto)"},
				"range":    obj{"type": "integer", "description": "Only results from the last N days"},
				"start":    obj{"type": "string", "description": "Earliest published date, DD/MM/YYYY"},
				"end":      obj{"type": "string", "description": "Latest published date, DD/MM/YYYY"},
			},
			"required": []string{"query"},
		},
	},
	{
		"name":        "fetch",
		"description": "Extract a single page's content. Returns the page as markdown (or the requested format).",
		"inputSchema": obj{
			"type": "object",
			"properties": obj{
				"url":      obj{"type": "string", "description": "Page URL to fetch"},
				"provider": obj{"type": "string", "description": "Override provider; defaults to the configured one (auto)"},
				"format":   obj{"type": "string", "enum": []string{"markdown", "html", "json"}, "description": "Output format (default markdown)"},
			},
			"required": []string{"url"},
		},
	},
	{
		"name":        "crawl",
		"description": "Crawl a website from a starting URL and return its pages as JSON.",
		"inputSchema": obj{
			"type": "object",
			"properties": obj{
				"url":      obj{"type": "string", "description": "Start URL to crawl"},
				"provider": obj{"type": "string", "description": "Override provider; defaults to the configured one (firecrawl)"},
			},
			"required": []string{"url"},
		},
	},
}

func toolsCall(ctx context.Context, req *rpcRequest) *rpcResponse {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return rpcErr(req.ID, -32602, "invalid params: "+err.Error())
	}
	logx.Debug("mcp: tools/call name=%s", p.Name)
	text, err := callTool(ctx, p.Name, p.Arguments)
	if err != nil {
		// MCP convention: tool execution errors ride back in the result with
		// isError=true so the model can see and react to them, rather than as a
		// transport-level JSON-RPC error.
		return rpcOK(req.ID, toolText(err.Error(), true))
	}
	return rpcOK(req.ID, toolText(text, false))
}

// callTool dispatches a tools/call to the shared operation runners. The arg
// shapes are the same structs the HTTP server decodes.
func callTool(ctx context.Context, name string, args json.RawMessage) (string, error) {
	switch name {
	case "search":
		var a searchRequest
		if err := json.Unmarshal(args, &a); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
		if a.Query == "" {
			return "", fmt.Errorf("query is required")
		}
		tr, err := buildTimeRange(a.Range, a.Start, a.End)
		if err != nil {
			return "", err
		}
		results, err := opSearch(ctx, a.Provider, a.Query, config.SearchOptions{TimeRange: tr})
		if err != nil {
			return "", err
		}
		return jsonString(results), nil

	case "fetch":
		var a fetchRequest
		if err := json.Unmarshal(args, &a); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
		if a.URL == "" {
			return "", fmt.Errorf("url is required")
		}
		result, err := opFetch(ctx, a.Provider, a.URL, a.Format)
		if err != nil {
			return "", err
		}
		return result.Content, nil

	case "crawl":
		var a crawlRequest
		if err := json.Unmarshal(args, &a); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
		if a.URL == "" {
			return "", fmt.Errorf("url is required")
		}
		result, err := opCrawl(ctx, a.Provider, a.URL)
		if err != nil {
			return "", err
		}
		return jsonString(result), nil
	}
	return "", fmt.Errorf("unknown tool %q", name)
}

func toolText(s string, isErr bool) obj {
	return obj{
		"content": []obj{{"type": "text", "text": s}},
		"isError": isErr,
	}
}

func rpcOK(id json.RawMessage, result any) *rpcResponse {
	return &rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func rpcErr(id json.RawMessage, code int, msg string) *rpcResponse {
	return &rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
}

func jsonString(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}
