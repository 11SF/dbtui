// Package views implements dbtui's page content: connection list/form,
// table/key/collection browser, query runner, result rendering, history,
// and logs. Per the test spec §7, every non-trivial branching/formatting
// decision is extracted into a pure, standalone function here so it's
// testable without a running terminal — the tview wiring around each one is
// kept as thin as possible.
package views

import (
	"context"
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"dbtui/internal/app"
	"dbtui/internal/config"
	"dbtui/internal/db"
)

// ErrUnsupportedClient is returned by SelectBrowserMode when the given
// client implements none of the three capability interfaces.
var ErrUnsupportedClient = fmt.Errorf("views: client does not implement SQLStore, KVStore, or DocumentStore")

// SelectBrowserMode decides which browser render mode (spec §5.5) applies
// to client, by type-asserting against the three capability interfaces
// rather than switching on DBType directly (keeps view code driver-agnostic
// per spec §4.1). Returns ErrUnsupportedClient instead of panicking when
// client implements none of them.
func SelectBrowserMode(client db.Client) (config.StoreCategory, error) {
	switch client.(type) {
	case db.SQLStore:
		return config.CategorySQL, nil
	case db.KVStore:
		return config.CategoryKV, nil
	case db.DocumentStore:
		return config.CategoryDocument, nil
	default:
		return "", ErrUnsupportedClient
	}
}

// BrowserPage is the :tables page's real tview content, built for whichever
// mode the active connection's client supports (spec §5.5).
type BrowserPage struct {
	tview.Primitive
	app *app.App
}

// NewBrowserPage builds the browser for app's current active connection,
// branching on SelectBrowserMode (never on DBType directly).
func NewBrowserPage(a *app.App) (*BrowserPage, error) {
	active := a.GetActive()
	if active == nil || active.Client == nil {
		return nil, fmt.Errorf("views: no active connection")
	}
	mode, err := SelectBrowserMode(active.Client)
	if err != nil {
		return nil, err
	}

	switch mode {
	case config.CategorySQL:
		return &BrowserPage{Primitive: newSQLBrowser(a, active.Client.(db.SQLStore)), app: a}, nil
	case config.CategoryKV:
		return &BrowserPage{Primitive: newKVBrowser(a, active.Client.(db.KVStore)), app: a}, nil
	case config.CategoryDocument:
		return &BrowserPage{Primitive: newDocumentBrowser(a, active.Client.(db.DocumentStore)), app: a}, nil
	}
	return nil, ErrUnsupportedClient
}

// --- SQL browser: TreeView (schema -> table) + a result pane on the right. ---

func newSQLBrowser(a *app.App, client db.SQLStore) tview.Primitive {
	root := tview.NewTreeNode("schemas").SetColor(tcell.ColorWhite)
	tree := tview.NewTreeView().SetRoot(root).SetCurrentNode(root)
	result := NewResultView()
	footer := tview.NewTextView().SetDynamicColors(true)
	result.OnStatus = func(msg string) { footer.SetText(msg) }
	result.OnLeave = func() { a.TView.SetFocus(tree) }

	ctx := context.Background()
	schemas, err := client.ListSchemas(ctx)
	if err != nil {
		footer.SetText(fmt.Sprintf("[red]list schemas: %v[white]", err))
	}
	for _, schema := range schemas {
		schemaNode := tview.NewTreeNode(schema).SetSelectable(true).SetColor(tcell.ColorYellow)
		schemaNode.SetReference(schema)
		root.AddChild(schemaNode)
	}

	loadedSchemas := map[string]bool{}

	tree.SetSelectedFunc(func(node *tview.TreeNode) {
		ref := node.GetReference()
		switch v := ref.(type) {
		case string: // schema node: lazily load its tables
			schema := v
			if loadedSchemas[schema] {
				node.SetExpanded(!node.IsExpanded())
				return
			}
			loadedSchemas[schema] = true
			tables, err := client.ListTables(ctx, schema)
			if err != nil {
				footer.SetText(fmt.Sprintf("[red]list tables: %v[white]", err))
				return
			}
			for _, tr := range tables {
				child := tview.NewTreeNode(fmt.Sprintf("%s (%s)", tr.Name, tr.Kind)).SetColor(tcell.ColorWhite)
				child.SetReference(tr)
				node.AddChild(child)
			}
			node.SetExpanded(true)
		case db.TableRef: // table node: run SELECT * ... LIMIT 100
			sql := fmt.Sprintf("SELECT * FROM %s.%s LIMIT 100", v.Schema, v.Name)
			res, err := client.Query(ctx, sql)
			if err != nil {
				footer.SetText(fmt.Sprintf("[red]%v[white]", err))
				return
			}
			result.SetResult(res)
			footer.SetText(ResultFooter(len(res.Rows), res.Duration.Milliseconds(), res.Capped))
			a.TView.SetFocus(result.Table)
		}
	})

	tree.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyTab {
			a.TView.SetFocus(result.Table)
			return nil
		}
		if event.Rune() == 'd' {
			node := tree.GetCurrentNode()
			if node == nil {
				return nil
			}
			tr, ok := node.GetReference().(db.TableRef)
			if !ok {
				return nil
			}
			desc, err := client.DescribeTable(ctx, tr.Schema, tr.Name)
			if err != nil {
				footer.SetText(fmt.Sprintf("[red]%v[white]", err))
				return nil
			}
			showDescribeModal(a, tr.Name, FormatTableDescription(desc), tree)
			return nil
		}
		return event
	})

	body := tview.NewFlex().
		AddItem(tree, 0, 1, true).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(result.Table, 0, 1, false).
			AddItem(footer, 1, 0, false), 0, 2, false)
	return body
}

// --- KV browser: flat key list for the current DB index. ---

func newKVBrowser(a *app.App, client db.KVStore) tview.Primitive {
	table := tview.NewTable().SetSelectable(true, false)
	footer := tview.NewTextView().SetDynamicColors(true)
	dbIndex := 0
	ctx := context.Background()

	var keys []string
	refresh := func(pattern string) {
		ks, err := db.ScanAllKeys(ctx, client, dbIndex, pattern, 500)
		if err != nil {
			footer.SetText(fmt.Sprintf("[red]%v[white]", err))
			return
		}
		keys = ks
		table.Clear()
		for i, k := range keys {
			kind, _ := client.KeyType(ctx, dbIndex, k)
			ttl, _ := client.TTL(ctx, dbIndex, k)
			table.SetCell(i, 0, tview.NewTableCell(fmt.Sprintf("%-8s %-40s ttl=%s", kind, k, ttl)))
		}
		footer.SetText(fmt.Sprintf("db%d: %d keys", dbIndex, len(keys)))
	}
	refresh("*")

	table.SetSelectedFunc(func(row, _ int) {
		if row < 0 || row >= len(keys) {
			return
		}
		v, err := client.GetValue(ctx, dbIndex, keys[row])
		if err != nil {
			footer.SetText(fmt.Sprintf("[red]%v[white]", err))
			return
		}
		showDescribeModal(a, keys[row], RenderKVValue(v), table)
	})

	body := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(table, 0, 1, true).
		AddItem(footer, 1, 0, false)
	return body
}

// RenderKVValue is re-exported here for the browser's key-detail modal;
// the pure implementation lives in internal/db (db.RenderKVValue).
func RenderKVValue(v *db.KVValue) string { return db.RenderKVValue(v) }

// --- Document browser: TreeView (database -> collection). ---

func newDocumentBrowser(a *app.App, client db.DocumentStore) tview.Primitive {
	root := tview.NewTreeNode("databases").SetColor(tcell.ColorWhite)
	tree := tview.NewTreeView().SetRoot(root).SetCurrentNode(root)
	result := NewResultView()
	footer := tview.NewTextView().SetDynamicColors(true)
	result.OnStatus = func(msg string) { footer.SetText(msg) }
	result.OnLeave = func() { a.TView.SetFocus(tree) }

	ctx := context.Background()
	dbs, err := client.ListDatabases(ctx)
	if err != nil {
		footer.SetText(fmt.Sprintf("[red]list databases: %v[white]", err))
	}
	for _, name := range dbs {
		node := tview.NewTreeNode(name).SetColor(tcell.ColorYellow)
		node.SetReference(name)
		root.AddChild(node)
	}

	type collRef struct{ database, collection string }
	loadedDBs := map[string]bool{}

	tree.SetSelectedFunc(func(node *tview.TreeNode) {
		switch ref := node.GetReference().(type) {
		case string:
			database := ref
			if loadedDBs[database] {
				node.SetExpanded(!node.IsExpanded())
				return
			}
			loadedDBs[database] = true
			colls, err := client.ListCollections(ctx, database)
			if err != nil {
				footer.SetText(fmt.Sprintf("[red]list collections: %v[white]", err))
				return
			}
			for _, c := range colls {
				child := tview.NewTreeNode(c).SetColor(tcell.ColorWhite)
				child.SetReference(collRef{database, c})
				node.AddChild(child)
			}
			node.SetExpanded(true)
		case collRef:
			res, err := client.Find(ctx, ref.database, ref.collection, "{}", 0)
			if err != nil {
				footer.SetText(fmt.Sprintf("[red]%v[white]", err))
				return
			}
			result.SetDocResult(res)
			footer.SetText(DocResultFooter(len(res.Documents), res.Duration.Milliseconds(), 100))
			a.TView.SetFocus(result.Table)
		}
	})

	tree.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyTab {
			a.TView.SetFocus(result.Table)
			return nil
		}
		if event.Rune() == 'd' {
			node := tree.GetCurrentNode()
			if node == nil {
				return nil
			}
			ref, ok := node.GetReference().(collRef)
			if !ok {
				return nil
			}
			indexes, err := client.IndexInfo(ctx, ref.database, ref.collection)
			if err != nil {
				footer.SetText(fmt.Sprintf("[red]%v[white]", err))
				return nil
			}
			showDescribeModal(a, ref.collection, FormatIndexInfo(indexes), tree)
			return nil
		}
		return event
	})

	body := tview.NewFlex().
		AddItem(tree, 0, 1, true).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(result.Table, 0, 1, false).
			AddItem(footer, 1, 0, false), 0, 2, false)
	return body
}

// showDescribeModal displays text (a formatted TableDescription/IndexInfo)
// in a dismissable modal, reusing one shared "describe" page (spec §5.5:
// "d on a table node → open the describe.go modal"). returnFocus is given
// focus back once the modal closes — without it, closing the modal leaves
// focus nowhere, the same dead-end the browser's tree/result panes had.
func showDescribeModal(a *app.App, title, text string, returnFocus tview.Primitive) {
	modal := tview.NewModal().
		SetText(title + "\n\n" + text).
		AddButtons([]string{"Close"})
	modal.SetDoneFunc(func(int, string) {
		a.Pages.RemovePage("describe")
		if returnFocus != nil {
			a.TView.SetFocus(returnFocus)
		}
	})
	a.Pages.AddPage("describe", modal, true, true)
}
