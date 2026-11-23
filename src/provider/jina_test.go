package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rishang/seek/config"
)

func TestJinaSearchSendsKeyAndMapsResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" || r.URL.Query().Get("q") != "go" || r.URL.Query().Get("type") != "news" {
			t.Errorf("unexpected request %s", r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer k" || r.Header.Get("X-Respond-With") != "no-content" {
			t.Errorf("bad headers: %v", r.Header)
		}
		w.Write([]byte(`{"code":200,"status":20000,"data":[{"title":"Go","url":"https://go.dev","description":"The Go language","publishedTime":"2026-01-02"}]}`))
	}))
	defer srv.Close()
	old := jinaSearchBase
	jinaSearchBase = srv.URL
	defer func() { jinaSearchBase = old }()

	sp := NewJinaProvider(config.ProviderConfig{APIKey: "k"}).(SearchProvider)
	res, err := sp.Search(context.Background(), "go", config.SearchOptions{Kind: config.KindNews})
	if err != nil {
		t.Fatal(err)
	}
	want := config.SearchResult{Title: "Go", URL: "https://go.dev", Snippet: "The Go language", PublishedDate: "2026-01-02"}
	if len(res) != 1 || res[0] != want {
		t.Fatalf("bad mapping: %+v", res)
	}
}

// Keyless jina is fetch-only and must not send an Authorization header.
func TestJinaKeylessReaderFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("keyless fetch sent Authorization %q", r.Header.Get("Authorization"))
		}
		var body struct{ URL string }
		json.NewDecoder(r.Body).Decode(&body)
		if r.Method != http.MethodPost || body.URL != "https://example.com/?a=1" {
			t.Errorf("unexpected %s body %+v", r.Method, body)
		}
		w.Write([]byte(`{"code":200,"data":{"url":"https://example.com/","content":"# Example"}}`))
	}))
	defer srv.Close()

	p := NewJinaProvider(config.ProviderConfig{Host: srv.URL})
	if _, ok := p.(SearchProvider); ok {
		t.Fatal("keyless jina should not offer search")
	}
	got, err := p.(FetchProvider).Fetch(context.Background(), "https://example.com/?a=1", config.FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "# Example" || got.Format != "markdown" {
		t.Fatalf("got %+v", got)
	}
}

func TestJinaFetchHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Respond-With") != "html" {
			t.Errorf("missing X-Respond-With: html")
		}
		w.Write([]byte(`{"data":{"html":"<p>hi</p>"}}`))
	}))
	defer srv.Close()
	old := jinaReaderBase
	jinaReaderBase = srv.URL
	defer func() { jinaReaderBase = old }()

	got, err := NewJinaProvider(config.ProviderConfig{APIKey: "k"}).(FetchProvider).
		Fetch(context.Background(), "https://example.com", config.FetchOptions{OutputFormat: config.FormatHTML})
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "<p>hi</p>" || got.Format != "html" {
		t.Fatalf("got %+v", got)
	}
}
