// Package cache provides transparent caching of provider search, fetch, and
// crawl results. It ships two interchangeable backends: a local SQLite
// database (the default) and an S3-compatible object store.
package cache

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// DefaultTTL is how long a cached entry stays valid when no TTL is given.
const DefaultTTL = 15 * 24 * time.Hour

// Key uniquely identifies a cached result. URL holds the search query for
// search operations; Format is empty when not applicable.
type Key struct {
	Op       string // "search" | "fetch" | "crawl"
	Provider string
	URL      string
	Format   string
}

// Entry is a stored result.
type Entry struct {
	Content string
	Format  string
}

// Store is a cache backend. Implementations must be safe for concurrent use
// and should treat expiry lazily: a Get past the entry's TTL reports a miss.
type Store interface {
	Get(ctx context.Context, k Key) (Entry, bool, error)
	Set(ctx context.Context, k Key, content string, ttl time.Duration) error
	Close() error
}

// DefaultPath returns the default SQLite database location (~/.seek/cache.db).
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "seek-cache.db" // last resort: current directory
	}
	return filepath.Join(home, ".seek", "cache.db")
}
