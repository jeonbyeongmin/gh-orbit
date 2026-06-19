// Self-update wiring (see internal/update for the why). On launch a background
// tea.Cmd checks the latest published release; when it's newer than the
// running build, renderHelpStatus surfaces a compact "↑ update available · ,
// settings" footer nudge. The version detail and the `U` upgrade action (which
// shells out `gh extension upgrade orbit`) live in the Settings dialog — see
// settings.go.
package tui

import (
	"context"
	"log"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/update"
)

// Version is the build-time version string, copied from package main's
// ldflags-injected value by cmd/orbit before the program starts. The "dev"
// default of a local `go build` disables the update check entirely — local
// builds never nag and never shell out to `gh extension upgrade`.
var Version = "dev"

const (
	// updateCheckTimeout matches the read-only loaders' 30s budget.
	updateCheckTimeout = 30 * time.Second
	// upgradeTimeout covers `gh extension upgrade` downloading a release asset.
	upgradeTimeout = 2 * time.Minute
)

// updateCheckedMsg carries the latest published tag and whether it's newer
// than the running build. A failed or skipped check lands as the zero value
// (available=false) — the reminder is passive enrichment, never an error.
type updateCheckedMsg struct {
	latest    string
	available bool
}

// updateUpgradedMsg is the terminal message of the `U` upgrade action.
type updateUpgradedMsg struct{ err error }

// checkUpdateCmd fires the once-a-day release check off the UI thread. It
// returns nil on dev builds so tea.Batch skips it (no check, no network).
func checkUpdateCmd(current string) tea.Cmd {
	if current == "" || current == "dev" {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), updateCheckTimeout)
		defer cancel()
		latest, err := update.Check(ctx, current)
		if err != nil {
			log.Printf("update check: %v", err)
			return updateCheckedMsg{}
		}
		return updateCheckedMsg{latest: latest, available: update.Newer(current, latest)}
	}
}

// upgradeOrbitCmd runs `gh extension upgrade orbit`. The running process keeps
// the old binary, so the handler tells the user to restart `gh orbit`.
func upgradeOrbitCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), upgradeTimeout)
		defer cancel()
		return updateUpgradedMsg{err: update.Upgrade(ctx)}
	}
}

func (m Model) updateUpdateMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case updateCheckedMsg:
		m.updateChecked = true
		m.updateLatest = msg.latest
		m.updateAvailable = msg.available
		return m, nil
	case updateUpgradedMsg:
		m.updateInFlight = false
		if msg.err != nil {
			m.status = "upgrade failed: " + firstLine(msg.err.Error())
			m.statusStyle = statusErrS
			return m, nil
		}
		m.updateAvailable = false
		m.status = "upgraded to " + m.updateLatest + " — restart gh orbit to use it"
		m.statusStyle = statusOkS
		return m, nil
	}
	return m, nil
}
