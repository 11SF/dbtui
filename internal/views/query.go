package views

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"dbtui/internal/app"
	"dbtui/internal/config"
	"dbtui/internal/db"
	"dbtui/internal/history"
)

// QueryView runs a query/command against the active connection, branching
// by input mode rather than being three separate views (spec §5.6).
type QueryView struct {
	Mode   config.StoreCategory
	Input  tview.Primitive
	Result *ResultView
}

// ErrUnsupportedCategory is returned by SelectQueryMode for a DBType with no
// recognized category (an empty StoreCategory).
var ErrUnsupportedCategory = fmt.Errorf("views: no query mode for this store category")

// SelectQueryMode decides QueryView.Mode from the active connection's
// DBType.Category(). Pure function, mirrors SelectBrowserMode's
// no-panic-on-unknown-input contract (test spec §7.2).
func SelectQueryMode(t config.DBType) (config.StoreCategory, error) {
	cat := t.Category()
	if cat == "" {
		return "", ErrUnsupportedCategory
	}
	return cat, nil
}

// destructiveCommandRe matches a Redis command's leading token against the
// destructive command list (spec §5.6/§10): FLUSHDB, FLUSHALL, DEL, UNLINK,
// case-insensitive. Anchored to the whole first token (\A...\z after
// splitting) rather than a bare substring search, so "DELAY_SOMETHING"
// does not false-positive on "DEL" (test spec §7.4).
var destructiveCommands = map[string]bool{
	"FLUSHDB":  true,
	"FLUSHALL": true,
	"DEL":      true,
	"UNLINK":   true,
}

// IsDestructiveRedisCommand reports whether the given redis-cli style
// command line requires the confirm modal before executing (spec §5.6,
// §10): a case-insensitive whole-token match on its first word against
// FLUSHDB/FLUSHALL/DEL/UNLINK, never a substring check.
func IsDestructiveRedisCommand(commandLine string) bool {
	fields := strings.Fields(commandLine)
	if len(fields) == 0 {
		return false
	}
	return destructiveCommands[strings.ToUpper(fields[0])]
}

// NewQueryView builds the query view content for the given category. The
// caller (app wiring) supplies result, already constructed via
// NewResultView.
func NewQueryView(mode config.StoreCategory, result *ResultView) *QueryView {
	var input tview.Primitive
	switch mode {
	case config.CategorySQL:
		input = tview.NewTextArea()
	case config.CategoryKV:
		input = tview.NewInputField()
	case config.CategoryDocument:
		input = tview.NewTextArea()
	default:
		input = tview.NewTextArea()
	}
	return &QueryView{Mode: mode, Input: input, Result: result}
}

// QueryPage wires a QueryView into an actual tview page against the app's
// active connection: SQL/Document modes run on Ctrl+R, KV mode runs on
// Enter (spec §5.6), gated by the destructive-command confirm modal in KV
// mode. It's the interactive counterpart to the pure functions above.
type QueryPage struct {
	*tview.Flex
	*QueryView
	app         *app.App
	footer      *tview.TextView
	targetField *tview.InputField // Document mode only: "database.collection"
	scratch     *scratchDebouncer // SQL mode only: spec §5.6 crash-recovery autosave
}

// scratchDebounceDelay is the debounce interval SQL-mode :query autosave
// waits for typing to settle before writing to disk (spec §5.6: "500ms
// debounce").
const scratchDebounceDelay = 500 * time.Millisecond

// NewQueryPage builds the :query page for the app's current active
// connection. Returns an error (rather than panicking) if there is no
// active connection or its type has no recognized category.
func NewQueryPage(a *app.App) (*QueryPage, error) {
	active := a.GetActive()
	if active == nil || active.Client == nil {
		return nil, fmt.Errorf("views: no active connection")
	}
	mode, err := SelectQueryMode(active.Conn.Type)
	if err != nil {
		return nil, err
	}

	result := NewResultView()
	qv := NewQueryView(mode, result)
	footer := tview.NewTextView().SetDynamicColors(true)
	result.OnStatus = func(msg string) { footer.SetText(msg) }

	p := &QueryPage{QueryView: qv, app: a, footer: footer}

	body := tview.NewFlex().SetDirection(tview.FlexRow)
	switch mode {
	case config.CategorySQL, config.CategoryDocument:
		if mode == config.CategoryDocument {
			p.targetField = tview.NewInputField().SetLabel("db.collection: ")
			if active.Conn.DBName != "" {
				p.targetField.SetText(active.Conn.DBName + ".")
			}
			body.AddItem(p.targetField, 1, 0, false)
		}
		body.AddItem(qv.Input, 0, 1, true)
		textArea := qv.Input.(*tview.TextArea)
		textArea.SetInputCapture(p.handleTextAreaKey)
		if mode == config.CategorySQL {
			if saved, err := LoadScratch(); err == nil && saved != "" {
				textArea.SetText(saved, true)
			}
			p.scratch = newScratchDebouncer(scratchDebounceDelay, SaveScratch)
			textArea.SetChangedFunc(func() {
				p.scratch.Trigger(textArea.GetText)
			})
		}
	case config.CategoryKV:
		input := qv.Input.(*tview.InputField)
		dbIndex := 0
		input.SetLabel(fmt.Sprintf("%s:%d[db%d]> ", active.Conn.Host, active.Conn.Port, dbIndex))
		input.SetDoneFunc(func(key tcell.Key) {
			if key == tcell.KeyEnter {
				p.runKV(input.GetText(), false)
			}
		})
		body.AddItem(input, 1, 0, true)
	}
	body.AddItem(result.Table, 0, 2, false)
	body.AddItem(footer, 1, 0, false)

	p.Flex = body
	return p, nil
}

func (p *QueryPage) handleTextAreaKey(event *tcell.EventKey) *tcell.EventKey {
	if event.Key() == tcell.KeyCtrlR {
		switch p.Mode {
		case config.CategorySQL:
			p.runSQL(p.Input.(*tview.TextArea).GetText())
		case config.CategoryDocument:
			p.runFind(p.Input.(*tview.TextArea).GetText())
		}
		return nil
	}
	return event
}

func (p *QueryPage) runSQL(sql string) {
	active := p.app.GetActive()
	store, ok := active.Client.(db.SQLStore)
	if !ok {
		p.footer.SetText("[red]active connection does not support SQL queries[white]")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), active.Conn.QueryTimeout())
	defer cancel()

	res, err := store.Query(ctx, sql)
	entry := &history.HistoryEntry{Timestamp: time.Now(), ConnName: active.Conn.Name, Query: sql}
	if err != nil {
		entry.Error = err.Error()
		p.footer.SetText(fmt.Sprintf("[red]%s[white]", err.Error()))
	} else {
		entry.DurationMs = res.Duration.Milliseconds()
		entry.RowCount = len(res.Rows)
		p.Result.SetResult(res)
		p.footer.SetText(ResultFooter(len(res.Rows), entry.DurationMs, res.Capped))
	}
	if p.app.History != nil {
		_ = p.app.History.Insert(entry)
	}
}

func (p *QueryPage) runFind(filterJSON string) {
	active := p.app.GetActive()
	store, ok := active.Client.(db.DocumentStore)
	if !ok {
		p.footer.SetText("[red]active connection does not support document queries[white]")
		return
	}
	database, collection := parseDBCollection(p.targetField.GetText())
	if database == "" || collection == "" {
		p.footer.SetText("[red]set db.collection first[white]")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), active.Conn.QueryTimeout())
	defer cancel()

	res, err := store.Find(ctx, database, collection, filterJSON, 0)
	entry := &history.HistoryEntry{Timestamp: time.Now(), ConnName: active.Conn.Name, Query: filterJSON}
	if err != nil {
		entry.Error = err.Error()
		p.footer.SetText(fmt.Sprintf("[red]%s[white]", err.Error()))
	} else {
		entry.DurationMs = res.Duration.Milliseconds()
		entry.RowCount = len(res.Documents)
		p.Result.SetDocResult(res)
		p.footer.SetText(DocResultFooter(len(res.Documents), entry.DurationMs, 100))
	}
	if p.app.History != nil {
		_ = p.app.History.Insert(entry)
	}
}

func (p *QueryPage) runKV(commandLine string, confirmed bool) {
	if IsDestructiveRedisCommand(commandLine) && !confirmed {
		p.showConfirm(commandLine)
		return
	}

	active := p.app.GetActive()
	store, ok := active.Client.(db.KVStore)
	if !ok {
		p.footer.SetText("[red]active connection does not support KV commands[white]")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), active.Conn.QueryTimeout())
	defer cancel()

	args := strings.Fields(commandLine)
	dbIndex := 0
	result, err := store.RunCommand(ctx, dbIndex, args)
	entry := &history.HistoryEntry{Timestamp: time.Now(), ConnName: active.Conn.Name, Query: commandLine}
	if err != nil {
		entry.Error = err.Error()
		p.footer.SetText(fmt.Sprintf("[red]%s[white]", err.Error()))
	} else {
		entry.RowCount = 1
		p.Result.SetRaw(fmt.Sprintf("%v", result))
		p.footer.SetText(ResultFooter(1, entry.DurationMs, false))
	}
	if p.app.History != nil {
		_ = p.app.History.Insert(entry)
	}
	if input, ok := p.Input.(*tview.InputField); ok {
		input.SetText("")
	}
}

func (p *QueryPage) showConfirm(commandLine string) {
	modal := tview.NewModal().
		SetText(fmt.Sprintf("Run destructive command %q?", commandLine)).
		AddButtons([]string{"Cancel", "Run"})
	modal.SetDoneFunc(func(idx int, label string) {
		p.app.Pages.RemovePage("confirm")
		if label == "Run" {
			p.runKV(commandLine, true)
		}
	})
	p.app.Pages.AddPage("confirm", modal, true, true)
}

// parseDBCollection splits "database.collection" into its two parts.
func parseDBCollection(s string) (database, collection string) {
	idx := strings.Index(s, ".")
	if idx < 0 {
		return "", ""
	}
	return s[:idx], s[idx+1:]
}
