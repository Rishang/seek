package cache

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) Store {
	t.Helper()
	store, err := OpenSQLite(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestStoreSetGet(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	key := Key{Op: "fetch", Provider: "firecrawl", URL: "https://example.com", Format: "markdown"}

	if _, ok, err := store.Get(ctx, key); ok || err != nil {
		t.Fatalf("expected miss on empty store, got ok=%v err=%v", ok, err)
	}

	if err := store.Set(ctx, key, "hello", time.Hour); err != nil {
		t.Fatalf("Set: %v", err)
	}

	entry, ok, err := store.Get(ctx, key)
	if err != nil || !ok {
		t.Fatalf("expected hit, got ok=%v err=%v", ok, err)
	}
	if entry.Content != "hello" || entry.Format != "markdown" {
		t.Fatalf("unexpected entry: %+v", entry)
	}
}

func TestStoreExpiry(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	key := Key{Op: "search", Provider: "tavily", URL: "golang"}

	// Negative TTL falls back to DefaultTTL on Set, so write a tiny positive TTL
	// and wait it out to exercise the expiry branch.
	if err := store.Set(ctx, key, "data", time.Nanosecond); err != nil {
		t.Fatalf("Set: %v", err)
	}
	time.Sleep(2 * time.Millisecond)

	if _, ok, _ := store.Get(ctx, key); ok {
		t.Fatal("expected expired entry to be reported as a miss")
	}
}

func TestStoreOverwrite(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	key := Key{Op: "crawl", Provider: "spider.cloud", URL: "https://x.test"}

	_ = store.Set(ctx, key, "v1", time.Hour)
	_ = store.Set(ctx, key, "v2", time.Hour)

	entry, ok, _ := store.Get(ctx, key)
	if !ok || entry.Content != "v2" {
		t.Fatalf("expected overwrite to v2, got ok=%v content=%q", ok, entry.Content)
	}
}
