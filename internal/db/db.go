// Package db provides a SQLite-backed repository for the nstream media library.
// It uses modernc.org/sqlite (CGO-free) and manages schema creation automatically.
package db

import (
	"database/sql"
	_ "embed"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

// DB wraps a *sql.DB with convenience helpers.
type DB struct {
	*sql.DB
}

// Open opens (or creates) the SQLite database at the given path and applies the
// schema. WAL mode and foreign-key enforcement are enabled automatically.
func Open(path string) (*DB, error) {
	sqldb, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("db open: %w", err)
	}
	sqldb.SetMaxOpenConns(1) // SQLite is single-writer
	sqldb.SetConnMaxLifetime(0)
	sqldb.SetMaxIdleConns(1)

	if _, err := sqldb.Exec(schema); err != nil {
		_ = sqldb.Close()
		return nil, fmt.Errorf("db migrate: %w", err)
	}

	// Idempotent column additions for existing databases.
	colMigrations := []string{
		`ALTER TABLE videos ADD COLUMN media_id   INTEGER REFERENCES media_items(id)`,
		`ALTER TABLE videos ADD COLUMN episode_id INTEGER REFERENCES tv_episodes(id)`,
		`ALTER TABLE media_items ADD COLUMN metadata_language TEXT NOT NULL DEFAULT 'en-US'`,
		// Index on media_id is created here (after the column exists) so it works on both
		// fresh installs and upgrades from older schema versions.
		`CREATE INDEX IF NOT EXISTS idx_videos_media ON videos(media_id)`,
	}
	for _, m := range colMigrations {
		_, _ = sqldb.Exec(m) // ignore "duplicate column" / "already exists" errors
	}

	return &DB{sqldb}, nil
}

// nullTime converts *time.Time to sql.NullTime.
func nullTime(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *t, Valid: true}
}

// nullString converts a string to sql.NullString, treating "" as NULL.
func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}
