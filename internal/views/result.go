package views

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"dbtui/internal/db"
)

// ResultView renders either a tabular QueryResult (SQL/KV) or a DocResult
// (Mongo) — a shared component pushed by the browser/query views, not a
// standalone page (spec §5.7).
type ResultView struct {
	*tview.Table
	lastResult *db.QueryResult
	lastDocs   *db.DocResult

	// OnStatus, if set, receives a one-line status message (e.g. "yanked
	// row", "exported to ...") after a y/Y/e keypress, so the embedding
	// page can show it (typically in its footer). Optional — nil means
	// yank/export happen silently.
	OnStatus func(string)

	// OnLeave, if set, is called on Tab so the embedding page (browser.go's
	// tree, query.go's input) can move focus back to itself — ResultView is
	// otherwise a dead end once focused, since Esc is reserved globally for
	// page-level back navigation (app.globalInputCapture), not pane-local
	// focus switching.
	OnLeave func()
}

func NewResultView() *ResultView {
	r := &ResultView{Table: tview.NewTable().SetFixed(1, 0)}
	r.SetInputCapture(r.handleKey)
	return r
}

func (r *ResultView) handleKey(event *tcell.EventKey) *tcell.EventKey {
	if event.Key() == tcell.KeyTab {
		if r.OnLeave != nil {
			r.OnLeave()
		}
		return nil
	}
	switch event.Rune() {
	case 'y':
		r.yankCell()
		return nil
	case 'Y':
		r.yankRow()
		return nil
	case 'e':
		r.export()
		return nil
	}
	return event
}

func (r *ResultView) notify(msg string) {
	if r.OnStatus != nil {
		r.OnStatus(msg)
	}
}

func (r *ResultView) yankCell() {
	row, col := r.GetSelection()
	if r.lastResult != nil && row >= 1 && row-1 < len(r.lastResult.Rows) {
		cells := r.lastResult.Rows[row-1]
		if col >= 0 && col < len(cells) {
			_ = clipboard.WriteAll(YankCell(cells[col]))
			r.notify("yanked cell")
			return
		}
	}
	cell := r.GetCell(row, col)
	if cell != nil {
		_ = clipboard.WriteAll(cell.Text)
		r.notify("yanked cell")
	}
}

func (r *ResultView) yankRow() {
	row, _ := r.GetSelection()
	if r.lastResult != nil && row >= 1 && row-1 < len(r.lastResult.Rows) {
		_ = clipboard.WriteAll(YankRow(r.lastResult.Rows[row-1]))
		r.notify("yanked row")
	}
}

// exportDir returns (creating if necessary) the directory exports are
// written to: <user config dir>/dbtui/exports.
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

func (r *ResultView) export() {
	dir, err := exportDir()
	if err != nil {
		r.notify(fmt.Sprintf("export failed: %v", err))
		return
	}
	stamp := time.Now().Format("20060102-150405")

	switch {
	case r.lastResult != nil:
		path := filepath.Join(dir, "export-"+stamp+".csv")
		if err := os.WriteFile(path, []byte(ExportCSV(r.lastResult)), 0o600); err != nil {
			r.notify(fmt.Sprintf("export failed: %v", err))
			return
		}
		r.notify("exported to " + path)
	case r.lastDocs != nil:
		path := filepath.Join(dir, "export-"+stamp+".jsonl")
		if err := os.WriteFile(path, []byte(ExportJSONL(compactJSONLines(r.lastDocs.Documents))), 0o600); err != nil {
			r.notify(fmt.Sprintf("export failed: %v", err))
			return
		}
		r.notify("exported to " + path)
	default:
		r.notify("nothing to export")
	}
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

// SetResult renders a tabular QueryResult: header row (bold) fixed via
// SetFixed(1, 0) so it never scrolls away, then one row per record.
func (r *ResultView) SetResult(res *db.QueryResult) {
	r.lastResult = res
	r.lastDocs = nil
	r.Table.Clear()
	// Selectable rows+columns so a cursor is visible and y/Y yank the cell/
	// row actually under it (GetSelection() is meaningless without this).
	r.Table.SetSelectable(true, true)
	if res == nil {
		return
	}

	for col, name := range res.Columns {
		cell := tview.NewTableCell(name).SetAttributes(tcell.AttrBold).SetSelectable(false)
		r.Table.SetCell(0, col, cell)
	}
	for rowIdx, row := range res.Rows {
		for col, val := range row {
			r.Table.SetCell(rowIdx+1, col, tview.NewTableCell(fmt.Sprintf("%v", val)))
		}
	}
}

// docDivider separates consecutive documents in the document result view
// (spec §5.7: "document data doesn't fit a flat row/column grid ... divider
// line between them"). Exported so the exact separator is documented and
// test-asserted in one place (test spec §7.3).
const docDivider = "\n---\n"

// FormatDocResult turns a DocResult into the divider-separated display text
// used by SetDocResult — pure so it's testable without tview (test spec
// §7.3): all documents present, in order, separated by docDivider, no
// truncation.
func FormatDocResult(res *db.DocResult) string {
	if res == nil {
		return ""
	}
	return strings.Join(res.Documents, docDivider)
}

// SetDocResult renders a document result as a scrollable text block: one
// pretty-printed JSON document per entry, divider between them (spec §5.7).
// Backed by a single-cell Table so ResultView keeps one consistent
// underlying primitive.
func (r *ResultView) SetDocResult(res *db.DocResult) {
	r.lastDocs = res
	r.lastResult = nil
	r.Table.Clear()
	r.Table.SetSelectable(false, false)
	cell := tview.NewTableCell(FormatDocResult(res)).SetSelectable(false)
	r.Table.SetCell(0, 0, cell)
}

// SetRaw renders arbitrary non-tabular output (e.g. a Redis RunCommand
// result, which is a raw `any`, not a QueryResult/DocResult) as a single
// text block — the KV-mode counterpart to SetResult/SetDocResult.
func (r *ResultView) SetRaw(text string) {
	r.lastResult = nil
	r.lastDocs = nil
	r.Table.Clear()
	r.Table.SetSelectable(false, false)
	r.Table.SetCell(0, 0, tview.NewTableCell(text).SetSelectable(false))
}

// ResultFooter formats the footer text shown below a tabular result:
// "%d rows • %dms%s", appending a cap note when the store's auto-limit (or
// equivalent cap) kicked in (spec §5.7).
func ResultFooter(rowCount int, durationMs int64, capped bool) string {
	footer := fmt.Sprintf("%d rows • %dms", rowCount, durationMs)
	if capped {
		footer += " (capped at 1000)"
	}
	return footer
}

// DocResultFooter is ResultFooter's document-result counterpart: document
// count instead of row count, and the equivalent "(capped at N)" note keyed
// to the effective Find() limit that was applied.
func DocResultFooter(docCount int, durationMs int64, limit int) string {
	footer := fmt.Sprintf("%d docs • %dms", docCount, durationMs)
	if docCount >= limit {
		footer += fmt.Sprintf(" (capped at %d)", limit)
	}
	return footer
}

// YankCell renders a single cell value for the clipboard (spec §5.7: "y =
// yank cell").
func YankCell(v any) string {
	return fmt.Sprintf("%v", v)
}

// YankRow renders a full row as tab-separated text for the clipboard (spec
// §5.7: "Y = yank row (tab-separated)").
func YankRow(row []any) string {
	parts := make([]string, len(row))
	for i, v := range row {
		parts[i] = fmt.Sprintf("%v", v)
	}
	return strings.Join(parts, "\t")
}

// ExportCSV renders a QueryResult as CSV text (spec §5.7: "e = export: CSV
// for SQL result"). Values containing a comma, quote, or newline are
// quoted per RFC 4180; embedded quotes are doubled.
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

// ExportJSONL renders a DocResult as newline-delimited JSON, one compact
// line per document (spec §5.7: ".jsonl for document result"). Each input
// document is already pretty-printed JSON; ExportJSONL only needs to strip
// its internal newlines/indentation down to one line, which callers do via
// CompactJSONLine before calling this — kept here as a pure join so the
// "one line per doc, \n separated" contract is explicit and tested.
func ExportJSONL(lines []string) string {
	return strings.Join(lines, "\n")
}
