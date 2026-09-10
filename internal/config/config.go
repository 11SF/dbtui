// Package config loads and saves dbtui's connection list.
//
// File location is derived from os.UserConfigDir() + "/dbtui/connections.yaml"
// rather than a hardcoded path, so it respects platform conventions (and the
// HOME/XDG_CONFIG_HOME env vars, which is what makes this testable via
// t.Setenv). On Linux this resolves to ~/.config/dbtui/connections.yaml as
// described in the spec; on macOS os.UserConfigDir() resolves under
// ~/Library/Application Support instead — that's a platform difference in
// the stdlib helper itself, not a deviation from "use os.UserConfigDir(),
// don't hardcode the path".
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const configDirName = "dbtui"
const configFileName = "connections.yaml"

// Path returns the absolute path to connections.yaml.
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("config: resolve user config dir: %w", err)
	}
	return filepath.Join(dir, configDirName, configFileName), nil
}

// Load reads connections.yaml, creating an empty one if it doesn't exist yet.
func Load() (*ConfigFile, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			empty := &ConfigFile{Connections: []Connection{}}
			if err := Save(empty); err != nil {
				return nil, fmt.Errorf("config: create default config: %w", err)
			}
			return empty, nil
		}
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	var cfg ConfigFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		// Deliberately do not touch the file on disk — surface the error so
		// the caller can decide what to do with a malformed config.
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	if cfg.Connections == nil {
		cfg.Connections = []Connection{}
	}
	return &cfg, nil
}

// Save writes cfg to connections.yaml, creating parent directories as needed.
func Save(cfg *ConfigFile) error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("config: create config dir: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("config: marshal: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	return nil
}
