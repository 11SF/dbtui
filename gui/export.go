package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dbtui/internal/db"
)

// YankCell renders a single cell value for the clipboard.
func YankCell(v any) string {
	return fmt.Sprintf("%v", v)
}

// YankRow renders a full row as tab-separated text for the clipboard.
func YankRow(row []any) string {
	parts := make([]string, len(row))
	for i, v := range row {
		parts[i] = fmt.Sprintf("%v", v)
	}
	return strings.Join(parts, "\t")
}

// ExportCSV renders a QueryResult as CSV text. Values containing a comma,
// quote, or newline are quoted per RFC 4180; embedded quotes are doubled.
func ExportCSV(res *db.QueryResult) string {
	if res == nil {
		return ""
	}
	var b strings.Builder
	writeCSVRow(&b, res.Columns)
	for _, row := range res.Rows {
		strs := make([]string, len(row))
		for i, v := range row {
			strs[i] = fmt.Sprintf("%v", v)
		}
		writeCSVRow(&b, strs)
	}
	return b.String()
}

func writeCSVRow(b *strings.Builder, fields []string) {
	for i, f := range fields {
		if i > 0 {
			b.WriteByte(',')
		}
		if strings.ContainsAny(f, ",\"\n") {
			b.WriteByte('"')
			b.WriteString(strings.ReplaceAll(f, `"`, `""`))
			b.WriteByte('"')
		} else {
			b.WriteString(f)
		}
	}
	b.WriteByte('\n')
}

// ExportJSONL renders newline-delimited JSON, one line per input string.
func ExportJSONL(lines []string) string {
	return strings.Join(lines, "\n")
}

// compactJSONLines collapses each pretty-printed document string down to
// one line, since .jsonl requires exactly one JSON value per line.
func compactJSONLines(docs []string) []string {
	out := make([]string, len(docs))
	for i, d := range docs {
		out[i] = strings.Join(strings.Fields(d), " ")
	}
	return out
}

// exportDir returns (creating if necessary) the directory exports are
// written to: <user config dir>/dbtui/exports — same convention the TUI used.
func exportDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	out := filepath.Join(dir, "dbtui", "exports")
	if err := os.MkdirAll(out, 0o700); err != nil {
		return "", err
	}
	return out, nil
}

// ExportResultCSV writes the most recent RunQuery result as CSV and returns
// its path.
func (a *App) ExportResultCSV() (string, error) {
	a.mu.Lock()
	res := a.lastResult
	a.mu.Unlock()
	if res == nil {
		return "", fmt.Errorf("gui: nothing to export")
	}

	dir, err := exportDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "export-"+time.Now().Format("20060102-150405")+".csv")
	if err := os.WriteFile(path, []byte(ExportCSV(res)), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// ExportDocResultJSONL writes the most recent Find result as .jsonl and
// returns its path.
func (a *App) ExportDocResultJSONL() (string, error) {
	a.mu.Lock()
	res := a.lastDocs
	a.mu.Unlock()
	if res == nil {
		return "", fmt.Errorf("gui: nothing to export")
	}

	dir, err := exportDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "export-"+time.Now().Format("20060102-150405")+".jsonl")
	if err := os.WriteFile(path, []byte(ExportJSONL(compactJSONLines(res.Documents))), 0o600); err != nil {
		return "", err
	}
	return path, nil
}
