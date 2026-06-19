// Settings dialog — opened globally with `,` (see updateKey). It shows the
// running build version and the once-a-day update check's result, and hosts
// the `U` upgrade action (`gh extension upgrade orbit`). The body is a list of
// labeled rows so future settings — a diff-highlight theme is planned — drop
// in as more rows without reshaping the dialog.
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// settingsOpenable reports whether `,` should open the Settings dialog from
// the current surface. Only the bare pages qualify; a text-input or confirm
// riding on the Local Changes page (commit message / discard / abort) is
// excluded so `,` stays a literal character / the confirm keeps its keys.
func (m Model) settingsOpenable() bool {
	if m.mode == viewModeLocalChanges && (m.commitInput.open || m.lcDiscardOpen || m.lcAbortOpen) {
		return false
	}
	return m.isPageMode()
}

func (m Model) beginSettings() (tea.Model, tea.Cmd) {
	m.settingsReturnMode = m.mode
	m.mode = viewModeSettings
	// Open clean: a stale action status from the launching page would otherwise
	// render as the dialog's feedback line.
	m.status = ""
	m.statusStyle = statusOkS
	return m, nil
}

func (m Model) handleSettingsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// While the upgrade subprocess runs, swallow everything but ctrl+c so the
	// dialog can't be dismissed (or the upgrade re-triggered) mid-flight.
	if m.updateInFlight {
		if msg.String() == "ctrl+c" {
			return m.handleCtrlC()
		}
		return m, nil
	}
	switch msg.String() {
	case "ctrl+c":
		return m.handleCtrlC()
	case "q", "esc", ",":
		// Close back to the launching page. m.status is left intact so a
		// post-upgrade "restart gh orbit" note carries to that page's footer.
		m.mode = m.settingsReturnMode
		return m, nil
	case "U":
		if !m.updateAvailable || Version == "dev" {
			return m, nil
		}
		m.updateInFlight = true
		m.setBusyStatus("upgrading…")
		return m, upgradeOrbitCmd()
	}
	return m, nil
}

// renderSettingsInner builds the centered dialog body.
func (m Model) renderSettingsInner() string {
	rows := []string{
		confirmPromptS.Render("Settings"),
		"",
		settingsRow("version", Version),
		settingsRow("latest", m.latestLabel()),
		"",
	}
	// Feedback line: while the dialog owns the keys, the only status that can
	// land here is the upgrade's own (busy spinner, then result/error).
	if m.status != "" {
		if m.statusIsBusy() {
			rows = append(rows, statusBusyS.Render(spinnerGlyph(m.spinnerFrame)+" "+m.status))
		} else {
			rows = append(rows, m.statusStyle.Render(m.status))
		}
	}
	rows = append(rows, help.Render(m.settingsHint()))
	return strings.Join(rows, "\n")
}

// latestLabel describes the update state for the dialog's "latest" row.
func (m Model) latestLabel() string {
	switch {
	case Version == "dev":
		return help.Render("dev build — updates disabled")
	case !m.updateChecked:
		return help.Render("checking…")
	case m.updateLatest == "":
		return help.Render("unavailable (check failed)")
	case m.updateAvailable:
		return updateHintS.Render(m.updateLatest + "  (new)")
	default:
		return m.updateLatest + "  (up to date)"
	}
}

// settingsHint is the dialog's bottom key row, offering `U` only when an
// upgrade is actually available.
func (m Model) settingsHint() string {
	if m.updateAvailable && Version != "dev" {
		return "[U] upgrade · [esc] close"
	}
	return "[esc] close"
}

// settingsRow lays a dim, fixed-width label against its value so the rows align.
func settingsRow(label, value string) string {
	return help.Render(fmt.Sprintf("%-8s ", label)) + value
}
