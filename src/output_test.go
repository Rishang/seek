package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rishang/seek/config"
)

func TestRenderSearchCSV(t *testing.T) {
	results := []config.SearchResult{
		{Title: "First", URL: "https://a.example", Snippet: "alpha"},
		{Title: "Comma, quote\"", URL: "https://b.example", Snippet: "line\nbreak"},
	}
	var buf bytes.Buffer
	if err := renderTo(&buf, "csv", results); err != nil {
		t.Fatalf("renderTo: %v", err)
	}
	out := buf.String()
	if !strings.HasPrefix(out, "title,url,snippet\n") {
		t.Fatalf("missing header: %q", out)
	}
	if !strings.Contains(out, "First,https://a.example,alpha") {
		t.Fatalf("missing first row: %q", out)
	}
	// Fields with commas, quotes, and newlines must be quoted/escaped.
	if !strings.Contains(out, "\"Comma, quote\"\"\"") {
		t.Fatalf("special chars not escaped: %q", out)
	}
}

func TestRenderDefaultsToJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := renderTo(&buf, "", &config.ScrapeResult{URL: "u", Content: "c", Format: "markdown"}); err != nil {
		t.Fatalf("renderTo: %v", err)
	}
	if !strings.Contains(buf.String(), `"content": "c"`) {
		t.Fatalf("expected JSON output, got %q", buf.String())
	}
}
