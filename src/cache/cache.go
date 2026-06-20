// Package cache provides transparent caching of provider search, fetch, and
// crawl results. The default backend is SQLite; the Store interface allows
// other backends (e.g. S3) to be added without touching callers.
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

// Entry is a stored result together with its caching metadata.
type Entry struct {
	Timestamp time.Time
	URL       string
	Provider  string
	Content   string
	Format    string
	TTL       time.Duration
}

// Store persists and retrieves cache entries. Implementations must be safe for
// concurrent use.
type Store interface {
	// Get returns the entry for k. ok is false on a miss or when the entry has
	// expired (an expired entry should be treated as absent).
	Get(ctx context.Context, k Key) (entry Entry, ok bool, err error)
	// Set stores content for k with the given TTL.
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
