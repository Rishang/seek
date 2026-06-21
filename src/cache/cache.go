// Package cache provides transparent caching of provider search, fetch, and
// crawl results, backed by a local SQLite database.
package cache

import (
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

// DefaultPath returns the default SQLite database location (~/.seek/cache.db).
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "seek-cache.db" // last resort: current directory
	}
	return filepath.Join(home, ".seek", "cache.db")
}
