package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rishang/seek/config"
	"github.com/rishang/seek/provider"
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

func TestOpenAPISpecIsValidJSON(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal([]byte(openAPISpec), &doc); err != nil {
		t.Fatalf("openAPISpec is not valid JSON: %v", err)
	}
	if doc["openapi"] == nil || doc["paths"] == nil {
		t.Fatalf("openAPISpec missing required top-level keys")
	}
}

func TestDocsRoutesServe(t *testing.T) {
	h := serveMux("token-set") // docs must be reachable even with auth on

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "\"openapi\"") {
		t.Fatalf("/openapi.json: code %d body %q", rec.Code, rec.Body.String()[:min(80, rec.Body.Len())])
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/docs", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "swagger-ui") {
		t.Fatalf("/docs: code %d", rec.Code)
	}
}
