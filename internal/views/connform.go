package views

import (
	"fmt"
	"strconv"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"dbtui/internal/app"
	"dbtui/internal/config"
	"dbtui/internal/secrets"
)

// visibleFields returns the connection-form fields shown for a given
// DBType, per spec §5.4's per-type field visibility table:
//   - postgres/mysql: Host, Port, User, Password, DBName
//   - redis: Host, Port, Password (User hidden — optional ACL username,
//     deferred as an advanced field)
//   - mongodb: Host, Port, User, Password, DBName, AuthSource
//
// Order is the same field ordering used to lay out the tview.Form.
func visibleFields(t config.DBType) []string {
	switch t {
	case config.Postgres, config.MySQL:
		return []string{"Host", "Port", "User", "Password", "DBName"}
	case config.Redis:
		return []string{"Host", "Port", "Password"}
	case config.MongoDB:
		return []string{"Host", "Port", "User", "Password", "DBName", "AuthSource"}
	default:
		return nil
	}
}

// VisibleFields exports visibleFields for callers outside the package (the
// tview form-building code) while keeping the name test spec §7.7 asks for
// (visibleFields) as the actual pure function under test.
func VisibleFields(t config.DBType) []string { return visibleFields(t) }

// ValidateConnForm checks that every field visibleFields requires for t is
// non-empty in values (keyed by field name), per spec §5.4: "Submit →
// validate required fields for the selected type". Password is validated
// separately (it never lives in the values map — see spec §3.1) via
// requirePassword.
func ValidateConnForm(t config.DBType, values map[string]string, requirePassword bool) error {
	for _, f := range visibleFields(t) {
		if f == "Password" {
			continue
		}
		if values[f] == "" {
			return &ValidationError{Field: f}
		}
	}
	if requirePassword && values["Password"] == "" {
		return &ValidationError{Field: "Password"}
	}
	return nil
}

// ValidationError reports a missing required connection-form field.
type ValidationError struct {
	Field string
}

func (e *ValidationError) Error() string {
	return e.Field + " is required"
}

// --- Pure form-state helpers (constructor/submit/delete logic, kept
// independently testable from the tview.Form wiring below, same principle
// as visibleFields/ValidateConnForm). ---

// dbTypeOptions is the Type dropdown's option list, in display order.
var dbTypeOptions = []config.DBType{config.Postgres, config.MySQL, config.Redis, config.MongoDB}

// ConnFormValuesFromConnection maps an existing config.Connection onto the
// form's field-name -> value representation, for edit-mode pre-fill (spec
// §5.4: "e" edit). Password is deliberately never populated here — it is
// never read back out of the keyring for display, per spec §3.1's "password
// is NOT stored" guarantee; the user must re-enter it to change it, and an
// edit that doesn't touch Password leaves the stored one untouched (see
// BuildConnectionFromForm/onSubmit).
func ConnFormValuesFromConnection(c config.Connection) map[string]string {
	values := map[string]string{
		"Host":       c.Host,
		"Port":       "",
		"User":       c.User,
		"DBName":     c.DBName,
		"AuthSource": c.AuthSource,
		"Group":      c.Group,
	}
	if c.Port != 0 {
		values["Port"] = strconv.Itoa(c.Port)
	}
	return values
}

// ConnFormTunnelValuesFromConnection maps an existing connection's tunnel
// config (if any) onto the tunnel sub-form's field values, and reports
// whether the "use kubectl tunnel" checkbox should start checked.
func ConnFormTunnelValuesFromConnection(c config.Connection) (values map[string]string, useTunnel bool) {
	values = map[string]string{
		"KubeContext": "", "Namespace": "", "TargetType": "pod", "TargetName": "", "RemotePort": "", "LocalPort": "",
	}
	if c.Tunnel == nil {
		return values, false
	}
	t := c.Tunnel
	values["KubeContext"] = t.KubeContext
	values["Namespace"] = t.Namespace
	if t.TargetType != "" {
		values["TargetType"] = t.TargetType
	}
	values["TargetName"] = t.TargetName
	if t.RemotePort != 0 {
		values["RemotePort"] = strconv.Itoa(t.RemotePort)
	}
	if t.LocalPort != 0 {
		values["LocalPort"] = strconv.Itoa(t.LocalPort)
	}
	return values, true
}

// BuildConnectionFromForm validates and assembles a config.Connection from
// the form's current state. name/dbType come from their own dedicated form
// items (not the visibleFields-driven map, since every type needs a name
// and the type itself decides which of the other fields apply).
// requirePassword mirrors ValidateConnForm's parameter: false in edit mode
// when the user left Password blank (meaning "keep the existing one").
func BuildConnectionFromForm(name string, dbType config.DBType, values map[string]string, requirePassword bool, useTunnel bool, tunnelValues map[string]string) (config.Connection, error) {
	if name == "" {
		return config.Connection{}, &ValidationError{Field: "Name"}
	}
	if err := ValidateConnForm(dbType, values, requirePassword); err != nil {
		return config.Connection{}, err
	}

	c := config.Connection{
		Name:       name,
		Type:       dbType,
		Host:       values["Host"],
		User:       values["User"],
		DBName:     values["DBName"],
		AuthSource: values["AuthSource"],
		Group:      values["Group"],
	}
	if values["Port"] != "" {
		port, err := strconv.Atoi(values["Port"])
		if err != nil {
			return config.Connection{}, &ValidationError{Field: "Port"}
		}
		c.Port = port
	}

	if useTunnel {
		tc := &config.TunnelConfig{
			KubeContext: tunnelValues["KubeContext"],
			Namespace:   tunnelValues["Namespace"],
			TargetType:  tunnelValues["TargetType"],
			TargetName:  tunnelValues["TargetName"],
		}
		if tunnelValues["Namespace"] == "" || tunnelValues["TargetName"] == "" {
			return config.Connection{}, &ValidationError{Field: "Namespace/TargetName"}
		}
		if tunnelValues["RemotePort"] != "" {
			p, err := strconv.Atoi(tunnelValues["RemotePort"])
			if err != nil {
				return config.Connection{}, &ValidationError{Field: "RemotePort"}
			}
			tc.RemotePort = p
		} else {
			return config.Connection{}, &ValidationError{Field: "RemotePort"}
		}
		if tunnelValues["LocalPort"] != "" {
			p, err := strconv.Atoi(tunnelValues["LocalPort"])
			if err != nil {
				return config.Connection{}, &ValidationError{Field: "LocalPort"}
			}
			tc.LocalPort = p
		}
		c.Tunnel = tc
	}

	return c, nil
}

// UpsertConnection returns a new slice with c inserted or replacing an
// existing entry. If originalName is non-empty, the entry with that name is
// replaced (this is how an edit that also renames a connection works — spec
// §5.4's "e edit" reuses the same modal as "n new"); otherwise, an existing
// entry named c.Name is replaced in place (so a duplicate-name save doesn't
// create two entries), or c is appended if no such entry exists.
func UpsertConnection(conns []config.Connection, c config.Connection, originalName string) []config.Connection {
	key := originalName
	if key == "" {
		key = c.Name
	}
	out := make([]config.Connection, len(conns))
	copy(out, conns)
	for i, existing := range out {
		if existing.Name == key {
			out[i] = c
			return out
		}
	}
	return append(out, c)
}

// RemoveConnectionByName returns a new slice with the named connection
// removed (spec §5.3's "x delete" keybind). A no-op (returns an equivalent
// copy) if no connection with that name exists.
func RemoveConnectionByName(conns []config.Connection, name string) []config.Connection {
	out := make([]config.Connection, 0, len(conns))
	for _, c := range conns {
		if c.Name != name {
			out = append(out, c)
		}
	}
	return out
}

// --- Interactive tview.Form modal ---

// ConnFormMode distinguishes "n" (new) from "e" (edit) — the only
// difference is pre-filled values and which key UpsertConnection replaces.
type ConnFormMode int

const (
	ConnFormNew ConnFormMode = iota
	ConnFormEdit
)

// ConnFormView is the :conn "n"/"e" modal (spec §5.4): a tview.Form laid
// over connlist, whose visible fields change with the Type dropdown and the
// "use kubectl tunnel" checkbox. All persistence logic lives in the pure
// functions above; this type only wires tview widgets to that state and to
// config.Save/secrets.SetPassword on submit.
type ConnFormView struct {
	*tview.Flex
	form   *tview.Form
	errBar *tview.TextView

	appRef       *app.App
	mode         ConnFormMode
	originalName string

	name         string
	dbType       config.DBType
	values       map[string]string
	tunnelValues map[string]string
	useTunnel    bool

	// OnDone is called after a successful submit or a cancel, so the caller
	// (connlist.go) can remove this page and refresh the list.
	OnDone func()
}

// NewConnFormView builds the modal. existing == nil means "new connection"
// (spec's "n"); a non-nil existing pre-fills every field for "e" edit.
func NewConnFormView(a *app.App, existing *config.Connection) *ConnFormView {
	v := &ConnFormView{appRef: a, dbType: config.Postgres, values: map[string]string{}}
	v.tunnelValues, v.useTunnel = ConnFormTunnelValuesFromConnection(config.Connection{})

	if existing != nil {
		v.mode = ConnFormEdit
		v.originalName = existing.Name
		v.name = existing.Name
		v.dbType = existing.Type
		v.values = ConnFormValuesFromConnection(*existing)
		v.tunnelValues, v.useTunnel = ConnFormTunnelValuesFromConnection(*existing)
	} else {
		v.mode = ConnFormNew
		v.values["Port"] = ""
	}

	v.errBar = tview.NewTextView().SetDynamicColors(true)
	v.form = tview.NewForm()
	v.rebuild()

	v.Flex = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(v.errBar, 1, 0, false).
		AddItem(v.form, 0, 1, true)
	v.Flex.SetBorder(true).SetTitle(formTitle(v.mode))
	v.Flex.SetInputCapture(v.handleFormEsc)

	return v
}

func formTitle(mode ConnFormMode) string {
	if mode == ConnFormEdit {
		return " Edit Connection "
	}
	return " New Connection "
}

// showError displays a validation/save error in the modal's error bar
// rather than dismissing it, so the user can fix the offending field
// without losing what they've already entered.
func (v *ConnFormView) showError(err error) {
	v.errBar.SetText(fmt.Sprintf("[red]%s[white]", err.Error()))
}

// rebuild fully regenerates the tview.Form's items from current state
// (dbType, useTunnel) while preserving already-entered values in
// v.values/v.tunnelValues — tview.Form has no "insert/remove field by name"
// API, so changing the Type dropdown or the tunnel checkbox rebuilds the
// whole item list rather than trying to splice it.
func (v *ConnFormView) rebuild() {
	focused, _ := v.form.GetFocusedItemIndex()
	v.form.Clear(true)

	v.form.AddInputField("Name", v.name, 30, nil, func(text string) { v.name = text })

	typeIdx := 0
	for i, t := range dbTypeOptions {
		if t == v.dbType {
			typeIdx = i
		}
	}
	typeStrs := make([]string, len(dbTypeOptions))
	for i, t := range dbTypeOptions {
		typeStrs[i] = string(t)
	}
	v.form.AddDropDown("Type", typeStrs, typeIdx, func(text string, index int) {
		newType := dbTypeOptions[index]
		if newType != v.dbType {
			v.dbType = newType
			v.rebuild()
		}
	})

	for _, field := range visibleFields(v.dbType) {
		field := field
		if field == "Password" {
			v.form.AddPasswordField("Password", "", 30, '*', func(text string) { v.values["Password"] = text })
			continue
		}
		v.form.AddInputField(field, v.values[field], 30, nil, func(text string) { v.values[field] = text })
	}

	v.form.AddCheckbox("Use kubectl tunnel", v.useTunnel, func(checked bool) {
		v.useTunnel = checked
		v.rebuild()
	})

	if v.useTunnel {
		v.form.AddInputField("KubeContext", v.tunnelValues["KubeContext"], 30, nil, func(text string) { v.tunnelValues["KubeContext"] = text })
		v.form.AddInputField("Namespace", v.tunnelValues["Namespace"], 30, nil, func(text string) { v.tunnelValues["Namespace"] = text })
		targetIdx := 0
		if v.tunnelValues["TargetType"] == "svc" {
			targetIdx = 1
		}
		v.form.AddDropDown("TargetType", []string{"pod", "svc"}, targetIdx, func(text string, index int) {
			v.tunnelValues["TargetType"] = text
		})
		v.form.AddInputField("TargetName", v.tunnelValues["TargetName"], 30, nil, func(text string) { v.tunnelValues["TargetName"] = text })
		v.form.AddInputField("RemotePort", v.tunnelValues["RemotePort"], 30, nil, func(text string) { v.tunnelValues["RemotePort"] = text })
		v.form.AddInputField("LocalPort", v.tunnelValues["LocalPort"], 30, nil, func(text string) { v.tunnelValues["LocalPort"] = text })
	}

	v.form.AddButton("Save", v.onSubmit)
	v.form.AddButton("Cancel", func() {
		if v.OnDone != nil {
			v.OnDone()
		}
	})

	if focused >= 0 && focused < v.form.GetFormItemCount() {
		v.form.SetFocus(focused)
	}
}

func (v *ConnFormView) onSubmit() {
	requirePassword := v.mode == ConnFormNew
	conn, err := BuildConnectionFromForm(v.name, v.dbType, v.values, requirePassword, v.useTunnel, v.tunnelValues)
	if err != nil {
		v.showError(err)
		return
	}

	v.appRef.Config.Connections = UpsertConnection(v.appRef.Config.Connections, conn, v.originalName)
	if err := config.Save(v.appRef.Config); err != nil {
		v.showError(err)
		return
	}

	if pw := v.values["Password"]; pw != "" {
		if err := secrets.SetPassword(conn.Name, pw); err != nil {
			v.showError(err)
			return
		}
	}

	if v.OnDone != nil {
		v.OnDone()
	}
}

// modalCenter wraps p in nested Flexes so it renders as a fixed-size box
// centered over whatever's already on the page (spec §5.4: "sized smaller
// than fullscreen"), instead of stretching to fill the terminal.
func modalCenter(p tview.Primitive, width, height int) tview.Primitive {
	return tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(p, height, 1, true).
			AddItem(nil, 0, 1, false), width, 1, true).
		AddItem(nil, 0, 1, false)
}

// handleFormEsc lets Esc cancel the modal (in addition to the Cancel
// button), consistent with the rest of the app's Esc-goes-back convention.
func (v *ConnFormView) handleFormEsc(event *tcell.EventKey) *tcell.EventKey {
	if event.Key() == tcell.KeyEsc {
		if v.OnDone != nil {
			v.OnDone()
		}
		return nil
	}
	return event
}
