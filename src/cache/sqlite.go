package cache

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// sqliteStore is the default Store backend, backed by a local SQLite database.
type sqliteStore struct {
	db *sql.DB
}

const sqliteSchema = `
CREATE TABLE IF NOT EXISTS entries (
	op         TEXT    NOT NULL,
	provider   TEXT    NOT NULL,
	url        TEXT    NOT NULL,
	format     TEXT    NOT NULL,
	content    TEXT    NOT NULL,
	created_at INTEGER NOT NULL, -- unix seconds
	ttl        INTEGER NOT NULL, -- seconds
	PRIMARY KEY (op, provider, url, format)
);`

// OpenSQLite opens (creating if needed) a SQLite-backed Store at path.
func OpenSQLite(path string) (Store, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("cache: create dir: %w", err)
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("cache: open sqlite: %w", err)
	}
	if _, err := db.Exec(sqliteSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("cache: init schema: %w", err)
	}
	return &sqliteStore{db: db}, nil
}

func (s *sqliteStore) Get(ctx context.Context, k Key) (Entry, bool, error) {
	const q = `SELECT content, created_at, ttl FROM entries
		WHERE op = ? AND provider = ? AND url = ? AND format = ?`

	var (
		content   string
		createdAt int64
		ttlSecs   int64
	)
	err := s.db.QueryRowContext(ctx, q, k.Op, k.Provider, k.URL, k.Format).
		Scan(&content, &createdAt, &ttlSecs)
	if err == sql.ErrNoRows {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, fmt.Errorf("cache: get: %w", err)
	}

	created := time.Unix(createdAt, 0)
	ttl := time.Duration(ttlSecs) * time.Second
	if time.Since(created) > ttl {
		// Expired: drop it and report a miss.
		_ = s.delete(ctx, k)
		return Entry{}, false, nil
	}

	return Entry{
		Timestamp: created,
		URL:       k.URL,
		Provider:  k.Provider,
		Content:   content,
		Format:    k.Format,
		TTL:       ttl,
	}, true, nil
}

func (s *sqliteStore) Set(ctx context.Context, k Key, content string, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	const q = `INSERT INTO entries (op, provider, url, format, content, created_at, ttl)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(op, provider, url, format) DO UPDATE SET
			content = excluded.content,
			created_at = excluded.created_at,
			ttl = excluded.ttl`

	_, err := s.db.ExecContext(ctx, q,
		k.Op, k.Provider, k.URL, k.Format, content, time.Now().Unix(), int64(ttl.Seconds()))
	if err != nil {
		return fmt.Errorf("cache: set: %w", err)
	}
	return nil
}

func (s *sqliteStore) delete(ctx context.Context, k Key) error {
	const q = `DELETE FROM entries WHERE op = ? AND provider = ? AND url = ? AND format = ?`
	_, err := s.db.ExecContext(ctx, q, k.Op, k.Provider, k.URL, k.Format)
	return err
}

func (s *sqliteStore) Close() error { return s.db.Close() }
