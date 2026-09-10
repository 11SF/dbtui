package views

import (
	"strings"

	"github.com/rivo/tview"

	"dbtui/internal/logging"
)

// LogsView is a read-only view of the ring-buffer logger (spec §5.9).
type LogsView struct {
	*tview.TextView
	logger *logging.RingBuffer
}

func NewLogsView(logger *logging.RingBuffer) *LogsView {
	v := &LogsView{
		TextView: tview.NewTextView().SetScrollable(true),
		logger:   logger,
	}
	v.Refresh()
	return v
}

// Refresh redraws the log lines. Auto-scrolls to bottom (spec §5.9) unless
// the caller has manually scrolled up — that decision is made by the
// caller via ShouldAutoScroll, since it depends on tview scroll-offset
// state this package doesn't own.
func (v *LogsView) Refresh() {
	v.TextView.SetText(strings.Join(v.logger.Lines(), "\n"))
}

// ShouldAutoScroll reports whether the log view should jump to the bottom
// on a new line: true unless the user has manually scrolled away from the
// bottom (spec §5.9: "unless the user has scrolled up manually — check
// GetScrollOffset"). atBottom is the caller's own comparison of the current
// scroll offset against the maximum.
func ShouldAutoScroll(userScrolledUp bool) bool {
	return !userScrolledUp
}
