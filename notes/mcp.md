# `seek mcp` — MCP server (stdio)

`seek mcp` speaks the Model Context Protocol over stdio so MCP-capable agents
can call seek's search/scrape/crawl tools with the same provider factory and
`auto` failover the CLI uses.

## Transport

Newline-delimited JSON-RPC 2.0 on stdin/stdout: one JSON object per line.
`json.Encoder.Encode` appends the trailing newline, which is exactly the framing
MCP's stdio transport expects. **stdout is the protocol channel** — all logging
goes to stderr (package `logx`), so it never corrupts the stream.

## Methods

| Method                      | Behaviour |
|-----------------------------|-----------|
| `initialize`                | Returns `protocolVersion` (echoes the client's when given, else `2025-06-18`), `capabilities.tools`, and `serverInfo`. |
| `ping`                      | Returns `{}`. |
| `tools/list`                | Lists the `search`, `scrape`, `crawl` tools with JSON-Schema `inputSchema`. |
| `tools/call`                | Dispatches to the op runners (`ops.go`). |
| notifications (no `id`)     | Handled silently, no response (e.g. `notifications/initialized`). |
| unknown method (with `id`)  | JSON-RPC error `-32601`. |
| parse error                 | JSON-RPC error `-32700`, `id: null`. |

### Tools

- `search` — args `{query (required), provider?, range?, start?, end?}` → ranked
  results as JSON text.
- `scrape` — args `{url (required), provider?, format?}` → page content text.
- `crawl`  — args `{url (required), provider?}` → crawl result as JSON text.

The argument structs are the *same* types the HTTP server decodes
(`searchRequest`/`scrapeRequest`/`crawlRequest`), so the two surfaces never
drift.

### Error convention

Transport/protocol problems (bad params, unknown method) use the JSON-RPC
`error` field. **Tool execution** failures (provider down, validation) come back
as a normal result with `isError: true` and the message in a text content block,
per MCP convention — so the model can see and react to the failure.

## Concurrency

The reader loop is sequential (one stdin), but each request is dispatched in its
own goroutine, so a slow scrape never blocks other in-flight calls. Writes to
stdout are serialized by `mcpConn`'s mutex; responses may arrive out of order
(each carries its request `id`, which JSON-RPC allows). On EOF the loop waits
for outstanding goroutines (`sync.WaitGroup`) before returning.

The factory is read-only after startup, so concurrent tool calls are safe.

## Debug logging

Run with `-v` (or `SEEK_LOG=debug`) to see, on stderr: server ready, each
`<- method=… id=…`, each `tools/call name=…`, parse errors, and shutdown.

## ponytail notes

- Pure stdlib JSON-RPC over stdio; no MCP SDK dependency. The protocol surface
  used here (initialize / tools) is small and stable.
- Ceiling: `initialize` assumes one protocol revision but echoes the client's
  requested `protocolVersion`, so a newer client still negotiates. Revisit if a
  future revision changes the tools/call result shape.
- Scanner buffer is capped at 16 MiB per incoming line (requests are small;
  large payloads ride the *outgoing* side, which `Encoder` streams).
