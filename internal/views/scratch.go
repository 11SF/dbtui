package views

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const scratchFileName = "scratch.sql"

// ScratchPath returns the path to the SQL-mode :query scratch buffer (spec
// §5.6: "Auto-saves text to ~/.config/dbtui/scratch.sql on a 500ms
// debounce, to survive a crash"). Resolved via os.UserConfigDir(), the same
// pattern internal/config.Path() uses, so it respects HOME/XDG_CONFIG_HOME
// and is equally testable via t.Setenv.
func ScratchPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("views: resolve user config dir: %w", err)
	}
	return filepath.Join(dir, "dbtui", scratchFileName), nil
}

// LoadScratch reads the scratch buffer's contents, returning "" (no error)
// if the file doesn't exist yet — the common case on a first run or after a
// clean quit that intentionally left nothing to restore.
func LoadScratch() (string, error) {
	path, err := ScratchPath()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("views: read scratch buffer: %w", err)
	}
	return string(data), nil
}

// SaveScratch overwrites the scratch buffer with text, creating the parent
// config directory if needed.
func SaveScratch(text string) error {
	path, err := ScratchPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("views: create config dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		return fmt.Errorf("views: write scratch buffer: %w", err)
	}
	return nil
}

// scratchDebouncer coalesces rapid successive Trigger calls (one per
// keystroke) into a single save after delay has elapsed with no further
// triggers, so a fast typist doesn't hit disk on every character — spec
// §5.6's "500ms debounce". A time.Timer reset on each Trigger, not a
// per-keystroke time.Sleep, so triggering never blocks the caller.
type scratchDebouncer struct {
	delay time.Duration
	save  func(text string) error
	timer *time.Timer
}

func newScratchDebouncer(delay time.Duration, save func(text string) error) *scratchDebouncer {
	return &scratchDebouncer{delay: delay, save: save}
}

// Trigger (re)starts the debounce timer. When it finally fires with no
// intervening Trigger call, save(getText()) runs — getText is called lazily
// at fire time (not at Trigger time) so the saved content reflects the
// latest text even if more keystrokes land before the timer settles.
func (d *scratchDebouncer) Trigger(getText func() string) {
	if d.timer != nil {
		d.timer.Stop()
	}
	d.timer = time.AfterFunc(d.delay, func() {
		_ = d.save(getText())
	})
}

// Stop cancels any pending debounced save (e.g. on a clean quit, where
// there's nothing left to coalesce).
func (d *scratchDebouncer) Stop() {
	if d.timer != nil {
		d.timer.Stop()
	}
}
