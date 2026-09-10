// Package app owns the top-level tview application shell: the root layout
// (status bar + page area + command line + hotkey bar), global state (the
// single active connection, per spec §3.2's "one active connection at a
// time" v1 scope), and the view-history stack driven by Esc.
package app

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"dbtui/internal/config"
	"dbtui/internal/db"
	"dbtui/internal/history"
	"dbtui/internal/logging"
	"dbtui/internal/tunnel"
)

// ConnStatus is the lifecycle state of the single active connection.
type ConnStatus int

const (
	StatusDisconnected ConnStatus = iota
	StatusTunnelConnecting
	StatusTunnelUp
	StatusDBConnecting
	StatusConnected
	StatusError
)

// ActiveConnection is the app's single in-flight/established connection.
type ActiveConnection struct {
	Conn        config.Connection
	Status      ConnStatus
	TunnelProc  *tunnel.Tunnel // nil if not using a tunnel
	Client      db.Client      // nil until StatusConnected
	LastError   error
	ConnectedAt time.Time
}

// App is the root of the tview application: owns the tview.Application, the
// Pages that views swap into, global state, and the shared history/logging
// stores every view reads from.
type App struct {
	TView *tview.Application
	Pages *tview.Pages

	Config  *config.ConfigFile
	History *history.Store
	Logger  *logging.RingBuffer

	StatusBar   *StatusBar
	CommandLine *tview.InputField
	HotkeyBar   *tview.TextView
	Root        *tview.Flex

	mu     sync.Mutex
	Active *ActiveConnection

	navMu    sync.Mutex
	navStack []string

	commandLineVisible bool
	quitConfirm        bool

	// OnCommand is invoked with the parsed command name (e.g. "conn",
	// "tables", "q") when the user submits the ":" command line. Wired by
	// main.go once every page is registered, so app itself stays decoupled
	// from the views package (avoids an app<->views import cycle).
	OnCommand func(cmd string)

	// watchdogCancel stops the connection watchdog goroutine started by
	// StartWatchdog, if any is running.
	watchdogCancel context.CancelFunc
}

// NewApp builds the tview.Application, root layout, and Pages, but does not
// register any page content — callers register pages and call Run().
func NewApp(cfg *config.ConfigFile, hist *history.Store, logger *logging.RingBuffer) *App {
	a := &App{
		TView:   tview.NewApplication(),
		Pages:   tview.NewPages(),
		Config:  cfg,
		History: hist,
		Logger:  logger,
		Active:  &ActiveConnection{Status: StatusDisconnected},
	}

	a.StatusBar = NewStatusBar()
	a.CommandLine = tview.NewInputField().SetLabel(":")
	a.HotkeyBar = tview.NewTextView().SetDynamicColors(true)
	a.HotkeyBar.SetText(defaultHotkeyText)

	a.CommandLine.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			a.submitCommand(a.CommandLine.GetText())
		}
		a.hideCommandLine()
	})

	a.Root = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.StatusBar.View, 1, 0, false).
		AddItem(a.Pages, 0, 1, true).
		AddItem(a.HotkeyBar, 1, 0, false)

	a.TView.SetInputCapture(a.globalInputCapture)
	a.TView.SetRoot(a.Root, true)

	return a
}

// RegisterPage adds a named page to Pages and to the navigable set.
func (a *App) RegisterPage(name string, primitive tview.Primitive) {
	a.Pages.AddPage(name, primitive, true, false)
}

// SwitchToPage pushes the current page (if any) onto the back-stack and
// switches to name.
func (a *App) SwitchToPage(name string) {
	a.navMu.Lock()
	cur, _ := a.Pages.GetFrontPage()
	if cur != "" && cur != name {
		a.navStack = append(a.navStack, cur)
	}
	a.navMu.Unlock()
	a.Pages.SwitchToPage(name)
}

// GoBack pops the nav stack and switches to the previous page, if any.
// Returns false if the stack was empty (nothing to go back to).
func (a *App) GoBack() bool {
	a.navMu.Lock()
	if len(a.navStack) == 0 {
		a.navMu.Unlock()
		return false
	}
	prev := a.navStack[len(a.navStack)-1]
	a.navStack = a.navStack[:len(a.navStack)-1]
	a.navMu.Unlock()
	a.Pages.SwitchToPage(prev)
	return true
}

// SetActive atomically replaces the active connection state.
func (a *App) SetActive(ac *ActiveConnection) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Active = ac
}

// GetActive returns a snapshot of the active connection pointer (the struct
// it points to may still be mutated by the watchdog; callers reading fields
// off it should treat it as best-effort/eventually-consistent, matching the
// TUI's own display-refresh cadence).
func (a *App) GetActive() *ActiveConnection {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.Active
}

// Run starts the tview event loop. Never panics on its own account (spec
// §7) — any setup error must be surfaced before Run is called.
func (a *App) Run() error {
	return a.TView.Run()
}

// Stop shuts down the tview event loop and the connection watchdog, if any.
func (a *App) Stop() {
	if a.watchdogCancel != nil {
		a.watchdogCancel()
	}
	a.TView.Stop()
}

func (a *App) submitCommand(text string) {
	cmd := ParseCommand(text)
	if cmd == "" {
		return
	}
	if cmd == "q" {
		a.confirmQuit()
		return
	}
	if a.OnCommand != nil {
		a.OnCommand(cmd)
	}
}

// ParseCommand extracts the command token from raw ":"-line input, e.g.
// "conn" from " conn " or "conn" from ":conn". Pure and independently
// testable — no tview dependency.
func ParseCommand(raw string) string {
	s := raw
	for len(s) > 0 && (s[0] == ':' || s[0] == ' ') {
		s = s[1:]
	}
	end := len(s)
	for i, r := range s {
		if r == ' ' {
			end = i
			break
		}
	}
	return s[:end]
}

func (a *App) showCommandLine() {
	a.commandLineVisible = true
	a.CommandLine.SetText("")
	a.Root.RemoveItem(a.HotkeyBar)
	a.Root.AddItem(a.CommandLine, 1, 0, true)
	a.Root.AddItem(a.HotkeyBar, 1, 0, false)
	a.TView.SetFocus(a.CommandLine)
}

func (a *App) hideCommandLine() {
	if !a.commandLineVisible {
		return
	}
	a.commandLineVisible = false
	a.Root.RemoveItem(a.CommandLine)
	a.TView.SetFocus(a.Pages)
}

func (a *App) confirmQuit() {
	a.Stop()
}

// Disconnect tears down the active connection's client and tunnel (if any)
// and resets Active to StatusDisconnected. Safe to call when nothing is
// connected (spec §5.3's "d disconnect" keybind, reachable regardless of
// current state).
func (a *App) Disconnect() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.Active == nil {
		a.Active = &ActiveConnection{Status: StatusDisconnected}
		return nil
	}

	var err error
	if a.Active.Client != nil {
		err = a.Active.Client.Close()
	}
	if a.Active.TunnelProc != nil {
		if stopErr := a.Active.TunnelProc.Stop(); stopErr != nil && err == nil {
			err = stopErr
		}
	}
	a.Active = &ActiveConnection{Status: StatusDisconnected}
	return err
}

const defaultHotkeyText = "[yellow]<:>[white] cmd  [yellow]</>[white] filter  [yellow]<esc>[white] back  [yellow]<q>[white] quit"

func (a *App) globalInputCapture(event *tcell.EventKey) *tcell.EventKey {
	if a.commandLineVisible {
		return event // let the InputField handle its own keys
	}

	switch event.Rune() {
	case ':':
		a.showCommandLine()
		return nil
	}

	switch event.Key() {
	case tcell.KeyEsc:
		a.GoBack()
		return nil
	case tcell.KeyCtrlC:
		a.confirmQuit()
		return nil
	}

	return event
}

// StartWatchdog polls the active tunnel's IsAlive() every interval; if it
// dies while Active.Status == StatusConnected, it drives
// tunnel.AutoReconnect (spec §4.2's documented 1s/2s/4s backoff, owned by
// the tunnel package) and updates Active.Status accordingly. Returns
// immediately; the watchdog runs until ctx is cancelled or Stop() is called.
func (a *App) StartWatchdog(ctx context.Context, interval time.Duration, startFn func(ctx context.Context) (*tunnel.Tunnel, error), clock tunnel.Clock) {
	ctx, cancel := context.WithCancel(ctx)
	a.watchdogCancel = cancel

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ac := a.GetActive()
				if ac == nil || ac.TunnelProc == nil || ac.Status != StatusConnected {
					continue
				}
				if ac.TunnelProc.IsAlive() {
					continue
				}
				status, _, tun := tunnel.AutoReconnect(ctx, startFn, clock)
				a.mu.Lock()
				if status == tunnel.StatusConnected {
					a.Active.TunnelProc = tun
					a.Active.Status = StatusConnected
				} else {
					a.Active.Status = StatusError
					a.Active.LastError = fmt.Errorf("tunnel: auto-reconnect exhausted retries")
				}
				a.mu.Unlock()
			}
		}
	}()
}
