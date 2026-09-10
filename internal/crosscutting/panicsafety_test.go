// Package crosscutting holds tests that span multiple backend packages
// (test spec §8): passwords never leaking into logs/errors, and panic
// safety across exported backend entry points reachable from a connect/
// query/command flow.
//
// The TUI-specific entry points (internal/app, internal/views) this file
// used to also cover were removed when dbtui pivoted from a terminal UI to
// a Wails-based desktop GUI (see gui/) — their equivalents are now
// GUI-bound methods in gui/app.go, with their own tests in gui/.
package crosscutting

import (
	"context"
	"io"
	"testing"
	"time"

	"dbtui/internal/config"
	"dbtui/internal/db"
	dbredis "dbtui/internal/db/redis"
	"dbtui/internal/logging"
	"dbtui/internal/tunnel"

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
}

// --- Run query / run command ---

func TestPanicSafety_RunQuery(t *testing.T) {
	mustNotPanic(t, "db.ApplyAutoLimit empty string", func() {
		_, _ = db.ApplyAutoLimit("")
	})

	mustNotPanic(t, "db.FormatValue nil", func() {
		_ = db.FormatValue(nil)
	})

	mustNotPanic(t, "db_redis.Client.RunCommand empty args on zero-value client", func() {
		c := &dbredis.Client{}
		_, _ = c.RunCommand(context.Background(), 0, nil)
	})
}

// --- History / logging entry points reachable from the query/command flow ---

func TestPanicSafety_Logging(t *testing.T) {
	mustNotPanic(t, "logging.RingBuffer.Write empty", func() {
		rb := logging.NewRingBuffer(5)
		_, _ = rb.Write(nil)
	})
}
