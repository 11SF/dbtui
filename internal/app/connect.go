package app

import (
	"context"
	"fmt"
	"time"

	"dbtui/internal/config"
	"dbtui/internal/db"
	"dbtui/internal/secrets"
	"dbtui/internal/tunnel"
)

// GetPassword is overridable for tests (secrets.GetPassword hits an
// injectable backend already; this indirection additionally lets app-level
// tests avoid importing the real keyring package's default backend).
var GetPassword = secrets.GetPassword

// FindConnection returns the named connection from cfg, or an error if no
// connection with that name exists. Pure/testable without tview.
func FindConnection(cfg *config.ConfigFile, name string) (config.Connection, error) {
	if cfg == nil {
		return config.Connection{}, fmt.Errorf("app: no config loaded")
	}
	for _, c := range cfg.Connections {
		if c.Name == name {
			return c, nil
		}
	}
	return config.Connection{}, fmt.Errorf("app: no connection named %q", name)
}

// Connect drives the full connect flow for connName (spec §5.3's "Enter
// connect"): look up the saved connection, start its kubectl tunnel if
// configured, dial the store, and install the result as the app's single
// active connection (spec §3.2: one active connection at a time in v1).
// Status transitions through StatusTunnelConnecting/StatusTunnelUp/
// StatusDBConnecting to StatusConnected or StatusError as it goes, so a
// caller redrawing the status bar after each step reflects real progress.
func (a *App) Connect(ctx context.Context, connName string) error {
	conn, err := FindConnection(a.Config, connName)
	if err != nil {
		a.SetActive(&ActiveConnection{Status: StatusError, LastError: err})
		return err
	}

	a.SetActive(&ActiveConnection{Conn: conn, Status: StatusTunnelConnecting})

	password, err := GetPassword(connName)
	if err != nil && err != secrets.ErrNotFound {
		a.SetActive(&ActiveConnection{Conn: conn, Status: StatusError, LastError: err})
		return err
	}

	dialConn := conn
	var tun *tunnel.Tunnel
	if conn.Tunnel != nil {
		tun, err = tunnel.Start(ctx, *conn.Tunnel, a.Logger, tunnel.Options{})
		if err != nil {
			a.SetActive(&ActiveConnection{Conn: conn, Status: StatusError, LastError: err})
			return err
		}
		dialConn.Host = "localhost"
		dialConn.Port = tun.LocalPort
		a.SetActive(&ActiveConnection{Conn: conn, Status: StatusTunnelUp, TunnelProc: tun})
	}

	a.SetActive(&ActiveConnection{Conn: conn, Status: StatusDBConnecting, TunnelProc: tun})

	dialCtx, cancel := context.WithTimeout(ctx, dialConn.QueryTimeout())
	defer cancel()
	client, err := db.NewClient(dialCtx, dialConn, password)
	if err != nil {
		if tun != nil {
			tun.Stop()
		}
		a.SetActive(&ActiveConnection{Conn: conn, Status: StatusError, LastError: err})
		return err
	}

	a.SetActive(&ActiveConnection{
		Conn:        conn,
		Status:      StatusConnected,
		TunnelProc:  tun,
		Client:      client,
		ConnectedAt: time.Now(),
	})
	return nil
}
