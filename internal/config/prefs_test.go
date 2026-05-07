package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfigFile(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	appDir := filepath.Join(dir, appName)
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestLoadPrefsMissingFileReturnsZero(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	prefs, err := LoadPrefs()
	if err != nil {
		t.Fatalf("LoadPrefs: %v", err)
	}
	if prefs.Pull.Strategy != "" {
		t.Errorf("Pull.Strategy = %q, want empty", prefs.Pull.Strategy)
	}
}

func TestLoadPrefsEmptyFile(t *testing.T) {
	writeConfigFile(t, "")
	prefs, err := LoadPrefs()
	if err != nil {
		t.Fatalf("LoadPrefs: %v", err)
	}
	if prefs.Pull.Strategy != "" {
		t.Errorf("Pull.Strategy = %q, want empty", prefs.Pull.Strategy)
	}
}

func TestLoadPrefsRebaseStrategy(t *testing.T) {
	writeConfigFile(t, "[pull]\nstrategy = \"rebase\"\n")
	prefs, err := LoadPrefs()
	if err != nil {
		t.Fatalf("LoadPrefs: %v", err)
	}
	if prefs.Pull.Strategy != "rebase" {
		t.Errorf("Pull.Strategy = %q, want rebase", prefs.Pull.Strategy)
	}
}

func TestLoadPrefsMalformedTOMLReturnsError(t *testing.T) {
	writeConfigFile(t, "[pull\nstrategy = \"rebase\"\n")
	_, err := LoadPrefs()
	if err == nil {
		t.Fatal("expected error on malformed TOML")
	}
}

func TestLoadPrefsUnknownKeyIgnored(t *testing.T) {
	writeConfigFile(t, "[pull]\nstrategy = \"merge\"\nunknown = 42\n")
	prefs, err := LoadPrefs()
	if err != nil {
		t.Fatalf("LoadPrefs: %v", err)
	}
	if prefs.Pull.Strategy != "merge" {
		t.Errorf("Pull.Strategy = %q, want merge", prefs.Pull.Strategy)
	}
}
