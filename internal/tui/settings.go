// Settings dialog — opened globally with `,` (see updateKey). It shows the
// running build version and the once-a-day update check's result, hosts the
// `U` upgrade action (`gh extension upgrade orbit`), and lets ←/→ cycle the
// app-wide color theme — diff, commit graph, chips, and chrome (applied live
// behind the dialog, persisted via SavePrefs). The body is a list of labeled
// rows so future settings drop in as more rows without reshaping the dialog.
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/config"
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
	m.settingsEntryThemeIdx = m.diffThemeIdx
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
		if m.diffThemeIdx != m.settingsEntryThemeIdx {
			// Theme changed this session: the diff/chips/meta repainted live,
			// but the graph's lane glyphs are baked into each row's cached
			// prefix (rendered once at stream time, see commitItem). Restream
			// so renderGraphRow re-runs with the new lane palette. reloadCmd is
			// stale-while-revalidate and preserves the cursor, so the only
			// visible effect is the recolor.
			return m, m.reloadCmd()
		}
		return m, nil
	case "left", "right":
		return m.cycleDiffTheme(msg.String() == "right"), nil
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

// cycleDiffTheme steps the active theme one slot (forward when next), wrapping
// around the themes. applyTheme re-points activeDiffTheme and rebuilds the
// shared chrome styles; the chips, graph meta columns, status line, borders, and
// tabs read those vars each View so they repaint live, and RerenderTheme repaints
// the cached diff. The graph's lane glyphs are the exception — they're baked into
// each row's cached prefix at stream time, so they refresh on the reload the
// Settings dialog fires on close (see handleSettingsKey), not per cycle. It then
// persists the new key — a save failure surfaces in the dialog's feedback line
// but leaves the theme applied for the session.
func (m Model) cycleDiffTheme(next bool) Model {
	delta := -1
	if next {
		delta = 1
	}
	m.diffThemeIdx = (m.diffThemeIdx + delta + len(diffThemes)) % len(diffThemes)
	applyTheme(diffThemes[m.diffThemeIdx])
	m.localChanges.RerenderTheme()
	m.diff.RerenderTheme()

	// Persist by mutating the on-disk prefs, not by rebuilding from Model state:
	// a fresh load keeps every key the file already has (the pull strategy, any
	// field New() didn't mirror) regardless of whether startup's LoadPrefs
	// succeeded. A malformed file fails the load — surface it and leave the file
	// untouched rather than clobbering it; the new theme still applies live.
	prefs, err := config.LoadPrefs()
	if err != nil {
		m.status = "theme save failed: " + firstLine(err.Error())
		m.statusStyle = statusErrS
		return m
	}
	// Write the app-wide key and migrate off the legacy [diff] theme so there's
	// a single source of truth after the first switch.
	prefs.Theme = diffThemes[m.diffThemeIdx].key
	prefs.Diff.Theme = ""
	if err := config.SavePrefs(prefs); err != nil {
		m.status = "theme save failed: " + firstLine(err.Error())
		m.statusStyle = statusErrS
	} else {
		m.status = ""
	}
	return m
}

// renderSettingsInner builds the centered dialog body.
func (m Model) renderSettingsInner() string {
	rows := []string{
		confirmPromptS.Render("Settings"),
		"",
		settingsRow("version", Version),
		settingsRow("latest", m.latestLabel()),
		settingsRow("theme", m.diffThemeLabel()),
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

// diffThemeLabel renders the "theme" row value: the current theme name framed
// by ◂ ▸ to signal it's adjustable, with a dim light/dark tag so the reviewer
// knows which terminal background it's tuned for.
func (m Model) diffThemeLabel() string {
	th := diffThemes[m.diffThemeIdx]
	mode := "dark"
	if !th.dark {
		mode = "light"
	}
	return fmt.Sprintf("◂ %s ▸ ", th.name) + help.Render("("+mode+")")
}

// settingsHint is the dialog's bottom key row, offering `U` only when an
// upgrade is actually available.
func (m Model) settingsHint() string {
	if m.updateAvailable && Version != "dev" {
		return "[←/→] theme · [U] upgrade · [esc] close"
	}
	return "[←/→] theme · [esc] close"
}

// settingsRow lays a dim, fixed-width label against its value so the rows align.
func settingsRow(label, value string) string {
	return help.Render(fmt.Sprintf("%-8s ", label)) + value
}
