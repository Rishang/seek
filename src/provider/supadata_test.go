package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rishang/seek/config"
)

// Long videos return 202 + jobId; Transcribe must poll until completed.
func TestSupadataTranscribePollsJob(t *testing.T) {
	polls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "k" {
			t.Errorf("missing x-api-key header")
		}
		switch r.URL.Path {
		case "/transcript":
			w.WriteHeader(http.StatusAccepted)
			w.Write([]byte(`{"jobId":"j1"}`))
		case "/transcript/j1":
			polls++
			if polls < 2 {
				w.Write([]byte(`{"status":"active"}`))
				return
			}
			w.Write([]byte(`{"status":"completed","content":"hello world","lang":"en"}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	oldBase, oldPoll := supadataBase, supadataPollInterval
	supadataBase, supadataPollInterval = srv.URL, time.Millisecond
	defer func() { supadataBase, supadataPollInterval = oldBase, oldPoll }()

	p := NewSupadataProvider(config.ProviderConfig{APIKey: "k"})
	got, err := p.Transcribe(context.Background(), "https://youtu.be/x", "")
	if err != nil {
		t.Fatalf("transcribe: %v", err)
	}
	if got.Content != "hello world" || polls != 2 {
		t.Fatalf("got %q after %d polls", got.Content, polls)
	}
}

// Crawl polls while scraping, then follows the "next" pagination URL.
func TestSupadataCrawlPollsAndPaginates(t *testing.T) {
	var srv *httptest.Server
	polls := 0
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/web/crawl":
			w.Write([]byte(`{"jobId":"c1"}`))
		case r.URL.Path == "/web/crawl/c1" && r.URL.Query().Get("skip") == "":
			polls++
			if polls < 2 {
				w.Write([]byte(`{"status":"scraping"}`))
				return
			}
			w.Write([]byte(`{"status":"completed","pages":[{"url":"https://a","content":"A"}],"next":"` + srv.URL + `/web/crawl/c1?skip=1"}`))
		case r.URL.Path == "/web/crawl/c1":
			w.Write([]byte(`{"status":"completed","pages":[{"url":"https://b","content":"B"}]}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
		}
	}))
	defer srv.Close()

	oldBase, oldPoll := supadataBase, supadataPollInterval
	supadataBase, supadataPollInterval = srv.URL, time.Millisecond
	defer func() { supadataBase, supadataPollInterval = oldBase, oldPoll }()

	got, err := NewSupadataProvider(config.ProviderConfig{APIKey: "k"}).Crawl(context.Background(), "https://a")
	if err != nil {
		t.Fatalf("crawl: %v", err)
	}
	if len(got.Pages) != 2 || got.Pages[1] != "https://b" || got.Content != "A\n\nB\n\n" {
		t.Fatalf("got %+v", got)
	}
}
