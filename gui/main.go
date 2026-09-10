package main

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"dbtui/internal/config"
	"dbtui/internal/history"
	"dbtui/internal/logging"

	// Blank-imported so each driver's init() registers itself with
	// internal/db's driver registry (see internal/db/factory.go) before
	// db.NewClient is ever called.
	_ "dbtui/internal/db/mongo"
	_ "dbtui/internal/db/mysql"
	_ "dbtui/internal/db/postgres"
	_ "dbtui/internal/db/redis"
)

//go:embed all:frontend/dist
var assets embed.FS

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

	app := NewApp(cfg, hist, logger)

	return wails.Run(&options.App{
		Title:  "dbtui",
		Width:  1280,
		Height: 800,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 20, G: 20, B: 24, A: 1},
		OnStartup:        app.startup,
		Bind: []interface{}{
			app,
		},
	})
}
