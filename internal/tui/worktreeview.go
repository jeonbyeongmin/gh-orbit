// Full-screen worktree dashboard (the `w` view). Replaces the graph with a
// vertical stack of 3-line cards — one per worktree — so the reviewer can
// compare every tree's branch, PR/CI state, and last activity at once instead
// of squeezing them into a single overlay row. Switching is still one stroke:
// open with `w`, `↑`/`↓` to a card, `enter` to switch back to the graph.
//
// Card anatomy (gutter is 2 cols: cursor bar `▌` + current marker `▶`):
//
//	▌▶ develop                              #42✓  ●  2m
//	▌     ~/project/gh-orbit
//	▌     Merge pull request #101 from feat/pr-review
//
// Line 1 leads with the branch (the stable identifier — git guarantees one
// branch per attached worktree) and right-anchors the status cluster. Lines 2
// and 3 carry the path (dim) and the HEAD commit subject. Nothing competes for
// width, so long names no longer truncate the way the old single-row layout
// forced.
package tui

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// worktreeCardData is the per-tree state a single card renders. The Model
// method assembles it from refs + prs state so buildWorktreeCard stays a pure,
// testable function over plain values.
type worktreeCardData struct {
	branch    string
	detached  bool
	path      string // display path (home already collapsed to ~)
	badge     string // open-PR `#N` + CI glyph, or ""
	dirty     string // "●N" (N changed files) · "?" timed out · "" clean/loading
	sync      string // "↑a↓b" vs upstream, or "" (in sync / no upstream)
	subject   string // HEAD commit subject
	when      time.Time
	isCurrent bool // the worktree m.workdir lives in
	isCursor  bool // the row under the modal cursor
}

const (
	worktreeCardLines  = 3
	worktreeCardGutter = 2
	worktreeCardIndent = "   " // path / subject indent under the branch
)

// renderWorktreesView renders the full-screen dashboard content sized to fit
// inside the graph box (width × height). It always returns exactly height lines
// so the surrounding boxStyle frames a clean rectangle.
func (m Model) renderWorktreesView(width, height int) string {
	wts := m.modalWorktrees()
	now := time.Now()

	title := fmt.Sprintf("[Worktrees · %d]", len(wts))
	if m.worktreesModal.sortByCommit {
		title += "  ↓time"
	}
	fresh := ""
	if !m.refs.lastFetchAt.IsZero() {
		fresh = "fetched " + relativeShortAt(m.refs.lastFetchAt, now)
		if !strings.HasSuffix(fresh, "just now") {
			fresh += " ago"
		}
	}
	header := layoutLeftRight(modalHeaderS.Render(title), help.Render(fresh), width)

	// Card area = everything below the header (+ its blank spacer). Status
	// feedback and the key hint now ride the shared bottom line
	// (renderHelpStatus: status + `? help`) and the `?` panel, same as the
	// graph and local-changes pages — no in-box chrome, no duplicated keys.
	areaH := height - 2
	if areaH < worktreeCardLines {
		areaH = worktreeCardLines
	}
	var cardLines []string
	if len(wts) == 0 {
		cardLines = []string{help.Render("(no worktrees loaded yet)")}
	} else {
		cardLines = m.worktreeCardArea(wts, areaH, width, now)
	}

	lines := make([]string, 0, height)
	lines = append(lines, header, "")
	lines = append(lines, cardLines...)
	// Pad / clamp to exactly height so the box frame never jumps.
	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

// worktreeCardArea windows the cards around the cursor to fit areaH lines,
// prefixing / suffixing `↑ N more` / `↓ N more` markers when the list is
// clipped. Cards are separated by one blank line.
func (m Model) worktreeCardArea(wts []git.Worktree, areaH, width int, now time.Time) []string {
	capacity := (areaH + 1) / (worktreeCardLines + 1) // each card is 3 lines + 1 gap
	if capacity < 1 {
		capacity = 1
	}
	first := 0
	if len(wts) > capacity {
		first = m.worktreesModal.cursor - capacity/2
		if first < 0 {
			first = 0
		}
		if first > len(wts)-capacity {
			first = len(wts) - capacity
		}
	}
	last := first + capacity
	if last > len(wts) {
		last = len(wts)
	}

	var lines []string
	if first > 0 {
		lines = append(lines, help.Render(fmt.Sprintf("  ↑ %d more", first)))
	}
	for i := first; i < last; i++ {
		if i > first {
			lines = append(lines, "")
		}
		lines = append(lines, m.renderWorktreeCard(wts[i], i, width, now)...)
	}
	if last < len(wts) {
		lines = append(lines, help.Render(fmt.Sprintf("  ↓ %d more", len(wts)-last)))
	}
	return lines
}

// renderWorktreeCard assembles one card's data from refs + prs state and hands
// it to the pure builder.
func (m Model) renderWorktreeCard(wt git.Worktree, idx, width int, now time.Time) []string {
	dirty := ""
	if m.refs.worktreeTimedOut[wt.Path] {
		dirty = "?"
	} else if n := m.refs.WorktreeDirtyCount(wt.Path); n > 0 {
		dirty = "●" + strconv.Itoa(n)
	}
	sync := ""
	if s := m.refs.WorktreeSync(wt.Path); s.hasUpstream {
		if s.ahead > 0 {
			sync += "↑" + strconv.Itoa(s.ahead)
		}
		if s.behind > 0 {
			sync += "↓" + strconv.Itoa(s.behind)
		}
	}
	badge := ""
	if pr, ok := m.prs[wt.Branch]; ok {
		badge = prBadge(pr)
	}
	subject, when := m.refs.WorktreeLastCommit(wt.Path)
	return buildWorktreeCard(worktreeCardData{
		branch:    wt.Branch,
		detached:  wt.Detached,
		path:      worktreePathDisplay(wt.Path),
		badge:     badge,
		dirty:     dirty,
		sync:      sync,
		subject:   subject,
		when:      when,
		isCurrent: wt.Path == m.workdir,
		isCursor:  idx == m.worktreesModal.cursor,
	}, now, width)
}

// buildWorktreeCard renders the 3 lines of one worktree card, each padded to
// width. The cursor bar `▌` is the only per-row highlight (a bg tint over
// styled segments would hit the lipgloss wrap-reset trap), so it stays a clean
// left-gutter accent on all three lines.
func buildWorktreeCard(d worktreeCardData, now time.Time, width int) []string {
	bar := " "
	if d.isCursor {
		bar = cursorStyle.Render("▌")
	}
	mark := " "
	if d.isCurrent {
		mark = cursorStyle.Render("▶")
	}
	contentW := width - worktreeCardGutter
	if contentW < 1 {
		contentW = 1
	}

	branch := d.branch
	switch {
	case d.detached:
		branch = "(detached)"
	case branch == "":
		branch = "(no branch)"
	}
	var status []string
	if d.badge != "" {
		status = append(status, d.badge)
	}
	if d.sync != "" {
		status = append(status, d.sync)
	}
	if d.dirty != "" {
		status = append(status, d.dirty)
	}
	if !d.when.IsZero() {
		status = append(status, relativeShortAt(d.when, now))
	}
	// One space after the gutter so the branch doesn't butt against the
	// `▶` marker (`▌▶ develop`, not `▌▶develop`).
	line1 := bar + mark + " " + branchStatusLine(branch, strings.Join(status, "  "), d.isCurrent, contentW-1)

	indentW := runewidth.StringWidth(worktreeCardIndent)
	pathBudget := contentW - indentW
	if pathBudget < 1 {
		pathBudget = 1
	}
	line2 := bar + " " + help.Render(worktreeCardIndent+truncLeftKeepTail(d.path, pathBudget))

	subject := d.subject
	if subject == "" {
		subject = "—"
	}
	line3 := bar + " " + runewidth.Truncate(worktreeCardIndent+subject, contentW, "…")

	return []string{padToWidth(line1, width), padToWidth(line2, width), padToWidth(line3, width)}
}

// branchStatusLine left-anchors the branch and right-anchors the status
// cluster within width, truncating the branch (never the status) under
// pressure. Returns at most width display cells — the caller's padToWidth only
// pads, so an overflow here would wrap and corrupt the card frame.
func branchStatusLine(branch, status string, current bool, width int) string {
	sw := runewidth.StringWidth(status)
	// When the (plain) status alone fills the line there's no room for a branch:
	// show as much status as fits and drop the branch rather than overflow.
	if sw >= width {
		return runewidth.Truncate(status, width, "…")
	}
	plain := runewidth.Truncate(branch, width-sw-1, "…")
	gap := width - runewidth.StringWidth(plain) - sw // ≥ 1: plain width ≤ width-sw-1
	b := plain
	if current {
		b = selectedStyle.Render(plain)
	}
	return b + strings.Repeat(" ", gap) + status
}

// worktreePathDisplay collapses the user's home prefix to ~ so the path line
// reads cleanly. Non-home paths pass through unchanged.
func worktreePathDisplay(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if strings.HasPrefix(path, home+string(os.PathSeparator)) {
		return "~" + path[len(home):]
	}
	return path
}

// truncLeftKeepTail drops runes from the FRONT (prepending …) so the
// discriminating tail of a long path survives. Right-truncation would hide the
// worktree directory name, which is the part that tells trees apart.
func truncLeftKeepTail(s string, width int) string {
	if runewidth.StringWidth(s) <= width {
		return s
	}
	if width <= 1 {
		return runewidth.Truncate(s, width, "")
	}
	r := []rune(s)
	for len(r) > 0 && runewidth.StringWidth(string(r))+1 > width {
		r = r[1:]
	}
	return "…" + string(r)
}

// layoutLeftRight places left at the start and right flush to the width's right
// edge, with at least one space between. Mirrors renderHelpStatus's math.
func layoutLeftRight(left, right string, width int) string {
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}
