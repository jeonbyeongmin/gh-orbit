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
