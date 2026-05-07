// Package config resolves XDG paths and opens the debug log file used by the TUI.
package config

import (
	"os"
	"path/filepath"
)

const appName = "gh-orbit"

// xdgDir resolves an XDG Base Directory: if envVar is set, returns
// $envVar/<appName>; otherwise joins the user's home dir with homeFallback
// and appName. Shared by StateDir / ConfigDir.
func xdgDir(envVar string, homeFallback ...string) (string, error) {
	if dir := os.Getenv(envVar); dir != "" {
		return filepath.Join(dir, appName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	parts := append(append([]string{home}, homeFallback...), appName)
	return filepath.Join(parts...), nil
}

// StateDir returns the directory where gh-orbit stores per-user runtime state
// (logs, caches that survive runs): $XDG_STATE_HOME/gh-orbit or
// ~/.local/state/gh-orbit.
func StateDir() (string, error) {
	return xdgDir("XDG_STATE_HOME", ".local", "state")
}

// LogPath returns the absolute path of the debug log file.
func LogPath() (string, error) {
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "log"), nil
}

// ConfigDir returns the directory where gh-orbit looks for the user's
// preferences file: $XDG_CONFIG_HOME/gh-orbit or ~/.config/gh-orbit. Unlike
// StateDir / LogPath, callers do not create the directory eagerly — config
// is optional and a missing file means "use defaults".
func ConfigDir() (string, error) {
	return xdgDir("XDG_CONFIG_HOME", ".config")
}

// ConfigPath returns the absolute path of the TOML preferences file.
func ConfigPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.toml"), nil
}

// OpenLog ensures the state directory exists and opens the log file for
// append. The TUI owns stdout/stderr while running, so all log output must
// go through this file.
func OpenLog() (*os.File, error) {
	dir, err := StateDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(filepath.Join(dir, "log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
}
