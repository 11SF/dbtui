package history

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "history.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestHistoryStore_InsertAndList(t *testing.T) {
	s := openTestStore(t)
	base := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	entries := []*HistoryEntry{
		{Timestamp: base, ConnName: "pg-dev", Query: "SELECT 1", DurationMs: 5, RowCount: 1},
		{Timestamp: base.Add(1 * time.Minute), ConnName: "pg-dev", Query: "SELECT 2", DurationMs: 6, RowCount: 1},
		{Timestamp: base.Add(2 * time.Minute), ConnName: "pg-dev", Query: "SELECT 3", DurationMs: 7, RowCount: 1},
	}
	for _, e := range entries {
		if err := s.Insert(e); err != nil {
			t.Fatalf("Insert() error = %v", err)
		}
	}

	got, err := s.List(ListOptions{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("List() returned %d entries, want 3", len(got))
	}
	wantOrder := []string{"SELECT 3", "SELECT 2", "SELECT 1"}
	for i, w := range wantOrder {
		if got[i].Query != w {
			t.Fatalf("List()[%d].Query = %q, want %q (order should be timestamp DESC)", i, got[i].Query, w)
		}
	}
}

func TestHistoryStore_List_RespectsLimit(t *testing.T) {
	s := openTestStore(t)
	base := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	for i := 0; i < 10; i++ {
		e := &HistoryEntry{
			Timestamp:  base.Add(time.Duration(i) * time.Minute),
			ConnName:   "pg-dev",
			Query:      fmt.Sprintf("SELECT %d", i),
			DurationMs: 1,
			RowCount:   1,
		}
		if err := s.Insert(e); err != nil {
			t.Fatalf("Insert() error = %v", err)
		}
	}

	got, err := s.List(ListOptions{Limit: 5})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("List(limit=5) returned %d entries, want 5", len(got))
	}
	// most recent 5: SELECT 9..SELECT 5, descending
	for i, want := range []string{"SELECT 9", "SELECT 8", "SELECT 7", "SELECT 6", "SELECT 5"} {
		if got[i].Query != want {
			t.Fatalf("List(limit=5)[%d].Query = %q, want %q", i, got[i].Query, want)
		}
	}
}

func TestHistoryStore_FilterByConnName(t *testing.T) {
	s := openTestStore(t)
	base := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	entries := []*HistoryEntry{
		{Timestamp: base, ConnName: "pg-dev", Query: "SELECT 1", DurationMs: 1, RowCount: 1},
		{Timestamp: base.Add(time.Minute), ConnName: "mysql-staging", Query: "SELECT 2", DurationMs: 1, RowCount: 1},
		{Timestamp: base.Add(2 * time.Minute), ConnName: "pg-dev", Query: "SELECT 3", DurationMs: 1, RowCount: 1},
	}
	for _, e := range entries {
		if err := s.Insert(e); err != nil {
			t.Fatalf("Insert() error = %v", err)
		}
	}

	got, err := s.List(ListOptions{ConnName: "pg-dev"})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List(conn=pg-dev) returned %d entries, want 2", len(got))
	}
	for _, e := range got {
		if e.ConnName != "pg-dev" {
			t.Fatalf("List(conn=pg-dev) returned entry for %q", e.ConnName)
		}
	}
}

func TestHistoryStore_StoresErrorField(t *testing.T) {
	s := openTestStore(t)

	e := &HistoryEntry{
		Timestamp:  time.Now(),
		ConnName:   "pg-dev",
		Query:      "SELECT * FROM missing_table",
		DurationMs: 3,
		RowCount:   0,
		Error:      "relation \"missing_table\" does not exist",
	}
	if err := s.Insert(e); err != nil {
		t.Fatalf("Insert() error = %v", err)
	}

	got, err := s.List(ListOptions{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("List() returned %d entries, want 1", len(got))
	}
	if got[0].Error != e.Error {
		t.Fatalf("Error = %q, want %q", got[0].Error, e.Error)
	}
	if got[0].RowCount != 0 {
		t.Fatalf("RowCount = %d, want 0", got[0].RowCount)
	}
}

func TestHistoryStore_SchemaCreatedIfMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer s.Close()

	var tableName string
	err = s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='history'`).Scan(&tableName)
	if err != nil {
		t.Fatalf("history table not found: %v", err)
	}

	var indexName string
	err = s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='index' AND name='idx_history_timestamp'`).Scan(&indexName)
	if err != nil {
		t.Fatalf("idx_history_timestamp index not found: %v", err)
	}
}
