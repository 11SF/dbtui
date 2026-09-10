// Package crosscutting holds tests that span multiple packages (test spec
// §8): passwords never leaking into logs/errors, and panic safety across
// every exported entry point reachable from a key event (connect, run
// query, run command, disconnect, export).
package crosscutting

import (
	"context"
	"io"
	"testing"
	"time"

	"dbtui/internal/app"
	"dbtui/internal/config"
	"dbtui/internal/db"
	dbredis "dbtui/internal/db/redis"
	"dbtui/internal/history"
	"dbtui/internal/logging"
	"dbtui/internal/tunnel"
	"dbtui/internal/views"

	// Registers the postgres driver constructor so db.NewClient(config.Postgres, ...)
	// exercises the real driver's zero-value-input handling below.
	_ "dbtui/internal/db/postgres"
)

// mustNotPanic runs fn and fails the test if it panics, per test spec
// §8.2's "assert no panic — either via recover() wrapping ... or by
// asserting the function returns an error instead of panicking".
func mustNotPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("%s panicked: %v", name, r)
		}
	}()
	fn()
}

// --- Connect ---

func TestPanicSafety_Connect(t *testing.T) {
	mustNotPanic(t, "db.NewClient unsupported type", func() {
		_, _ = db.NewClient(context.Background(), config.Connection{Type: config.DBType("")}, "")
	})

	mustNotPanic(t, "db.NewClient zero-value postgres connection", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		conn := config.Connection{Type: config.Postgres, QueryTimeoutSec: 1}
		_, _ = db.NewClient(ctx, conn, "")
	})

	mustNotPanic(t, "tunnel.Start with zero TunnelConfig", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = tunnel.Start(ctx, config.TunnelConfig{}, io.Discard, tunnel.Options{ConnectTimeout: 500 * time.Millisecond})
	})

	mustNotPanic(t, "app.NewApp with nil config/history/logger", func() {
		a := app.NewApp(nil, nil, nil)
		if a == nil {
			t.Errorf("NewApp returned nil")
		}
	})
}

// --- Run query / run command ---

func TestPanicSafety_RunQuery(t *testing.T) {
	mustNotPanic(t, "db.ApplyAutoLimit empty string", func() {
		_, _ = db.ApplyAutoLimit("")
	})

	mustNotPanic(t, "db.FormatValue nil", func() {
		_ = db.FormatValue(nil)
	})

	mustNotPanic(t, "views.SelectQueryMode empty DBType", func() {
		_, _ = views.SelectQueryMode(config.DBType(""))
	})

	mustNotPanic(t, "views.SelectBrowserMode nil client", func() {
		var c db.Client
		_, _ = views.SelectBrowserMode(c)
	})

	mustNotPanic(t, "views.ValidateConnForm nil values map", func() {
		_ = views.ValidateConnForm(config.Postgres, nil, false)
	})

	mustNotPanic(t, "views.BuildConnectionFromForm nil values/tunnelValues", func() {
		_, _ = views.BuildConnectionFromForm("", config.DBType(""), nil, false, false, nil)
	})

	mustNotPanic(t, "views.BuildConnectionFromForm nil values, tunnel requested", func() {
		_, _ = views.BuildConnectionFromForm("n", config.Postgres, nil, false, true, nil)
	})

	mustNotPanic(t, "views.UpsertConnection nil slice", func() {
		_ = views.UpsertConnection(nil, config.Connection{Name: "n"}, "")
	})

	mustNotPanic(t, "views.RemoveConnectionByName nil slice", func() {
		_ = views.RemoveConnectionByName(nil, "n")
	})

	mustNotPanic(t, "views.ConnFormValuesFromConnection zero-value connection", func() {
		_ = views.ConnFormValuesFromConnection(config.Connection{})
	})

	mustNotPanic(t, "views.ConnFormTunnelValuesFromConnection zero-value connection", func() {
		_, _ = views.ConnFormTunnelValuesFromConnection(config.Connection{})
	})

	mustNotPanic(t, "views.NewConnFormView nil app and nil existing", func() {
		_ = views.NewConnFormView(nil, nil)
	})

	mustNotPanic(t, "db_redis.Client.RunCommand empty args on zero-value client", func() {
		c := &dbredis.Client{}
		_, _ = c.RunCommand(context.Background(), 0, nil)
	})
}

func TestPanicSafety_RunCommand(t *testing.T) {
	mustNotPanic(t, "views.IsDestructiveRedisCommand empty string", func() {
		_ = views.IsDestructiveRedisCommand("")
	})
	mustNotPanic(t, "views.IsDestructiveRedisCommand whitespace only", func() {
		_ = views.IsDestructiveRedisCommand("   ")
	})
}

// --- Disconnect ---

func TestPanicSafety_Disconnect(t *testing.T) {
	mustNotPanic(t, "app.Disconnect with nothing connected", func() {
		a := app.NewApp(&config.ConfigFile{}, nil, logging.NewRingBuffer(10))
		_ = a.Disconnect()
	})

	mustNotPanic(t, "app.Disconnect called twice in a row", func() {
		a := app.NewApp(&config.ConfigFile{}, nil, logging.NewRingBuffer(10))
		_ = a.Disconnect()
		_ = a.Disconnect()
	})
}

// --- Export / yank ---

func TestPanicSafety_Export(t *testing.T) {
	mustNotPanic(t, "views.ExportCSV nil result", func() {
		_ = views.ExportCSV(nil)
	})
	mustNotPanic(t, "views.ExportCSV zero-value result", func() {
		_ = views.ExportCSV(&db.QueryResult{})
	})
	mustNotPanic(t, "views.FormatDocResult nil", func() {
		_ = views.FormatDocResult(nil)
	})
	mustNotPanic(t, "views.ExportJSONL nil slice", func() {
		_ = views.ExportJSONL(nil)
	})
	mustNotPanic(t, "views.YankRow nil slice", func() {
		_ = views.YankRow(nil)
	})
	mustNotPanic(t, "views.YankCell nil value", func() {
		_ = views.YankCell(nil)
	})
	mustNotPanic(t, "ResultView.SetResult nil", func() {
		rv := views.NewResultView()
		rv.SetResult(nil)
	})
	mustNotPanic(t, "ResultView.SetDocResult nil", func() {
		rv := views.NewResultView()
		rv.SetDocResult(nil)
	})
}

// --- SQL-mode scratch-buffer autosave (spec §5.6) ---

func TestPanicSafety_ScratchBuffer(t *testing.T) {
	// Isolate from the real OS config dir (t.Setenv), same as
	// internal/config's own tests — these must never touch a real HOME.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp+"/.config")
	t.Setenv("USERPROFILE", tmp)

	mustNotPanic(t, "views.SaveScratch empty string", func() {
		_ = views.SaveScratch("")
	})
	mustNotPanic(t, "views.LoadScratch on a fresh dir", func() {
		_, _ = views.LoadScratch()
	})
}

// --- History / logging entry points reachable from the query/command flow ---

func TestPanicSafety_HistoryAndLogging(t *testing.T) {
	mustNotPanic(t, "views.TruncatePreview empty string", func() {
		_ = views.TruncatePreview("")
	})
	mustNotPanic(t, "views.HistoryRow zero-value entry", func() {
		_ = views.HistoryRow(history.HistoryEntry{})
	})
	mustNotPanic(t, "views.FilterHistory nil slice", func() {
		_ = views.FilterHistory(nil, "x")
	})
	mustNotPanic(t, "logging.RingBuffer.Write empty", func() {
		rb := logging.NewRingBuffer(5)
		_, _ = rb.Write(nil)
	})
}
