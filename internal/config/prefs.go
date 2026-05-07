package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/BurntSushi/toml"
)

// Prefs is the in-memory representation of $XDG_CONFIG_HOME/gh-orbit/config.toml.
// All fields are optional — a missing file decodes to a zero-value Prefs and
// callers must treat empty strings as "unset, fall back to git config / defaults".
type Prefs struct {
	Pull PullPrefs `toml:"pull"`
}

// PullPrefs mirrors the [pull] section. Strategy is one of "ff-only", "merge",
// "rebase", or "" (unset). Unknown values are kept as-is here; the caller
// (typically internal/git.ResolvePullStrategy) decides whether to map or fall
// through.
type PullPrefs struct {
	Strategy string `toml:"strategy"`
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
		if errors.Is(err, fs.ErrNotExist) || os.IsNotExist(err) {
			return Prefs{}, nil
		}
		return Prefs{}, fmt.Errorf("decode %s: %w", path, err)
	}
	return prefs, nil
}
