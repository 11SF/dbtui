// Package tunnel manages a kubectl port-forward subprocess used to reach a
// database that would otherwise require shelling into a pod first.
package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"dbtui/internal/config"
)

// runner abstracts the subprocess a Tunnel drives, so tests can inject a
// fake that never actually spawns kubectl. Exactly one goroutine may call
// Wait() (standard os/exec contract), and Tunnel itself owns that call.
type runner interface {
	Start() error
	Wait() error
	Kill() error
	Terminate() error // best-effort graceful stop (SIGTERM on unix)
	SetStderr(w io.Writer)
}

// execRunner is the real runner, backed by os/exec + kubectl.
type execRunner struct {
	cmd *exec.Cmd
}

func newExecRunner(ctx context.Context, cfg config.TunnelConfig, localPort int) *execRunner {
	target := cfg.TargetType + "/" + cfg.TargetName
	args := []string{"port-forward", target, fmt.Sprintf("%d:%d", localPort, cfg.RemotePort), "-n", cfg.Namespace}
	if cfg.KubeContext != "" {
		args = append(args, "--context", cfg.KubeContext)
	}
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	return &execRunner{cmd: cmd}
}

func (r *execRunner) Start() error          { return r.cmd.Start() }
func (r *execRunner) Wait() error           { return r.cmd.Wait() }
func (r *execRunner) SetStderr(w io.Writer) { r.cmd.Stderr = w }

func (r *execRunner) Kill() error {
	if r.cmd.Process == nil {
		return nil
	}
	return r.cmd.Process.Kill()
}

// Terminate sends SIGTERM, which is portable across the spec'd target
// platforms (macOS, Linux) — this is the one syscall-level call the spec
// itself calls for (§4.2: "cmd.Process.Signal(syscall.SIGTERM)").
func (r *execRunner) Terminate() error {
	if r.cmd.Process == nil {
		return nil
	}
	return r.cmd.Process.Signal(syscall.SIGTERM)
}

// PickFreePort asks the OS for an unused TCP port by binding to :0 and
// immediately releasing it.
func PickFreePort() (int, error) {
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		return 0, fmt.Errorf("tunnel: pick free port: %w", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// Clock abstracts time.Sleep so tests can drive backoff/polling
// deterministically instead of waiting on the wall clock.
type Clock interface {
	Sleep(d time.Duration)
}

type realClock struct{}

func (realClock) Sleep(d time.Duration) { time.Sleep(d) }

// Options configures timing knobs that production code leaves at their
// zero value (defaulted below) and tests override to keep runs fast and
// deterministic.
type Options struct {
	ConnectTimeout time.Duration // default 10s: how long to wait for the local port to open
	PollInterval   time.Duration // default 300ms: how often to retry dialing the local port
	DialTimeout    time.Duration // default 500ms: per-attempt dial timeout
	StopGrace      time.Duration // default 3s: SIGTERM grace period before Stop() force-kills
	Clock          Clock         // default realClock{}

	newRunner func(ctx context.Context, cfg config.TunnelConfig, localPort int) runner
	dialFunc  func(network, addr string, timeout time.Duration) (net.Conn, error)
}

func (o Options) withDefaults() Options {
	if o.ConnectTimeout == 0 {
		o.ConnectTimeout = 10 * time.Second
	}
	if o.PollInterval == 0 {
		o.PollInterval = 300 * time.Millisecond
	}
	if o.DialTimeout == 0 {
		o.DialTimeout = 500 * time.Millisecond
	}
	if o.StopGrace == 0 {
		o.StopGrace = 3 * time.Second
	}
	if o.Clock == nil {
		o.Clock = realClock{}
	}
	if o.newRunner == nil {
		o.newRunner = func(ctx context.Context, cfg config.TunnelConfig, localPort int) runner {
			return newExecRunner(ctx, cfg, localPort)
		}
	}
	if o.dialFunc == nil {
		o.dialFunc = func(network, addr string, timeout time.Duration) (net.Conn, error) {
			return net.DialTimeout(network, addr, timeout)
		}
	}
	return o
}

// ErrConnectTimeout means the local port never started accepting
// connections within Options.ConnectTimeout.
var ErrConnectTimeout = errors.New("tunnel: timeout waiting for local port to open")

// Tunnel represents a live kubectl port-forward subprocess.
type Tunnel struct {
	run       runner
	LocalPort int
	opts      Options

	mu       sync.Mutex
	exited   bool
	exitedCh chan struct{}
}

// Start launches the tunnel and blocks until the local port is accepting
// connections, or opts.ConnectTimeout elapses (in which case the subprocess
// is killed and an error wrapping ErrConnectTimeout is returned).
func Start(ctx context.Context, cfg config.TunnelConfig, logWriter io.Writer, opts Options) (*Tunnel, error) {
	opts = opts.withDefaults()

	localPort := cfg.LocalPort
	if localPort == 0 {
		p, err := PickFreePort()
		if err != nil {
			return nil, err
		}
		localPort = p
	}

	run := opts.newRunner(ctx, cfg, localPort)
	run.SetStderr(logWriter)

	if err := run.Start(); err != nil {
		return nil, fmt.Errorf("tunnel: start kubectl: %w", err)
	}

	addr := fmt.Sprintf("localhost:%d", localPort)
	deadline := time.Now().Add(opts.ConnectTimeout)
	for {
		conn, err := opts.dialFunc("tcp", addr, opts.DialTimeout)
		if err == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) {
			run.Kill()
			return nil, fmt.Errorf("%w: %s did not open within %s", ErrConnectTimeout, addr, opts.ConnectTimeout)
		}
		opts.Clock.Sleep(opts.PollInterval)
	}

	t := &Tunnel{run: run, LocalPort: localPort, opts: opts, exitedCh: make(chan struct{})}
	go func() {
		run.Wait()
		t.mu.Lock()
		t.exited = true
		t.mu.Unlock()
		close(t.exitedCh)
	}()
	return t, nil
}

// Stop gracefully terminates the tunnel (SIGTERM), force-killing it if it
// hasn't exited within opts.StopGrace. Never hangs past StopGrace.
func (t *Tunnel) Stop() error {
	if err := t.run.Terminate(); err != nil {
		return err
	}

	select {
	case <-t.exitedCh:
		return nil
	case <-time.After(t.opts.StopGrace):
		if err := t.run.Kill(); err != nil {
			return err
		}
		<-t.exitedCh
		return nil
	}
}

// IsAlive reports whether the underlying subprocess is still running.
func (t *Tunnel) IsAlive() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return !t.exited
}

// ReconnectStatus is tunnel's own minimal connection-state signal for the
// auto-reconnect retry loop. Kept local (rather than importing
// internal/app's ConnStatus) because tunnel is a lower-level package that
// app depends on, not the other way around — app's own connection watchdog
// (spec §4.2: poll IsAlive() every 2s, retry on death) maps this onto
// StatusConnected/StatusError when it drives AutoReconnect.
type ReconnectStatus int

const (
	StatusConnected ReconnectStatus = iota
	StatusError
)

// Backoff is the documented exponential backoff schedule for auto-reconnect
// (spec §4.2 and §10): a delay precedes each of the (up to) 3 retry
// attempts, so all three intervals are consumed on a run that only
// succeeds on the final attempt.
var Backoff = []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}

// AutoReconnect retries startFn up to len(Backoff) times, sleeping (via
// clock) the next backoff interval before each attempt. It returns as soon
// as an attempt succeeds; if every attempt fails it returns StatusError
// after exhausting the schedule, making no further attempts.
func AutoReconnect(ctx context.Context, startFn func(ctx context.Context) (*Tunnel, error), clock Clock) (ReconnectStatus, int, *Tunnel) {
	attempts := 0
	for _, delay := range Backoff {
		clock.Sleep(delay)
		attempts++
		tun, err := startFn(ctx)
		if err == nil {
			return StatusConnected, attempts, tun
		}
	}
	return StatusError, attempts, nil
}
