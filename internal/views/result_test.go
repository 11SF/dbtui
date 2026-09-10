package views

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"dbtui/internal/db"
)

// TestResultView_SetResult_MakesTableSelectable guards the "cursor stuck on
// the tree, no way to see the table rows" bug: without SetSelectable(true,
// true), the result grid renders but has no visible/movable cursor and
// GetSelection() is meaningless for y/Y yank.
func TestResultView_SetResult_MakesTableSelectable(t *testing.T) {
	r := NewResultView()
	if rows, cols := r.Table.GetSelectable(); rows || cols {
		t.Fatalf("before SetResult: GetSelectable() = (%v, %v), want (false, false)", rows, cols)
	}

	r.SetResult(&db.QueryResult{Columns: []string{"id"}, Rows: [][]any{{1}, {2}}})

	rows, cols := r.Table.GetSelectable()
	if !rows || !cols {
		t.Fatalf("after SetResult: GetSelectable() = (%v, %v), want (true, true)", rows, cols)
	}
}

// TestResultView_SetDocResult_NotSelectable confirms the document/raw render
// path deliberately stays non-selectable (a single pretty-printed text
// block, not a row/column grid — selectable mode there would have arrow
// keys hunting for a selectable cell that mostly doesn't exist).
func TestResultView_SetDocResult_NotSelectable(t *testing.T) {
	r := NewResultView()
	r.SetResult(&db.QueryResult{Columns: []string{"id"}, Rows: [][]any{{1}}})
	r.SetDocResult(&db.DocResult{Documents: []string{"{}"}})

	if rows, cols := r.Table.GetSelectable(); rows || cols {
		t.Fatalf("after SetDocResult: GetSelectable() = (%v, %v), want (false, false)", rows, cols)
	}
}

// TestResultView_TabInvokesOnLeave guards the "focus never leaves the
// result pane" half of the same bug: browser.go/query.go rely on Tab
// calling OnLeave to hand focus back to the tree/input, since Esc is
// reserved globally for page-level back navigation and can't do this.
func TestResultView_TabInvokesOnLeave(t *testing.T) {
	r := NewResultView()
	var left bool
	r.OnLeave = func() { left = true }

	handler := r.GetInputCapture()
	if handler == nil {
		t.Fatal("ResultView has no input capture set")
	}
	if ret := handler(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone)); ret != nil {
		t.Errorf("Tab keypress: want event consumed (nil returned), got %v", ret)
	}
	if !left {
		t.Error("Tab keypress did not invoke OnLeave")
	}
}

// TestResultView_TabWithoutOnLeave_NoPanic: OnLeave is optional (e.g. any
// future embedder that doesn't wire it) — Tab must still be handled safely.
func TestResultView_TabWithoutOnLeave_NoPanic(t *testing.T) {
	r := NewResultView()
	handler := r.GetInputCapture()
	handler(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
}
