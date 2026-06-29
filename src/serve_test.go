package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rishang/seek/config"
	"github.com/rishang/seek/provider"
	"github.com/urfave/cli/v3"
)

// setupEmptyFactory points the package globals at a factory with no configured
// providers, so the operation runners return clean errors instead of panicking.
func setupEmptyFactory(t *testing.T) {
	t.Helper()
	cfg = config.Default()
	factory = provider.NewFactory(nil)
}

func TestServeHealthz(t *testing.T) {
	rec := httptest.NewRecorder()
	serveMux("").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz: want 200, got %d", rec.Code)
	}
	if strings.TrimSpace(rec.Body.String()) != "ok" {
		t.Fatalf("healthz body: %q", rec.Body.String())
	}
}

func TestServeAuthRejectsMissingAndWrongToken(t *testing.T) {
	h := serveMux("s3cret")

	// No Authorization header.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/search", strings.NewReader(`{"query":"x"}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token: want 401, got %d", rec.Code)
	}

	// Wrong token.
	req := httptest.NewRequest(http.MethodPost, "/search", strings.NewReader(`{"query":"x"}`))
	req.Header.Set("Authorization", "Bearer nope")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: want 401, got %d", rec.Code)
	}
}

func TestServeValidation(t *testing.T) {
	h := serveMux("") // no auth

	// Missing query.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/search", strings.NewReader(`{}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty query: want 400, got %d", rec.Code)
	}

	// Unknown field is rejected.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/fetch", strings.NewReader(`{"url":"x","bogus":1}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field: want 400, got %d", rec.Code)
	}

	// Wrong method on a POST route → 405 (ServeMux method pattern).
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/search", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /search: want 405, got %d", rec.Code)
	}
}

func TestServeReachesProviderLayer(t *testing.T) {
	setupEmptyFactory(t) // auto chain empty → opSearch errors, not panics

	rec := httptest.NewRecorder()
	serveMux("").ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/search", strings.NewReader(`{"query":"hello"}`)))
	// Valid request, auth passed, validation passed — fails only at the provider.
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("no-provider search: want 502, got %d (body %s)", rec.Code, rec.Body.String())
	}
}

func TestServeConcurrencyLimit(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 2)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		entered <- struct{}{}
		<-release
		w.WriteHeader(http.StatusOK)
	})
	h := withConcurrencyLimit(2, inner)

	go func() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/search", nil))
	}()
	go func() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/search", nil))
	}()
	<-entered
	<-entered

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/search", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("over cap: want 503, got %d", rec.Code)
	}

	close(release)

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz bypass: want 200, got %d", rec.Code)
	}
}

func TestServeMaxConcurrentFromEnv(t *testing.T) {
	t.Setenv("SEEK_SERVE_MAX_CONCURRENT", "30")
	cmd := &cli.Command{Flags: []cli.Flag{&cli.IntFlag{Name: "max-concurrent", Value: defaultMaxConcurrent}}}
	n, err := serveMaxConcurrent(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if n != 30 {
		t.Fatalf("want 30 from env, got %d", n)
	}
}

func TestServeMaxConcurrentFlagOverridesEnv(t *testing.T) {
	t.Setenv("SEEK_SERVE_MAX_CONCURRENT", "30")
	cmd := &cli.Command{Flags: []cli.Flag{&cli.IntFlag{Name: "max-concurrent", Value: defaultMaxConcurrent}}}
	if err := cmd.Set("max-concurrent", "10"); err != nil {
		t.Fatal(err)
	}
	n, err := serveMaxConcurrent(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if n != 10 {
		t.Fatalf("want 10 from flag, got %d", n)
	}
}

func TestIsLoopback(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:8787": true,
		"localhost:8787": true,
		"[::1]:8787":     true,
		"0.0.0.0:8787":   false,
		"":               false,
		"192.168.1.5:80": false,
	}
	for addr, want := range cases {
		if got := isLoopback(addr); got != want {
			t.Errorf("isLoopback(%q) = %v, want %v", addr, got, want)
		}
	}
}
