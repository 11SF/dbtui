package main

import (
	"strings"
	"testing"

	"dbtui/internal/db"
)

func TestExportCSV(t *testing.T) {
	res := &db.QueryResult{
		Columns: []string{"id", "name"},
		Rows: [][]any{
			{1, "plain"},
			{2, "has,comma"},
			{3, "has\"quote"},
		},
	}
	got := ExportCSV(res)
	want := "id,name\n1,plain\n2,\"has,comma\"\n3,\"has\"\"quote\"\n"
	if got != want {
		t.Fatalf("ExportCSV() =\n%q\nwant\n%q", got, want)
	}
}

func TestExportCSV_Nil(t *testing.T) {
	if got := ExportCSV(nil); got != "" {
		t.Fatalf("ExportCSV(nil) = %q, want empty", got)
	}
}

func TestExportJSONL(t *testing.T) {
	got := ExportJSONL([]string{`{"a":1}`, `{"b":2}`})
	want := "{\"a\":1}\n{\"b\":2}"
	if got != want {
		t.Fatalf("ExportJSONL() = %q, want %q", got, want)
	}
}

func TestCompactJSONLines(t *testing.T) {
	docs := []string{"{\n  \"a\": 1\n}"}
	got := compactJSONLines(docs)
	if len(got) != 1 || strings.Contains(got[0], "\n") {
		t.Fatalf("compactJSONLines() = %q, want a single line with no newlines", got)
	}
}

func TestYankCellAndRow(t *testing.T) {
	if got := YankCell(42); got != "42" {
		t.Errorf("YankCell(42) = %q", got)
	}
	if got := YankRow([]any{1, "x", true}); got != "1\tx\ttrue" {
		t.Errorf("YankRow(...) = %q", got)
	}
}

func TestExportResultCSV_NothingToExport(t *testing.T) {
	a, _ := newTestApp(t)
	if _, err := a.ExportResultCSV(); err == nil {
		t.Fatal("want error when no query has been run yet")
	}
}

func TestExportDocResultJSONL_NothingToExport(t *testing.T) {
	a, _ := newTestApp(t)
	if _, err := a.ExportDocResultJSONL(); err == nil {
		t.Fatal("want error when no find has been run yet")
	}
}
