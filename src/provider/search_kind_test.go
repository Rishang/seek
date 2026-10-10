package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rishang/seek/config"
)

// newsSearch is a fakeSearch that also runs news searches.
type newsSearch struct{ fakeSearch }

func (newsSearch) SupportsKind(kind string) bool { return kind == config.KindNews }

func TestAutoSearchSkipsProvidersWithoutKind(t *testing.T) {
	a := newAutoSearch([]autoSearchEntry{
		{"web-only", fakeSearch{name: "web-only", results: hit(3)}},
		{"news", newsSearch{fakeSearch{name: "news", results: hit(1)}}},
	})
	if !a.SupportsKind(config.KindNews) || a.SupportsKind(config.KindImages) {
		t.Fatal("chain kind support should reflect its members")
	}
	res, err := a.Search(context.Background(), "q", config.SearchOptions{Kind: config.KindNews})
	if err != nil || len(res) != 1 {
		t.Fatalf("want 1 news result, got %d (err %v)", len(res), err)
	}
	if at := a.Attempts(); len(at) != 1 || at[0].Provider != "news" {
		t.Fatalf("web-only provider should be skipped, not attempted: %+v", at)
	}
}

func TestBraveImagesMapsImageURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/res/v1/images/search" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"results":[{"title":"Cat","url":"https://page","properties":{"url":"https://img.jpg"}}]}`))
	}))
	defer srv.Close()
	old := braveBase
	braveBase = srv.URL
	defer func() { braveBase = old }()

	res, err := NewBraveProvider(config.ProviderConfig{APIKey: "k"}).
		Search(context.Background(), "cat", config.SearchOptions{Kind: config.KindImages})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].URL != "https://page" || res[0].ImageURL != "https://img.jpg" {
		t.Fatalf("bad mapping: %+v", res)
	}
}

func TestTavilyImageAcceptsStringOrObject(t *testing.T) {
	var imgs []tvImage
	if err := json.Unmarshal([]byte(`["https://a.png",{"url":"https://b.png","description":"B"}]`), &imgs); err != nil {
		t.Fatal(err)
	}
	if imgs[0].URL != "https://a.png" || imgs[1].URL != "https://b.png" || imgs[1].Description != "B" {
		t.Fatalf("bad decode: %+v", imgs)
	}
}
