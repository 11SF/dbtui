// Command dbtui-gui is a Wails desktop GUI over the same backend the
// former dbtui TUI used (internal/config, internal/secrets, internal/db +
// drivers, internal/tunnel, internal/history, internal/logging) — only the
// presentation layer changed. App is the Wails-bound Go side: every
// exported method here is callable from the TypeScript frontend via
// generated bindings in frontend/wailsjs/go/main/App.
package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"dbtui/internal/config"
	"dbtui/internal/db"
	"dbtui/internal/history"
	"dbtui/internal/logging"
	"dbtui/internal/secrets"
	"dbtui/internal/tunnel"
)

// ConnStatus mirrors the old TUI's app.ConnStatus, as a string so it
// serializes cleanly to JSON for the frontend.
type ConnStatus string

const (
	StatusDisconnected     ConnStatus = "disconnected"
	StatusTunnelConnecting ConnStatus = "tunnel_connecting"
	StatusTunnelUp         ConnStatus = "tunnel_up"
	StatusDBConnecting     ConnStatus = "db_connecting"
	StatusConnected        ConnStatus = "connected"
	StatusError            ConnStatus = "error"
)

// ActiveConnection is the app's single in-flight/established connection —
// same "one connection at a time in v1" scope the backend already assumes.
type ActiveConnection struct {
	Conn        config.Connection
	Status      ConnStatus
	TunnelProc  *tunnel.Tunnel
	Client      db.Client
	LastError   error
	ConnectedAt time.Time
}

// StatusPayload is the JSON-friendly projection of ActiveConnection sent to
// the frontend, both as the Connect/GetStatus return value and as the
// "status" runtime event emitted at every transition during Connect.
type StatusPayload struct {
	Name            string `json:"name"`
	Type            string `json:"type"`
	Category        string `json:"category"`
	Status          string `json:"status"`
	TunnelLocalPort int    `json:"tunnelLocalPort,omitempty"`
	Error           string `json:"error,omitempty"`
}

func statusPayload(ac *ActiveConnection) StatusPayload {
	if ac == nil {
		return StatusPayload{Status: string(StatusDisconnected)}
	}
	p := StatusPayload{
		Name:     ac.Conn.Name,
		Type:     string(ac.Conn.Type),
		Category: string(ac.Conn.Type.Category()),
		Status:   string(ac.Status),
	}
	if ac.TunnelProc != nil {
		p.TunnelLocalPort = ac.TunnelProc.LocalPort
	}
	if ac.LastError != nil {
		p.Error = ac.LastError.Error()
	}
	return p
}

// App is the Wails-bound application struct.
type App struct {
	ctx context.Context

	mu     sync.Mutex
	cfg    *config.ConfigFile
	hist   *history.Store
	logger *logging.RingBuffer
	active *ActiveConnection

	// lastResult/lastDocs back the export endpoints — same "single active
	// result" simplicity the old ResultView had (one at a time, whichever
	// query/find ran most recently).
	lastResult *db.QueryResult
	lastDocs   *db.DocResult
}

// NewApp constructs the App with its backend dependencies already opened
// (config loaded, history store opened, logger created) — main.go does that
// setup, mirroring the old cmd/dbtui/main.go's run().
func NewApp(cfg *config.ConfigFile, hist *history.Store, logger *logging.RingBuffer) *App {
	return &App{cfg: cfg, hist: hist, logger: logger, active: &ActiveConnection{Status: StatusDisconnected}}
}

// startup is Wails' OnStartup hook: saves the runtime context so bound
// methods can emit events.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

func (a *App) setActive(ac *ActiveConnection) {
	a.mu.Lock()
	a.active = ac
	a.mu.Unlock()
	if a.ctx != nil {
		wailsRuntime.EventsEmit(a.ctx, "status", statusPayload(ac))
	}
}

func (a *App) getActive() *ActiveConnection {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.active
}

// GetStatus returns the current connection status — called on load (or
// after a window reload) instead of assuming the frontend already knows
// it from a prior "status" event.
func (a *App) GetStatus() StatusPayload {
	return statusPayload(a.getActive())
}

var (
	secretsGetPassword    = secrets.GetPassword
	secretsSetPassword    = secrets.SetPassword
	secretsDeletePassword = secrets.DeletePassword
)

func (a *App) findConnection(name string) (config.Connection, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg == nil {
		return config.Connection{}, fmt.Errorf("gui: no config loaded")
	}
	for _, c := range a.cfg.Connections {
		if c.Name == name {
			return c, nil
		}
	}
	return config.Connection{}, fmt.Errorf("gui: no connection named %q", name)
}

// --- Connection CRUD ---

// ListConnections returns the saved connections (password never included —
// config.Connection structurally has no password field).
func (a *App) ListConnections() ([]config.Connection, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg == nil {
		return nil, fmt.Errorf("gui: no config loaded")
	}
	out := make([]config.Connection, len(a.cfg.Connections))
	copy(out, a.cfg.Connections)
	return out, nil
}

// SaveConnection creates or updates a connection. originalName == "" means
// "new"; non-empty means "edit" (and may rename, if conn.Name differs).
// password == "" on an edit means "leave the stored password untouched" —
// if the edit also renames the connection, the untouched password is
// migrated to the new keyring key rather than orphaned under the old name.
func (a *App) SaveConnection(originalName string, conn config.Connection, password string) error {
	if conn.Name == "" {
		return fmt.Errorf("gui: connection name is required")
	}

	a.mu.Lock()
	if a.cfg == nil {
		a.mu.Unlock()
		return fmt.Errorf("gui: no config loaded")
	}
	a.cfg.Connections = upsertConnection(a.cfg.Connections, conn, originalName)
	cfg := a.cfg
	a.mu.Unlock()

	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("gui: save config: %w", err)
	}

	renamed := originalName != "" && originalName != conn.Name
	switch {
	case password != "":
		if err := secretsSetPassword(conn.Name, password); err != nil {
			return fmt.Errorf("gui: save password: %w", err)
		}
	case renamed:
		if oldPw, err := secretsGetPassword(originalName); err == nil {
			if err := secretsSetPassword(conn.Name, oldPw); err != nil {
				return fmt.Errorf("gui: migrate password: %w", err)
			}
		}
	}
	if renamed {
		_ = secretsDeletePassword(originalName)
	}
	return nil
}

// DeleteConnection removes a connection and its stored password.
func (a *App) DeleteConnection(name string) error {
	a.mu.Lock()
	if a.cfg == nil {
		a.mu.Unlock()
		return fmt.Errorf("gui: no config loaded")
	}
	a.cfg.Connections = removeConnectionByName(a.cfg.Connections, name)
	cfg := a.cfg
	a.mu.Unlock()

	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("gui: save config: %w", err)
	}
	if err := secretsDeletePassword(name); err != nil && err != secrets.ErrNotFound {
		if a.logger != nil {
			a.logger.Log("delete " + name + ": password delete failed: " + err.Error())
		}
	}
	return nil
}

// upsertConnection returns a new slice with c inserted, or replacing an
// existing entry: by originalName if given (an edit, possibly a rename), or
// by c.Name otherwise (so re-saving under the same name never duplicates).
func upsertConnection(conns []config.Connection, c config.Connection, originalName string) []config.Connection {
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

// removeConnectionByName returns a new slice with the named connection
// removed. A no-op copy if no connection with that name exists.
func removeConnectionByName(conns []config.Connection, name string) []config.Connection {
	out := make([]config.Connection, 0, len(conns))
	for _, c := range conns {
		if c.Name != name {
			out = append(out, c)
		}
	}
	return out
}

// --- Connect / disconnect ---

// Connect drives the full connect flow for name: look up the saved
// connection, start its kubectl tunnel if configured, dial the store, and
// install the result as the single active connection. Emits a "status"
// runtime event at every transition (tunnel connecting/up, db connecting,
// connected/error) so the frontend can show live progress instead of
// polling during a slow connect.
func (a *App) Connect(name string) error {
	conn, err := a.findConnection(name)
	if err != nil {
		a.setActive(&ActiveConnection{Status: StatusError, LastError: err})
		return err
	}

	a.setActive(&ActiveConnection{Conn: conn, Status: StatusTunnelConnecting})

	password, err := secretsGetPassword(name)
	if err != nil && err != secrets.ErrNotFound {
		a.setActive(&ActiveConnection{Conn: conn, Status: StatusError, LastError: err})
		return err
	}

	dialConn := conn
	var tun *tunnel.Tunnel
	ctx := context.Background()
	if conn.Tunnel != nil {
		tun, err = tunnel.Start(ctx, *conn.Tunnel, a.logger, tunnel.Options{})
		if err != nil {
			a.setActive(&ActiveConnection{Conn: conn, Status: StatusError, LastError: err})
			return err
		}
		dialConn.Host = "localhost"
		dialConn.Port = tun.LocalPort
		a.setActive(&ActiveConnection{Conn: conn, Status: StatusTunnelUp, TunnelProc: tun})
	}

	a.setActive(&ActiveConnection{Conn: conn, Status: StatusDBConnecting, TunnelProc: tun})

	dialCtx, cancel := context.WithTimeout(ctx, dialConn.QueryTimeout())
	defer cancel()
	client, err := db.NewClient(dialCtx, dialConn, password)
	if err != nil {
		if tun != nil {
			tun.Stop()
		}
		a.setActive(&ActiveConnection{Conn: conn, Status: StatusError, LastError: err})
		return err
	}

	a.setActive(&ActiveConnection{
		Conn:        conn,
		Status:      StatusConnected,
		TunnelProc:  tun,
		Client:      client,
		ConnectedAt: time.Now(),
	})
	return nil
}

// Disconnect tears down the active connection's client and tunnel (if any)
// and resets to StatusDisconnected. Safe to call when nothing is connected.
func (a *App) Disconnect() error {
	ac := a.getActive()
	if ac == nil {
		a.setActive(&ActiveConnection{Status: StatusDisconnected})
		return nil
	}

	var err error
	if ac.Client != nil {
		err = ac.Client.Close()
	}
	if ac.TunnelProc != nil {
		if stopErr := ac.TunnelProc.Stop(); stopErr != nil && err == nil {
			err = stopErr
		}
	}
	a.mu.Lock()
	a.lastResult = nil
	a.lastDocs = nil
	a.mu.Unlock()
	a.setActive(&ActiveConnection{Status: StatusDisconnected})
	return err
}
