package app

import (
	"fmt"

	"github.com/rivo/tview"
)

// StatusBar renders the top bar: connection name, store-category tag,
// status dot, and tunnel address when applicable (spec §5.1: "the status
// bar always includes the store category tag ... so it's clear which mode
// you're in without opening the browser").
type StatusBar struct {
	View *tview.TextView
}

func NewStatusBar() *StatusBar {
	tv := tview.NewTextView().SetDynamicColors(true)
	tv.SetText("[gray]no connection[white]")
	return &StatusBar{View: tv}
}

// Render sets the status bar text for the given active connection state.
func (b *StatusBar) Render(ac *ActiveConnection) {
	b.View.SetText(FormatStatusBar(ac))
}

func statusColorLabel(s ConnStatus) (color, label string) {
	switch s {
	case StatusConnected:
		return "green", "connected"
	case StatusError:
		return "red", "error"
	case StatusTunnelConnecting, StatusDBConnecting, StatusTunnelUp:
		return "yellow", "connecting"
	default:
		return "gray", "disconnected"
	}
}

// FormatStatusBar is the pure formatting function behind the status bar's
// displayed text, independently testable without a tview.Application.
// Square brackets around the store-category tag are doubled ("[[postgres]")
// per tview's escaping convention, since dynamic color tags are enabled on
// the underlying TextView and a bare "[postgres]" would otherwise be parsed
// as an (invalid) color tag rather than shown literally.
func FormatStatusBar(ac *ActiveConnection) string {
	if ac == nil || ac.Status == StatusDisconnected {
		return "[gray]no connection[white]"
	}

	color, label := statusColorLabel(ac.Status)
	text := fmt.Sprintf("conn: %s [[%s] [%s]●[white] %s", ac.Conn.Name, ac.Conn.Type, color, label)
	if ac.TunnelProc != nil {
		text += fmt.Sprintf(" | tunnel: localhost:%d", ac.TunnelProc.LocalPort)
	}
	if ac.Status == StatusError && ac.LastError != nil {
		text += fmt.Sprintf(" | [red]%s[white]", ac.LastError.Error())
	}
	return text
}
