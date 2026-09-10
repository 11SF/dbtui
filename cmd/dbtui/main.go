// Command dbtui is a k9s-style terminal UI for connecting to databases
// (optionally through a kubectl port-forward tunnel), running
// queries/commands, and browsing schema — see dbtui-technical-spec-merged-en.md.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"dbtui/internal/app"
	"dbtui/internal/config"
	"dbtui/internal/history"
	"dbtui/internal/logging"
	"dbtui/internal/views"

	// Blank-imported so each driver's init() registers itself with
	// internal/db's driver registry (see internal/db/factory.go) before
	// NewClient is ever called.
	_ "dbtui/internal/db/mongo"
	_ "dbtui/internal/db/mysql"
	_ "dbtui/internal/db/postgres"
	_ "dbtui/internal/db/redis"
)

func historyPath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "dbtui", "history.db"), nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	histPath, err := historyPath()
	if err != nil {
		return fmt.Errorf("resolve history path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(histPath), 0o700); err != nil {
		return fmt.Errorf("create history dir: %w", err)
	}
	hist, err := history.Open(histPath)
	if err != nil {
		return fmt.Errorf("open history: %w", err)
	}
	defer hist.Close()

	logger := logging.NewRingBuffer(500)

	a := app.NewApp(cfg, hist, logger)

	connList := views.NewConnListView(a)
	logsView := views.NewLogsView(logger)
	historyView := views.NewHistoryView(a, hist)

	a.RegisterPage("conn", connList)
	a.RegisterPage("history", historyView)
	a.RegisterPage("logs", logsView)

	// registerLiveViews (re)builds the tables/query pages against whatever
	// connection is currently active, replacing any previous instance.
	// Called once right after a successful connect, and again each time the
	// user navigates to :tables/:query so a stale page never lingers past a
	// disconnect/reconnect (spec §6: "built lazily once a connection is
	// active").
	registerLiveViews := func() (tablesOK, queryOK bool) {
		if browser, err := views.NewBrowserPage(a); err == nil {
			a.Pages.RemovePage("tables")
			a.RegisterPage("tables", browser)
			tablesOK = true
		} else {
			logger.Log("tables: " + err.Error())
		}
		if queryPage, err := views.NewQueryPage(a); err == nil {
			a.Pages.RemovePage("query")
			a.RegisterPage("query", queryPage)
			queryOK = true
		} else {
			logger.Log("query: " + err.Error())
		}
		return
	}

	connList.OnConnected = func(string) { registerLiveViews() }

	a.OnCommand = func(cmd string) {
		switch cmd {
		case "conn":
			connList.Refresh()
			a.SwitchToPage("conn")
		case "history":
			historyView.Refresh()
			a.SwitchToPage("history")
		case "logs":
			logsView.Refresh()
			a.SwitchToPage("logs")
		case "tables":
			if ok, _ := registerLiveViews(); ok {
				a.SwitchToPage("tables")
			}
		case "query":
			if _, ok := registerLiveViews(); ok {
				a.SwitchToPage("query")
			}
		}
	}

	a.SwitchToPage("conn")
	return a.Run()
}
