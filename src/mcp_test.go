package main

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"testing"
)

func decodeResult(t *testing.T, resp *rpcResponse) obj {
	t.Helper()
	b, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var m obj
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	return m
}

// authed returns an mcpAuth that is already authenticated (for non-auth tests).
func authed() *mcpAuth { return &mcpAuth{ok: true} }

func TestMCPInitializeEchoesProtocolVersion(t *testing.T) {
	resp := dispatchMCP(context.Background(), &rpcRequest{
		Method: "initialize", ID: json.RawMessage("1"),
		Params: json.RawMessage(`{"protocolVersion":"2025-03-26"}`),
	}, "", &mcpAuth{})
	if resp == nil || resp.Error != nil {
		t.Fatalf("initialize errored: %+v", resp)
	}
	res := decodeResult(t, resp)
	if res["protocolVersion"] != "2025-03-26" {
		t.Errorf("want echoed protocolVersion, got %v", res["protocolVersion"])
	}
	if _, ok := res["serverInfo"]; !ok {
		t.Error("missing serverInfo")
	}
}

func TestMCPToolsListHasAllTools(t *testing.T) {
	resp := dispatchMCP(context.Background(), &rpcRequest{Method: "tools/list", ID: json.RawMessage("2")}, "", authed())
	res := decodeResult(t, resp)
	tools, ok := res["tools"].([]any)
	if !ok || len(tools) != 4 {
		t.Fatalf("want 4 tools, got %v", res["tools"])
	}
	want := map[string]bool{"search": false, "fetch": false, "crawl": false, "agent": false}
	for _, tl := range tools {
		name := tl.(map[string]any)["name"].(string)
		if _, ok := want[name]; !ok {
			t.Errorf("unexpected tool %q", name)
		}
		want[name] = true
	}
	for n, seen := range want {
		if !seen {
			t.Errorf("tool %q missing", n)
		}
	}
}

func TestMCPUnknownMethodErrors(t *testing.T) {
	resp := dispatchMCP(context.Background(), &rpcRequest{Method: "bogus", ID: json.RawMessage("3")}, "", authed())
	if resp == nil || resp.Error == nil || resp.Error.Code != -32601 {
		t.Fatalf("want -32601 method not found, got %+v", resp)
	}
}

func TestMCPNotificationGetsNoResponse(t *testing.T) {
	// No id → notification → nil response (even without auth).
	if resp := dispatchMCP(context.Background(), &rpcRequest{Method: "notifications/initialized"}, "", &mcpAuth{}); resp != nil {
		t.Fatalf("notification should yield no response, got %+v", resp)
	}
}

func TestMCPToolsCallBadParams(t *testing.T) {
	resp := dispatchMCP(context.Background(), &rpcRequest{
		Method: "tools/call", ID: json.RawMessage("4"),
		Params: json.RawMessage(`{"name":123}`), // name should be a string
	}, "", authed())
	if resp == nil || resp.Error == nil || resp.Error.Code != -32602 {
		t.Fatalf("want -32602 invalid params, got %+v", resp)
	}
}

func TestMCPToolsCallValidationReturnsIsError(t *testing.T) {
	// search without a query / agent without a question: callTool errors
	// before hitting any provider, so it comes back as a tool result with
	// isError=true (not a protocol error).
	for _, tool := range []string{"search", "agent"} {
		resp := dispatchMCP(context.Background(), &rpcRequest{
			Method: "tools/call", ID: json.RawMessage("5"),
			Params: json.RawMessage(`{"name":"` + tool + `","arguments":{}}`),
		}, "", authed())
		if resp == nil || resp.Error != nil {
			t.Fatalf("%s: want a result, got %+v", tool, resp)
		}
		if res := decodeResult(t, resp); res["isError"] != true {
			t.Errorf("%s: want isError=true, got %v", tool, res["isError"])
		}
	}
}

func TestMCPUnknownToolReturnsIsError(t *testing.T) {
	resp := dispatchMCP(context.Background(), &rpcRequest{
		Method: "tools/call", ID: json.RawMessage("6"),
		Params: json.RawMessage(`{"name":"frobnicate","arguments":{}}`),
	}, "", authed())
	res := decodeResult(t, resp)
	if res["isError"] != true {
		t.Errorf("unknown tool should be isError=true, got %v", res["isError"])
	}
}

// --- Auth tests ---

func TestMCPAuthRejectsBadToken(t *testing.T) {
	cases := map[string]string{
		"missing": `{"protocolVersion":"2025-06-18"}`,
		"wrong":   `{"protocolVersion":"2025-06-18","token":"nope"}`,
	}
	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			resp := initializeAuth(&rpcRequest{
				ID:     json.RawMessage("1"),
				Params: json.RawMessage(params),
			}, "s3cret", &mcpAuth{})
			if resp == nil || resp.Error == nil || resp.Error.Code != -32600 {
				t.Fatalf("want -32600 auth error, got %+v", resp)
			}
		})
	}
}

func TestMCPAuthAcceptsCorrectToken(t *testing.T) {
	auth := &mcpAuth{}
	resp := initializeAuth(&rpcRequest{
		ID:     json.RawMessage("1"),
		Params: json.RawMessage(`{"protocolVersion":"2025-06-18","token":"s3cret"}`),
	}, "s3cret", auth)
	if resp == nil || resp.Error != nil {
		t.Fatalf("want success, got %+v", resp)
	}
	if !auth.ready() {
		t.Fatal("auth should be ready after successful initialize")
	}
}

func TestMCPAuthNotConfiguredAllowsAll(t *testing.T) {
	auth := &mcpAuth{}
	resp := initializeAuth(&rpcRequest{
		ID:     json.RawMessage("1"),
		Params: json.RawMessage(`{}`),
	}, "", auth)
	if resp == nil || resp.Error != nil {
		t.Fatalf("no-token-configured should succeed, got %+v", resp)
	}
}

func TestMCPToolsCallRequiresAuth(t *testing.T) {
	resp := dispatchMCP(context.Background(), &rpcRequest{
		Method: "tools/call", ID: json.RawMessage("2"),
		Params: json.RawMessage(`{"name":"search","arguments":{}}`),
	}, "s3cret", &mcpAuth{})
	if resp == nil || resp.Error == nil || resp.Error.Code != -32600 {
		t.Fatalf("want -32600 unauthorized, got %+v", resp)
	}
}

// --- Concurrency ---

// TestMCPConnConcurrentWrites exercises the mutex-guarded writer with many
// goroutines; run under -race it proves writes don't interleave or data-race.
func TestMCPConnConcurrentWrites(t *testing.T) {
	var buf bytes.Buffer
	conn := &mcpConn{enc: json.NewEncoder(&buf)}

	const n = 64
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			conn.send(rpcOK(json.RawMessage("1"), obj{"i": i}))
		}(i)
	}
	wg.Wait()

	// Every line must be a complete, parseable JSON-RPC response.
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	if len(lines) != n {
		t.Fatalf("want %d response lines, got %d", n, len(lines))
	}
	for _, ln := range lines {
		var r rpcResponse
		if err := json.Unmarshal(ln, &r); err != nil {
			t.Fatalf("interleaved/garbled line %q: %v", ln, err)
		}
	}
}
