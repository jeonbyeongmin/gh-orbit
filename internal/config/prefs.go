package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Prefs is the in-memory representation of $XDG_CONFIG_HOME/gh-orbit/config.toml.
// All fields are optional — a missing file decodes to a zero-value Prefs and
// callers must treat empty strings as "unset, fall back to git config / defaults".
type Prefs struct {
	// Theme is the app-wide color theme key (one of internal/tui's theme table
	// keys, or "" = unset → default). It supersedes the legacy [diff] theme;
	// see ThemeKey for the resolution order.
	Theme string    `toml:"theme,omitempty"`
	Pull  PullPrefs `toml:"pull"`
	Diff  DiffPrefs `toml:"diff"`
}

// ThemeKey resolves the configured theme key, preferring the top-level `theme`
// and falling back to the legacy `[diff] theme` so files written before the
// theme went app-wide keep working. "" means unset (caller defaults).
func (p Prefs) ThemeKey() string {
	if p.Theme != "" {
		return p.Theme
	}
	return p.Diff.Theme
}

// PullPrefs mirrors the [pull] section. Strategy is one of "ff-only", "merge",
// "rebase", or "" (unset). Unknown values are kept as-is here; the caller
// (typically internal/git.ResolvePullStrategy) decides whether to map or fall
// through.
type PullPrefs struct {
	Strategy string `toml:"strategy,omitempty"`
}

// DiffPrefs mirrors the [diff] section. Theme is the legacy theme key, kept so
// pre-existing config files still load (see Prefs.ThemeKey); new writes go to
// the top-level Prefs.Theme and clear this. One of internal/tui's theme table
// keys (see docs/config.md) or "" (unset → default). Unknown values fall through
// to the default.
type DiffPrefs struct {
	Theme string `toml:"theme,omitempty"`
}

// LoadPrefs reads the user's preferences file from ConfigPath. A missing file
// is not an error — it returns zero-value Prefs. Decode errors (malformed
// TOML, type mismatches) are returned so the caller can surface them once
// without aborting the program.
func LoadPrefs() (Prefs, error) {
	path, err := ConfigPath()
	if err != nil {
		return Prefs{}, err
	}
	var prefs Prefs
	if _, err := toml.DecodeFile(path, &prefs); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Prefs{}, nil
		}
		return Prefs{}, fmt.Errorf("decode %s: %w", path, err)
	}
	return prefs, nil
}

// SavePrefs writes prefs to ConfigPath as TOML, creating the config dir if it
// doesn't exist yet. The whole file is rewritten from the struct, so the caller
// must pass a Prefs carrying every setting it wants to keep — hand-written
// comments and unknown keys are not preserved (the same keys LoadPrefs ignores).
// The write is atomic: it lands in a temp file in the same dir, then renames
// over config.toml so a crash mid-write can't truncate the existing file.
func SavePrefs(prefs Prefs) error {
	dir, err := ConfigDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "config-*.toml")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op once the rename succeeds
	if err := toml.NewEncoder(tmp).Encode(prefs); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, filepath.Join(dir, "config.toml"))
}
