package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rishang/seek/config"
)

func TestLinkupSearchRequestAndMapping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" || r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("unexpected %s auth %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		var body linkupSearchRequest
		json.NewDecoder(r.Body).Decode(&body)
		want := linkupSearchRequest{Q: "go", Depth: "standard", OutputType: "searchResults", MaxResults: 10, FromDate: "2026-01-02"}
		if body != want {
			t.Errorf("body = %+v, want %+v", body, want)
		}
		w.Write([]byte(`{"results":[
			{"type":"text","name":"Go","url":"https://go.dev","content":" Go lang ","publishedAt":"2026-01-03"},
			{"type":"image","name":"Gopher","url":"https://img.png"}]}`))
	}))
	defer srv.Close()
	old := linkupBase
	linkupBase = srv.URL
	defer func() { linkupBase = old }()

	tr := config.TimeRange{Start: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)}
	res, err := NewLinkupProvider(config.ProviderConfig{APIKey: "k"}).
		Search(context.Background(), "go", config.SearchOptions{TimeRange: tr})
	if err != nil {
		t.Fatal(err)
	}
	want := config.SearchResult{Title: "Go", URL: "https://go.dev", Snippet: "Go lang", PublishedDate: "2026-01-03"}
	if len(res) != 1 || res[0] != want {
		t.Fatalf("bad mapping (image results should be dropped): %+v", res)
	}
}

func TestLinkupFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body linkupFetchRequest
		json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path != "/fetch" || body.URL != "https://example.com" {
			t.Errorf("unexpected %s %+v", r.URL.Path, body)
		}
		w.Write([]byte(`{"favicon":"","markdown":"# Example"}`))
	}))
	defer srv.Close()
	old := linkupBase
	linkupBase = srv.URL
	defer func() { linkupBase = old }()

	got, err := NewLinkupProvider(config.ProviderConfig{APIKey: "k"}).
		Fetch(context.Background(), "https://example.com", config.FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "# Example" || got.Format != "markdown" {
		t.Fatalf("got %+v", got)
	}
}
