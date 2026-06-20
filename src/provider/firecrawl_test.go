package provider

import (
	"encoding/json"
	"testing"
)

// Firecrawl v2 /v2/search nests results under a source key (web/news/images),
// not a flat array. Guards against regressing to the v1 []fcSearchItem shape.
func TestFirecrawlSearchResponseDecode(t *testing.T) {
	const body = `{
		"success": true,
		"data": {
			"web": [
				{"title": "T", "description": "D", "url": "https://example.com"}
			],
			"images": [],
			"news": []
		}
	}`

	var resp fcSearchResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data.Web) != 1 {
		t.Fatalf("web items: want 1, got %d", len(resp.Data.Web))
	}
	got := resp.Data.Web[0]
	if got.Title != "T" || got.Description != "D" || got.URL != "https://example.com" {
		t.Fatalf("web item: got %+v", got)
	}
}
