package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rishang/seek/config"
	"github.com/rishang/seek/logx"
	"github.com/urfave/cli/v3"
)

const defaultMaxConcurrent = 50

// serveCmd exposes search/fetch/crawl over a small JSON HTTP API. net/http
// serves every request in its own goroutine, so the server is concurrent by
// default; the operation runners only read the shared factory, so concurrent
// requests are safe.
func serveCmd() *cli.Command {
	return &cli.Command{
		Name:      "serve",
		Usage:     "Run seek as an HTTP API",
		UsageText: "seek serve [--addr host:port] [--token TOKEN]",
		Description: "Expose search, fetch, and crawl over HTTP as JSON. Listens on\n" +
			"127.0.0.1:8787 by default.\n\n" +
			"  POST /search  {\"query\":\"...\",\"provider\":\"auto\",\"range\":7}\n" +
			"  POST /fetch  {\"url\":\"https://...\",\"format\":\"markdown\"}\n" +
			"  POST /crawl   {\"url\":\"https://...\"}\n" +
			"  GET  /healthz\n\n" +
			"Auth: set --token (or SEEK_SERVE_TOKEN) to require `Authorization: Bearer\n" +
			"<token>` on every request. Without a token the API is UNAUTHENTICATED —\n" +
			"anyone who can reach the address can spend your provider keys, so only\n" +
			"bind a tokenless server to loopback.",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "addr", Value: "127.0.0.1:8787", Usage: "Listen address (host:port)"},
			&cli.StringFlag{Name: "token", Usage: "Require this Bearer token (or set SEEK_SERVE_TOKEN)"},
			&cli.IntFlag{Name: "max-concurrent", Value: defaultMaxConcurrent, Usage: "Max in-flight operation requests (or set SEEK_SERVE_MAX_CONCURRENT; GET /healthz is exempt)"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			token := cmd.String("token")
			if token == "" {
				token = os.Getenv("SEEK_SERVE_TOKEN")
			}
			maxConcurrent, err := serveMaxConcurrent(cmd)
			if err != nil {
				return err
			}
			return runServe(ctx, cmd.String("addr"), token, maxConcurrent)
		},
	}
}

// serveMaxConcurrent resolves the in-flight request cap: --max-concurrent overrides
// SEEK_SERVE_MAX_CONCURRENT, which defaults to 50.
func serveMaxConcurrent(cmd *cli.Command) (int, error) {
	if cmd.IsSet("max-concurrent") {
		n := int(cmd.Int("max-concurrent"))
		if n <= 0 {
			return 0, fmt.Errorf("--max-concurrent must be positive")
		}
		return n, nil
	}
	if v := os.Getenv("SEEK_SERVE_MAX_CONCURRENT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid SEEK_SERVE_MAX_CONCURRENT %q: must be a positive integer", v)
		}
		return n, nil
	}
	return defaultMaxConcurrent, nil
}

// runServe builds the HTTP server and serves until ctx is cancelled.
func runServe(ctx context.Context, addr, token string, maxConcurrent int) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           serveHandler(token, maxConcurrent),
		ReadHeaderTimeout: 10 * time.Second,
	}

	if token == "" {
		logx.Warn("serve: no token set — the API is UNAUTHENTICATED; anyone who can reach %s can use your provider keys", addr)
		if !isLoopback(addr) {
			logx.Warn("serve: %q is not a loopback address; do not expose a tokenless API on a public interface", addr)
		}
	}

	// Shut down gracefully when the context is cancelled (e.g. SIGINT).
	go func() {
		<-ctx.Done()
		logx.Debug("serve: context cancelled, shutting down")
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	fmt.Fprintf(os.Stderr, "seek serve: listening on http://%s\n", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// serveMux wires the routes. Split out so tests can exercise the handlers
// without binding a socket.
func serveMux(token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /search", auth(token, handleSearch))
	mux.HandleFunc("POST /fetch", auth(token, handleFetch))
	mux.HandleFunc("POST /crawl", auth(token, handleCrawl))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	return withLogging(mux)
}

// serveHandler is the production stack: routes plus concurrency limiting.
func serveHandler(token string, maxConcurrent int) http.Handler {
	return withConcurrencyLimit(maxConcurrent, withLogging(serveMux(token)))
}

// withConcurrencyLimit caps in-flight operation requests. GET /healthz bypasses
// the cap so liveness probes stay reliable under load.
func withConcurrencyLimit(max int, next http.Handler) http.Handler {
	sem := make(chan struct{}, max)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		select {
		case sem <- struct{}{}:
			defer func() { <-sem }()
			next.ServeHTTP(w, r)
		default:
			httpError(w, http.StatusServiceUnavailable, "too many concurrent requests")
		}
	})
}

// withLogging emits a debug line per request (method, path, remote addr).
func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logx.Debug("serve: %s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)
		next.ServeHTTP(w, r)
	})
}

// auth wraps a handler with constant-time Bearer-token checking. When token is
// empty the check is skipped (the caller is warned at startup).
func auth(token string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if token != "" {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
				logx.Debug("serve: auth rejected for %s %s", r.Method, r.URL.Path)
				httpError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
		}
		h(w, r)
	}
}

type searchRequest struct {
	Query    string `json:"query"`
	Provider string `json:"provider,omitempty"`
	Range    int    `json:"range,omitempty"`
	Start    string `json:"start,omitempty"` // DD/MM/YYYY
	End      string `json:"end,omitempty"`   // DD/MM/YYYY
}

func handleSearch(w http.ResponseWriter, r *http.Request) {
	var req searchRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Query == "" {
		httpError(w, http.StatusBadRequest, "query is required")
		return
	}
	tr, err := buildTimeRange(req.Range, req.Start, req.End)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	results, err := opSearch(r.Context(), req.Provider, req.Query, config.SearchOptions{TimeRange: tr})
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

type fetchRequest struct {
	URL      string `json:"url"`
	Provider string `json:"provider,omitempty"`
	Format   string `json:"format,omitempty"`
}

func handleFetch(w http.ResponseWriter, r *http.Request) {
	var req fetchRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.URL == "" {
		httpError(w, http.StatusBadRequest, "url is required")
		return
	}
	result, err := opFetch(r.Context(), req.Provider, req.URL, req.Format)
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

type crawlRequest struct {
	URL      string `json:"url"`
	Provider string `json:"provider,omitempty"`
}

func handleCrawl(w http.ResponseWriter, r *http.Request) {
	var req crawlRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.URL == "" {
		httpError(w, http.StatusBadRequest, "url is required")
		return
	}
	result, err := opCrawl(r.Context(), req.Provider, req.URL)
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// decodeJSON reads a JSON body into v, writing a 400 and returning false on
// failure. Bodies are capped to guard against oversized payloads.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)) // 1 MiB ceiling
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func httpError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// isLoopback reports whether addr's host is a loopback interface. An empty host
// (net/http binds every interface) counts as non-loopback.
func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
