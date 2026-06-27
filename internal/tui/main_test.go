package tui

import (
	"os"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TestMain forces a known color profile so style assertions can rely on
// ANSI escape sequences being emitted. Without this, lipgloss detects no
// terminal in `go test` and renders styles as plain strings, which makes
// "selected style differs from unselected" checks trivially false.
func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.ANSI256)
	os.Exit(m.Run())
}

// useTheme pins the app-wide theme for a test that asserts specific palette ANSI
// codes (e.g. "48;5;205"). applyTheme mutates package-global style vars, and the
// many tests that call New() leave the default theme applied — so without this a
// palette assertion silently depends on which theme happens to be active. It
// restores the default on cleanup so the pin can't leak to the next test.
func useTheme(t *testing.T, key string) {
	t.Helper()
	applyTheme(diffThemes[diffThemeIndex(key)])
	t.Cleanup(func() { applyTheme(diffThemes[0]) })
}
