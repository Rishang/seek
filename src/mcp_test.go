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

func TestMCPInitializeEchoesProtocolVersion(t *testing.T) {
	resp := dispatchMCP(context.Background(), &rpcRequest{
		Method: "initialize", ID: json.RawMessage("1"),
		Params: json.RawMessage(`{"protocolVersion":"2025-03-26"}`),
	})
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

func TestMCPToolsListHasThreeTools(t *testing.T) {
	resp := dispatchMCP(context.Background(), &rpcRequest{Method: "tools/list", ID: json.RawMessage("2")})
	res := decodeResult(t, resp)
	tools, ok := res["tools"].([]any)
	if !ok || len(tools) != 3 {
		t.Fatalf("want 3 tools, got %v", res["tools"])
	}
	want := map[string]bool{"search": false, "scrape": false, "crawl": false}
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
	resp := dispatchMCP(context.Background(), &rpcRequest{Method: "bogus", ID: json.RawMessage("3")})
	if resp == nil || resp.Error == nil || resp.Error.Code != -32601 {
		t.Fatalf("want -32601 method not found, got %+v", resp)
	}
}

func TestMCPNotificationGetsNoResponse(t *testing.T) {
	// No id → notification → nil response.
	if resp := dispatchMCP(context.Background(), &rpcRequest{Method: "notifications/initialized"}); resp != nil {
		t.Fatalf("notification should yield no response, got %+v", resp)
	}
}

func TestMCPToolsCallBadParams(t *testing.T) {
	resp := dispatchMCP(context.Background(), &rpcRequest{
		Method: "tools/call", ID: json.RawMessage("4"),
		Params: json.RawMessage(`{"name":123}`), // name should be a string
	})
	if resp == nil || resp.Error == nil || resp.Error.Code != -32602 {
		t.Fatalf("want -32602 invalid params, got %+v", resp)
	}
}

func TestMCPToolsCallValidationReturnsIsError(t *testing.T) {
	// search without a query: callTool errors before hitting any provider, so
	// it comes back as a tool result with isError=true (not a protocol error).
	resp := dispatchMCP(context.Background(), &rpcRequest{
		Method: "tools/call", ID: json.RawMessage("5"),
		Params: json.RawMessage(`{"name":"search","arguments":{}}`),
	})
	if resp == nil || resp.Error != nil {
		t.Fatalf("want a result, got %+v", resp)
	}
	res := decodeResult(t, resp)
	if res["isError"] != true {
		t.Errorf("want isError=true, got %v", res["isError"])
	}
}

func TestMCPUnknownToolReturnsIsError(t *testing.T) {
	resp := dispatchMCP(context.Background(), &rpcRequest{
		Method: "tools/call", ID: json.RawMessage("6"),
		Params: json.RawMessage(`{"name":"frobnicate","arguments":{}}`),
	})
	res := decodeResult(t, resp)
	if res["isError"] != true {
		t.Errorf("unknown tool should be isError=true, got %v", res["isError"])
	}
}

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
