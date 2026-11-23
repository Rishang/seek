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

func TestSerperSearchKinds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-KEY") != "k" {
			t.Errorf("missing X-API-KEY header")
		}
		var body serperSearchRequest
		json.NewDecoder(r.Body).Decode(&body)
		switch r.URL.Path {
		case "/search":
			if body.TBS != "cdr:1,cd_min:01/02/2026" {
				t.Errorf("tbs = %q", body.TBS)
			}
			w.Write([]byte(`{"organic":[{"title":"Go","link":"https://go.dev","snippet":"lang","date":"Jan 2, 2026"}]}`))
		case "/news":
			w.Write([]byte(`{"news":[{"title":"N","link":"https://n","snippet":"s","date":"2 days ago","imageUrl":"https://thumb"}]}`))
		case "/images":
			w.Write([]byte(`{"images":[{"title":"Cat","link":"https://page","imageUrl":"https://img.jpg"}]}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	old := serperSearchBase
	serperSearchBase = srv.URL
	defer func() { serperSearchBase = old }()

	p := NewSerperProvider(config.ProviderConfig{APIKey: "k"})
	tr := config.TimeRange{Start: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)}
	cases := []struct {
		opts config.SearchOptions
		want config.SearchResult
	}{
		{config.SearchOptions{TimeRange: tr}, config.SearchResult{Title: "Go", URL: "https://go.dev", Snippet: "lang", PublishedDate: "Jan 2, 2026"}},
		{config.SearchOptions{Kind: config.KindNews}, config.SearchResult{Title: "N", URL: "https://n", Snippet: "s", PublishedDate: "2 days ago"}},
		{config.SearchOptions{Kind: config.KindImages}, config.SearchResult{Title: "Cat", URL: "https://page", ImageURL: "https://img.jpg"}},
	}
	for _, c := range cases {
		res, err := p.Search(context.Background(), "q", c.opts)
		if err != nil {
			t.Fatal(err)
		}
		if len(res) != 1 || res[0] != c.want {
			t.Errorf("kind %q: got %+v, want %+v", c.opts.Kind, res, c.want)
		}
	}
}

func TestSerperFetchFallsBackToText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body serperScrapeRequest
		json.NewDecoder(r.Body).Decode(&body)
		if !body.IncludeMarkdown || body.URL != "https://example.com" {
			t.Errorf("unexpected body %+v", body)
		}
		w.Write([]byte(`{"text":"plain page"}`))
	}))
	defer srv.Close()
	old := serperScrapeURL
	serperScrapeURL = srv.URL
	defer func() { serperScrapeURL = old }()

	got, err := NewSerperProvider(config.ProviderConfig{APIKey: "k"}).
		Fetch(context.Background(), "https://example.com", config.FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "plain page" {
		t.Fatalf("got %+v", got)
	}
}
