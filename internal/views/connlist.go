package views

import (
	"context"
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"dbtui/internal/app"
	"dbtui/internal/config"
	"dbtui/internal/secrets"
)

// StatusColor maps an app.ConnStatus to the tview color tag substring used
// to render its status dot in the connection list (test spec §7.6):
// connected=green, disconnected=gray, error=red, and a distinct
// "in-progress" color (not reused from those three) for
// StatusTunnelConnecting/StatusDBConnecting/StatusTunnelUp.
func StatusColor(s app.ConnStatus) string {
	switch s {
	case app.StatusConnected:
		return "green"
	case app.StatusError:
		return "red"
	case app.StatusTunnelConnecting, app.StatusDBConnecting, app.StatusTunnelUp:
		return "yellow"
	default:
		return "gray"
	}
}

// ConnListRow formats a single connection-list row: "● name (type)
// host:port [group]" with the status dot colored via StatusColor (spec
// §5.3).
func ConnListRow(c config.Connection, status app.ConnStatus) string {
	color := StatusColor(status)
	row := fmt.Sprintf("[%s]●[white] %s (%s) %s:%d", color, c.Name, c.Type, c.Host, c.Port)
	if c.Group != "" {
		row += fmt.Sprintf(" [%s]", c.Group)
	}
	return row
}

// ConnListView is the :conn page: a single-column table of saved
// connections (spec §5.3). Keybinds: Enter connect, d disconnect, n new,
// e edit, x delete (confirm modal first).
type ConnListView struct {
	*tview.Table
	app *app.App

	// OnConnected is called (name of the connection) after a successful
	// Enter-to-connect, so the caller can lazily register/refresh the
	// tables and query pages (spec §6: built lazily once a connection is
	// active).
	OnConnected func(name string)
	// OnStatusChange is called after every connect/disconnect attempt
	// (success or failure) so the caller can refresh the status bar.
	OnStatusChange func()
}

func NewConnListView(a *app.App) *ConnListView {
	v := &ConnListView{Table: tview.NewTable().SetSelectable(true, false), app: a}
	v.Refresh()
	v.SetInputCapture(v.handleKey)
	return v
}

func (v *ConnListView) selectedName() (string, bool) {
	row, _ := v.Table.GetSelection()
	if row < 0 || row >= len(v.app.Config.Connections) {
		return "", false
	}
	return v.app.Config.Connections[row].Name, true
}

func (v *ConnListView) handleKey(event *tcell.EventKey) *tcell.EventKey {
	switch event.Key() {
	case tcell.KeyEnter:
		name, ok := v.selectedName()
		if !ok {
			return nil
		}
		go func() {
			err := v.app.Connect(context.Background(), name)
			v.app.TView.QueueUpdateDraw(func() {
				v.Refresh()
				if v.OnStatusChange != nil {
					v.OnStatusChange()
				}
				if err == nil && v.OnConnected != nil {
					v.OnConnected(name)
				}
			})
		}()
		return nil
	}
	switch event.Rune() {
	case 'd':
		go func() {
			_ = v.app.Disconnect()
			v.app.TView.QueueUpdateDraw(func() {
				v.Refresh()
				if v.OnStatusChange != nil {
					v.OnStatusChange()
				}
			})
		}()
		return nil
	case 'n':
		v.showConnForm(nil)
		return nil
	case 'e':
		name, ok := v.selectedName()
		if !ok {
			return nil
		}
		conn, err := app.FindConnection(v.app.Config, name)
		if err != nil {
			return nil
		}
		v.showConnForm(&conn)
		return nil
	case 'x':
		name, ok := v.selectedName()
		if !ok {
			return nil
		}
		v.showDeleteConfirm(name)
		return nil
	}
	return event
}

const connFormPage = "connform"

// showConnForm pushes the new/edit connection modal (spec §5.4). existing
// == nil means "new" ("n"); non-nil pre-fills every field for "edit" ("e").
func (v *ConnListView) showConnForm(existing *config.Connection) {
	form := NewConnFormView(v.app, existing)
	form.OnDone = func() {
		v.app.Pages.RemovePage(connFormPage)
		v.app.TView.SetFocus(v.Table)
		v.Refresh()
		if v.OnStatusChange != nil {
			v.OnStatusChange()
		}
	}
	v.app.Pages.AddPage(connFormPage, modalCenter(form, 64, 24), true, true)
	v.app.TView.SetFocus(form)
}

const deleteConfirmPage = "delete-confirm"

// showDeleteConfirm confirms before removing a connection and its stored
// password (spec §5.3's "x delete (confirm modal first)").
func (v *ConnListView) showDeleteConfirm(name string) {
	modal := tview.NewModal().
		SetText(fmt.Sprintf("Delete connection %q?", name)).
		AddButtons([]string{"Cancel", "Delete"})
	modal.SetDoneFunc(func(idx int, label string) {
		v.app.Pages.RemovePage(deleteConfirmPage)
		v.app.TView.SetFocus(v.Table)
		if label == "Delete" {
			v.app.Config.Connections = RemoveConnectionByName(v.app.Config.Connections, name)
			if err := config.Save(v.app.Config); err != nil {
				// Nothing to surface it to but the log — the connlist itself
				// has no error bar; a failed save just leaves the on-disk
				// file out of sync with the in-memory list, which the next
				// successful Save (e.g. from a subsequent edit) will fix.
				if v.app.Logger != nil {
					v.app.Logger.Write([]byte("connlist: delete " + name + ": save failed: " + err.Error() + "\n"))
				}
			}
			_ = secrets.DeletePassword(name)
			v.Refresh()
			if v.OnStatusChange != nil {
				v.OnStatusChange()
			}
		}
	})
	v.app.Pages.AddPage(deleteConfirmPage, modal, true, true)
}

// Refresh reloads the connection list from app.Config.
func (v *ConnListView) Refresh() {
	v.Table.Clear()
	active := v.app.GetActive()
	for i, c := range v.app.Config.Connections {
		status := app.StatusDisconnected
		if active != nil && active.Conn.Name == c.Name {
			status = active.Status
		}
		cell := tview.NewTableCell(ConnListRow(c, status))
		v.Table.SetCell(i, 0, cell)
	}
}
