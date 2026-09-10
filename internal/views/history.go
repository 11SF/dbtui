package views

import (
	"fmt"

	"github.com/rivo/tview"

	"dbtui/internal/app"
	"dbtui/internal/history"
)

// previewMaxRunes is the truncation length for a history-list query/command
// preview (spec §5.8: "truncated to 60 chars"). Measured in runes, not
// bytes, so a multi-byte character is never split (test spec §7.5).
const previewMaxRunes = 60

// TruncatePreview truncates s to at most previewMaxRunes runes, appending
// "..." when truncated. Rune-safe: never splits a multi-byte UTF-8
// character (tested with Thai text, per test spec §7.5).
func TruncatePreview(s string) string {
	runes := []rune(s)
	if len(runes) <= previewMaxRunes {
		return s
	}
	return string(runes[:previewMaxRunes]) + "..."
}

// HistoryRow formats one history-list row: "timestamp | conn | duration |
// rows | query/command preview" (spec §5.8).
func HistoryRow(e history.HistoryEntry) string {
	return fmt.Sprintf("%s | %s | %dms | %d | %s",
		e.Timestamp.Format("2006-01-02 15:04:05"), e.ConnName, e.DurationMs, e.RowCount, TruncatePreview(e.Query))
}

// FilterHistory returns entries whose ConnName or Query contains substr
// (case-sensitive client-side filter per spec §5.8's "/" filter).
func FilterHistory(entries []history.HistoryEntry, substr string) []history.HistoryEntry {
	if substr == "" {
		return entries
	}
	var out []history.HistoryEntry
	for _, e := range entries {
		if containsFold(e.ConnName, substr) || containsFold(e.Query, substr) {
			out = append(out, e)
		}
	}
	return out
}

func containsFold(s, substr string) bool {
	sr, subr := []rune(s), []rune(substr)
	if len(subr) == 0 {
		return true
	}
	for i := 0; i+len(subr) <= len(sr); i++ {
		match := true
		for j, r := range subr {
			if toLower(sr[i+j]) != toLower(r) {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func toLower(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + ('a' - 'A')
	}
	return r
}

// HistoryView is the :history page (spec §5.8): reloads from sqlite each
// time the view is entered, supports a client-side "/" filter over the
// loaded list.
type HistoryView struct {
	*tview.Table
	app     *app.App
	store   *history.Store
	entries []history.HistoryEntry
}

func NewHistoryView(a *app.App, store *history.Store) *HistoryView {
	v := &HistoryView{Table: tview.NewTable().SetSelectable(true, false), app: a, store: store}
	v.Refresh()
	return v
}

// Refresh reloads history entries from sqlite and redraws the table.
func (v *HistoryView) Refresh() {
	entries, err := v.store.List(history.ListOptions{})
	if err != nil {
		v.app.Logger.Log("history: refresh: " + err.Error())
		return
	}
	v.entries = entries
	v.render(entries)
}

// Filter re-renders the currently loaded entries filtered by substr,
// without reloading from sqlite (spec §5.8: "/ filter (client-side on the
// loaded list)").
func (v *HistoryView) Filter(substr string) {
	v.render(FilterHistory(v.entries, substr))
}

func (v *HistoryView) render(entries []history.HistoryEntry) {
	v.Table.Clear()
	for i, e := range entries {
		v.Table.SetCell(i, 0, tview.NewTableCell(HistoryRow(e)))
	}
}
