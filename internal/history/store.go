// Package history stores dbtui's query/command history in a local SQLite
// file, one schema regardless of the active connection's store category.
package history

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// HistoryEntry is one executed query/command, logged the same way whether
// it came from SQL, a Redis command line, or a Mongo filter.
type HistoryEntry struct {
	ID         int64
	Timestamp  time.Time
	ConnName   string
	Query      string
	DurationMs int64
	RowCount   int
	Error      string // empty if successful
}

const schema = `
CREATE TABLE IF NOT EXISTS history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp INTEGER NOT NULL,
    conn_name TEXT NOT NULL,
    query TEXT NOT NULL,
    duration_ms INTEGER NOT NULL,
    row_count INTEGER NOT NULL,
    error TEXT DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_history_timestamp ON history(timestamp DESC);
`

// Store wraps a SQLite-backed history table.
type Store struct {
	db *sql.DB
}

// Open opens (creating if necessary) the SQLite file at path and ensures
// the history schema exists.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("history: open %s: %w", path, err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("history: create schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the underlying database handle.
func (s *Store) Close() error {
	return s.db.Close()
}

// Insert records a new history entry, filling in its ID.
func (s *Store) Insert(e *HistoryEntry) error {
	res, err := s.db.Exec(
		`INSERT INTO history (timestamp, conn_name, query, duration_ms, row_count, error) VALUES (?, ?, ?, ?, ?, ?)`,
		e.Timestamp.UnixNano(), e.ConnName, e.Query, e.DurationMs, e.RowCount, e.Error,
	)
	if err != nil {
		return fmt.Errorf("history: insert: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("history: last insert id: %w", err)
	}
	e.ID = id
	return nil
}

// ListOptions filters/limits a List() call. A zero value lists everything,
// ordered by timestamp DESC.
type ListOptions struct {
	ConnName string // empty = all connections
	Limit    int    // 0 = no limit
}

// List returns history entries ordered by timestamp DESC, most recent
// first, applying opts.ConnName and opts.Limit if set.
func (s *Store) List(opts ListOptions) ([]HistoryEntry, error) {
	query := `SELECT id, timestamp, conn_name, query, duration_ms, row_count, error FROM history`
	var args []any
	if opts.ConnName != "" {
		query += ` WHERE conn_name = ?`
		args = append(args, opts.ConnName)
	}
	query += ` ORDER BY timestamp DESC`
	if opts.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, opts.Limit)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("history: list: %w", err)
	}
	defer rows.Close()

	var entries []HistoryEntry
	for rows.Next() {
		var e HistoryEntry
		var tsNano int64
		if err := rows.Scan(&e.ID, &tsNano, &e.ConnName, &e.Query, &e.DurationMs, &e.RowCount, &e.Error); err != nil {
			return nil, fmt.Errorf("history: scan: %w", err)
		}
		e.Timestamp = time.Unix(0, tsNano)
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
