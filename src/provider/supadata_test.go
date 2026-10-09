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
		case "/":
			w.WriteHeader(http.StatusAccepted)
			w.Write([]byte(`{"jobId":"j1"}`))
		case "/j1":
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
