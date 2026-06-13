package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// spinnerFrames are braille glyphs — exactly one terminal cell each, unlike
// emoji whose width varies across terminals/fonts. Keep every frame 1-cell
// so the text after the spinner never shifts.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const spinnerInterval = 100 * time.Millisecond

// spinnerTickMsg drives the loading-spinner animation. The tick is gated:
// Model.Update arms it only while spinnerVisible() is true, and the
// spinnerTickMsg handler stops re-arming the moment nothing is loading —
// an idle TUI schedules zero wakeups.
type spinnerTickMsg struct{}

func spinnerTickCmd() tea.Cmd {
	return tea.Tick(spinnerInterval, func(time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}

func spinnerGlyph(frame int) string {
	return spinnerFrames[frame%len(spinnerFrames)]
}

// loadingPane renders the shared loading placeholder — a spinner glyph and
// label centered in the pane's inner content area (graph stream, patch
// overlay, local-changes tree/diff). Zero dimensions (pre-WindowSizeMsg
// renders, direct sub-model tests) fall back to the bare text.
func loadingPane(w, h, frame int) string {
	s := spinnerGlyph(frame) + " loading…"
	if w <= 0 || h <= 0 {
		return s
	}
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, s)
}
