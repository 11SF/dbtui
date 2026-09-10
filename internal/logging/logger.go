// Package logging provides an in-memory ring buffer logger that feeds the
// :logs view. It also implements io.Writer so it can be wired directly as
// a subprocess's Stderr (e.g. the kubectl port-forward tunnel).
package logging

import (
	"bytes"
	"sync"
)

// RingBuffer keeps the last N log lines written to it, dropping the oldest
// once capacity is exceeded. Safe for concurrent use.
type RingBuffer struct {
	mu    sync.Mutex
	lines []string
	cap   int
}

// NewRingBuffer creates a ring buffer holding at most capacity lines.
func NewRingBuffer(capacity int) *RingBuffer {
	return &RingBuffer{cap: capacity}
}

// Log appends a single pre-split line.
func (r *RingBuffer) Log(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.append(line)
}

// append must be called with r.mu held.
func (r *RingBuffer) append(line string) {
	r.lines = append(r.lines, line)
	if len(r.lines) > r.cap {
		r.lines = r.lines[len(r.lines)-r.cap:]
	}
}

// Lines returns a copy of the currently buffered lines, oldest first.
func (r *RingBuffer) Lines() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.lines))
	copy(out, r.lines)
	return out
}

// Write implements io.Writer. A single Write call may contain multiple
// newline-terminated lines (as subprocess stderr commonly does); each is
// recorded as its own log line. A trailing partial line (no terminating
// newline) is buffered as its own entry too, so nothing written is lost.
func (r *RingBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, line := range bytes.Split(p, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		r.append(string(line))
	}
	return len(p), nil
}
