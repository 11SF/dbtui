package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dbtui/internal/config"
)

// fakeRunner implements the unexported runner interface without ever
// spawning a real process, per the test spec's implementation note for §4.
type fakeRunner struct {
	mu             sync.Mutex
	startErr       error
	killCalls      int
	terminateCalls int
	terminateExits bool // if true, Terminate() simulates the process exiting
	waitCh         chan struct{}
	waitClosed     bool
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{waitCh: make(chan struct{})}
}

func (f *fakeRunner) Start() error { return f.startErr }

func (f *fakeRunner) Wait() error {
	<-f.waitCh
	return nil
}

func (f *fakeRunner) Kill() error {
	f.mu.Lock()
	f.killCalls++
	f.mu.Unlock()
	f.simulateExit()
	return nil
}

func (f *fakeRunner) Terminate() error {
	f.mu.Lock()
	f.terminateCalls++
	exits := f.terminateExits
	f.mu.Unlock()
	if exits {
		f.simulateExit()
	}
	return nil
}

func (f *fakeRunner) SetStderr(w io.Writer) {}

func (f *fakeRunner) simulateExit() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.waitClosed {
		f.waitClosed = true
		close(f.waitCh)
	}
}

func (f *fakeRunner) killCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.killCalls
}

func (f *fakeRunner) terminateCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.terminateCalls
}

// fakeOptions wires Options to use fake instead of spawning a real kubectl
// process.
func fakeOptions(fake *fakeRunner) Options {
	return Options{
		newRunner: func(ctx context.Context, cfg config.TunnelConfig, localPort int) runner {
			return fake
		},
	}
}

func TestTunnel_FreePortAllocation(t *testing.T) {
	seen := make(map[int]bool)
	for i := 0; i < 20; i++ {
		port, err := PickFreePort()
		if err != nil {
			t.Fatalf("PickFreePort() error = %v", err)
		}
		if port <= 0 {
			t.Fatalf("PickFreePort() = %d, want > 0", port)
		}
		if seen[port] {
			t.Fatalf("PickFreePort() returned duplicate port %d on iteration %d", port, i)
		}
		seen[port] = true
	}
}

func TestTunnel_Start_Success(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	port := ln.Addr().(*net.TCPAddr).Port
	fake := newFakeRunner()
	cfg := config.TunnelConfig{LocalPort: port, TargetType: "svc", TargetName: "postgres", RemotePort: 5432, Namespace: "default"}

	tun, err := Start(context.Background(), cfg, io.Discard, fakeOptions(fake))
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if tun.LocalPort != port {
		t.Fatalf("LocalPort = %d, want %d", tun.LocalPort, port)
	}
	tun.Stop()
}

func TestTunnel_Start_TimeoutIfPortNeverOpens(t *testing.T) {
	port, err := PickFreePort()
	if err != nil {
		t.Fatalf("PickFreePort() error = %v", err)
	}
	fake := newFakeRunner()
	cfg := config.TunnelConfig{LocalPort: port, TargetType: "svc", TargetName: "postgres", RemotePort: 5432, Namespace: "default"}

	opts := fakeOptions(fake)
	opts.ConnectTimeout = 30 * time.Millisecond
	opts.PollInterval = 5 * time.Millisecond
	opts.DialTimeout = 5 * time.Millisecond

	_, err = Start(context.Background(), cfg, io.Discard, opts)
	if err == nil {
		t.Fatalf("Start() error = nil, want timeout error")
	}
	if !errors.Is(err, ErrConnectTimeout) {
		t.Fatalf("Start() error = %v, want it to wrap ErrConnectTimeout", err)
	}
	if got := fake.killCount(); got != 1 {
		t.Fatalf("Kill() called %d times, want exactly 1", got)
	}
}

func startFakeTunnel(t *testing.T, fake *fakeRunner, opts Options) *Tunnel {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	port := ln.Addr().(*net.TCPAddr).Port
	cfg := config.TunnelConfig{LocalPort: port, TargetType: "svc", TargetName: "postgres", RemotePort: 5432, Namespace: "default"}
	opts.newRunner = func(ctx context.Context, cfg config.TunnelConfig, localPort int) runner { return fake }

	tun, err := Start(context.Background(), cfg, io.Discard, opts)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	return tun
}

func TestTunnel_Stop_GracefulThenForceKill(t *testing.T) {
	fake := newFakeRunner()
	fake.terminateExits = false // SIGTERM never causes it to exit

	opts := Options{StopGrace: 20 * time.Millisecond}
	tun := startFakeTunnel(t, fake, opts)

	done := make(chan error, 1)
	go func() { done <- tun.Stop() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Stop() hung past its grace period")
	}

	if got := fake.terminateCount(); got != 1 {
		t.Fatalf("Terminate() called %d times, want 1", got)
	}
	if got := fake.killCount(); got != 1 {
		t.Fatalf("Kill() called %d times, want 1 (force kill after grace period)", got)
	}
}

func TestTunnel_IsAlive(t *testing.T) {
	fake := newFakeRunner()
	tun := startFakeTunnel(t, fake, Options{})

	if !tun.IsAlive() {
		t.Fatalf("IsAlive() = false immediately after Start(), want true")
	}

	fake.simulateExit() // process dies on its own, not via Stop()
	<-tun.exitedCh      // deterministic wait for the monitor goroutine to observe it

	if tun.IsAlive() {
		t.Fatalf("IsAlive() = true after the process exited, want false")
	}
}

type fakeClock struct {
	mu     sync.Mutex
	sleeps []time.Duration
}

func (c *fakeClock) Sleep(d time.Duration) {
	c.mu.Lock()
	c.sleeps = append(c.sleeps, d)
	c.mu.Unlock()
}

func (c *fakeClock) recorded() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]time.Duration, len(c.sleeps))
	copy(out, c.sleeps)
	return out
}

func TestTunnel_AutoReconnect_RetriesWithBackoff(t *testing.T) {
	clock := &fakeClock{}
	var calls int32

	startFn := func(ctx context.Context) (*Tunnel, error) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			return nil, fmt.Errorf("attempt %d failed", n)
		}
		return &Tunnel{}, nil
	}

	status, attempts, tun := AutoReconnect(context.Background(), startFn, clock)

	if status != StatusConnected {
		t.Fatalf("status = %v, want StatusConnected", status)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
	if tun == nil {
		t.Fatalf("tun = nil, want non-nil on success")
	}
	if int32(attempts) != calls {
		t.Fatalf("attempts = %d, but startFn was called %d times", attempts, calls)
	}

	want := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}
	got := clock.recorded()
	if len(got) != len(want) {
		t.Fatalf("clock.Sleep called with %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("backoff[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestTunnel_AutoReconnect_GivesUpAfterMaxRetries(t *testing.T) {
	clock := &fakeClock{}
	var calls int32

	startFn := func(ctx context.Context) (*Tunnel, error) {
		atomic.AddInt32(&calls, 1)
		return nil, errors.New("always fails")
	}

	status, attempts, tun := AutoReconnect(context.Background(), startFn, clock)

	if status != StatusError {
		t.Fatalf("status = %v, want StatusError", status)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
	if tun != nil {
		t.Fatalf("tun = %v, want nil after giving up", tun)
	}
	if calls != 3 {
		t.Fatalf("startFn called %d times, want exactly 3 (no further attempts)", calls)
	}
}
