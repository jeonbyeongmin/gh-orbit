// Top dashboard — the horizontal worktrees + Local Changes + fetch
// freshness band that sits above the graph pane when Model.topDashboard
// is true (env var GH_ORBIT_TOP_DASHBOARD=1).
//
// This is a pure render wrapper over m.refs's state. No separate model,
// no cursor. Cursor handling stays on the sidebar (OQ5(a) decision from
// the subtract-sidebar-invent-dashboard design doc). PR B1 ships the
// component coexisting with the sidebar — sidebar still renders the same
// worktrees / Local Changes / freshness, the dashboard adds a second
// surface above the graph. PR B2 will retire the sidebar half.
package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
)

// dashboardLines returns the inner row count the top dashboard renders:
// header (1) + worktree rows (N) + separator (1). Returns 0 when there
// are no worktrees so the caller can skip the dashboard entirely on a
// single-tree repo (the sidebar's sticky Local Changes row still works).
func dashboardLines(m Model) int {
	n := len(m.refs.Worktrees())
	if n == 0 {
		return 0
	}
	return n + 2
}

// renderTopDashboard renders the dashboard body. Caller wraps it in a
// bordered box (graph/tab style) and stacks it above the graph in the
// right column.
//
// Layout (N=4 inner rows shown):
//
//	Worktrees (4)                       ◆ Local Changes 3 files +12 -3 · 2m ago
//	▶ main · develop ●
//	  feat-auth · feat/auth
//	  feat-qa · feat/qa
//	  refactor · feat/refactor ●
//	──────────────────────────────────── fetched 14m ago
func renderTopDashboard(m Model, width int) string {
	if width < 1 {
		width = 1
	}
	now := time.Now()
	wts := m.refs.Worktrees()
	if len(wts) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(renderDashboardHeader(m, len(wts), width, now))
	b.WriteByte('\n')

	focused := m.focused == paneDashboard
	for i, wt := range wts {
		isCurrent := wt.Path == m.refs.currentWorktreePath
		dirtyMark := ""
		if m.refs.worktreeTimedOut[wt.Path] {
			dirtyMark = "?"
		} else if m.refs.worktreeDirty[wt.Path] {
			dirtyMark = "●"
		}
		// selected highlights the row under the dashboard-focus cursor.
		// When focus is off, all rows render selected=false → the original
		// read-only band styling.
		selected := focused && i == m.dashboardFocus.cursor
		subject, when := m.refs.WorktreeLastCommit(wt.Path)
		b.WriteString(renderWorktreeSidebarRow(wt, isCurrent, selected, dirtyMark, subject, when, now, width))
		b.WriteByte('\n')
	}

	b.WriteString(renderDashboardFooterLine(m, width, now))
	return b.String()
}

// renderDashboardHeader composes the top line: "Worktrees (N)" left,
// "◆ Local Changes meta" right. The label always wins when there isn't
// room for both — the Local Changes count is also visible inside the
// sticky row on the sidebar, so dropping it here is non-fatal.
func renderDashboardHeader(m Model, nWorktrees, width int, now time.Time) string {
	leftPlain := fmt.Sprintf("Worktrees (%d)", nWorktrees)
	left := refHeaderStyle.Render(leftPlain)
	leftW := runewidth.StringWidth(leftPlain)

	meta := m.refs.formatLocalChangesMeta(now)
	if meta == "" {
		return runewidth.Truncate(left, width, "…")
	}
	right := "◆ Local Changes " + meta
	rightStyled := timeStyle.Render(right)
	rightW := runewidth.StringWidth(right)

	if leftW+rightW+2 > width {
		if leftW > width {
			return runewidth.Truncate(leftPlain, width, "…")
		}
		return left
	}
	pad := width - leftW - rightW
	return left + strings.Repeat(" ", pad) + rightStyled
}

// renderDashboardFooterLine renders the bottom horizontal separator with
// the fetch freshness right-aligned. lastFetchAt zero → plain rule.
func renderDashboardFooterLine(m Model, width int, now time.Time) string {
	fresh := ""
	if !m.refs.lastFetchAt.IsZero() {
		age := relativeShortAt(m.refs.lastFetchAt, now)
		if age == "just now" {
			fresh = "fetched " + age
		} else {
			fresh = "fetched " + age + " ago"
		}
	}
	if fresh == "" {
		return strings.Repeat("─", width)
	}
	freshStyled := timeStyle.Render(fresh)
	freshW := runewidth.StringWidth(fresh)
	if freshW+2 >= width {
		return runewidth.Truncate(fresh, width, "…")
	}
	pad := width - freshW - 1
	return strings.Repeat("─", pad) + " " + freshStyled
}
