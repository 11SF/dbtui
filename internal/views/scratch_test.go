package views

import (
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// isolateScratchDir points os.UserConfigDir() (and therefore ScratchPath)
// at a fresh temp directory, mirroring internal/config's own
// isolateConfigDir test helper so tests never touch a real HOME.
func isolateScratchDir(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, ".config"))
	t.Setenv("USERPROFILE", tmp)
}

func TestScratchPath(t *testing.T) {
	isolateScratchDir(t)
	path, err := ScratchPath()
	if err != nil {
		t.Fatalf("ScratchPath() error = %v", err)
	}
	if filepath.Base(path) != scratchFileName {
		t.Fatalf("ScratchPath() = %q, want basename %q", path, scratchFileName)
	}
	if filepath.Base(filepath.Dir(path)) != "dbtui" {
		t.Fatalf("ScratchPath() = %q, want parent dir named dbtui", path)
	}
}

func TestLoadScratch_MissingFile_ReturnsEmptyNoError(t *testing.T) {
	isolateScratchDir(t)
	got, err := LoadScratch()
	if err != nil {
		t.Fatalf("LoadScratch() error = %v, want nil for a missing file", err)
	}
	if got != "" {
		t.Fatalf("LoadScratch() = %q, want empty", got)
	}
}

func TestSaveLoadScratch_RoundTrip(t *testing.T) {
	isolateScratchDir(t)
	want := "SELECT * FROM users WHERE id = 1;\n-- work in progress"

	if err := SaveScratch(want); err != nil {
		t.Fatalf("SaveScratch() error = %v", err)
	}
	got, err := LoadScratch()
	if err != nil {
		t.Fatalf("LoadScratch() error = %v", err)
	}
	if got != want {
		t.Fatalf("LoadScratch() = %q, want %q", got, want)
	}
}

func TestSaveScratch_OverwritesPreviousContent(t *testing.T) {
	isolateScratchDir(t)
	if err := SaveScratch("first"); err != nil {
		t.Fatalf("SaveScratch() error = %v", err)
	}
	if err := SaveScratch("second"); err != nil {
		t.Fatalf("SaveScratch() error = %v", err)
	}
	got, err := LoadScratch()
	if err != nil {
		t.Fatalf("LoadScratch() error = %v", err)
	}
	if got != "second" {
		t.Fatalf("LoadScratch() = %q, want %q", got, "second")
	}
}

func TestScratchDebouncer_CoalescesRapidTriggers(t *testing.T) {
	var saveCount int32
	var lastSaved atomic.Value
	lastSaved.Store("")

	d := newScratchDebouncer(20*time.Millisecond, func(text string) error {
		atomic.AddInt32(&saveCount, 1)
		lastSaved.Store(text)
		return nil
	})

	// Simulate rapid keystrokes: each Trigger call resets the timer before
	// it fires, so only the last one should ever actually save.
	texts := []string{"S", "SE", "SEL", "SELE", "SELECT"}
	for _, text := range texts {
		text := text
		d.Trigger(func() string { return text })
		time.Sleep(5 * time.Millisecond) // well under the 20ms debounce delay
	}

	time.Sleep(60 * time.Millisecond) // let the final timer fire

	if got := atomic.LoadInt32(&saveCount); got != 1 {
		t.Fatalf("save called %d times, want exactly 1 (rapid triggers should coalesce)", got)
	}
	if got := lastSaved.Load().(string); got != "SELECT" {
		t.Fatalf("saved text = %q, want %q (the latest at fire time)", got, "SELECT")
	}
}

func TestScratchDebouncer_Stop_CancelsPendingSave(t *testing.T) {
	var saveCount int32
	d := newScratchDebouncer(15*time.Millisecond, func(text string) error {
		atomic.AddInt32(&saveCount, 1)
		return nil
	})

	d.Trigger(func() string { return "x" })
	d.Stop()

	time.Sleep(40 * time.Millisecond)
	if got := atomic.LoadInt32(&saveCount); got != 0 {
		t.Fatalf("save called %d times after Stop(), want 0", got)
	}
}

func TestScratchDebouncer_StopBeforeAnyTrigger_NoPanic(t *testing.T) {
	d := newScratchDebouncer(10*time.Millisecond, func(string) error { return nil })
	d.Stop() // must not panic when no timer was ever started
}
