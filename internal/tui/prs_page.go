// Pull Requests page: viewModePRsPage is a full-screen tab (the 4th, after
// Local Changes) reached via the tab/shift+tab cycle. It lists every open PR
// `gh pr list` returned — including ones whose head branch isn't checked out
// locally, which the graph cursor can't reach. `enter` opens the cursor PR on
// GitHub in the browser; `m` opens the merge confirm. The data is m.prList —
// prs.go's prListCmd already loads it for the chip badges; this surface only
// owns its own cursor.
package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// prsPageState backs viewModePRsPage. cursor indexes into m.prList.
type prsPageState struct {
	cursor int
}

// enterPRsPage flips to the Pull Requests tab. Unlike the old `l` modal there
// is no empty guard — a tab you cycle to always shows, rendering an empty state
// when there are no open PRs.
func (m Model) enterPRsPage() (Model, tea.Cmd) {
	if m.prsPage.cursor >= len(m.prList) {
		m.prsPage.cursor = 0
	}
	m.mode = viewModePRsPage
	m.status = ""
	return m, nil
}

func (m Model) prsPageMoveCursor(delta int) Model {
	if len(m.prList) == 0 {
		return m
	}
	c := m.prsPage.cursor + delta
	if c < 0 {
		c = 0
	}
	if c >= len(m.prList) {
		c = len(m.prList) - 1
	}
	m.prsPage.cursor = c
	return m
}

// cursorPR returns the PR under the page cursor, or ok=false when the list is
// empty / the cursor is out of range. Shared by the enter / merge handlers.
func (m Model) cursorPR() (prInfo, bool) {
	if m.prsPage.cursor < 0 || m.prsPage.cursor >= len(m.prList) {
		return prInfo{}, false
	}
	return m.prList[m.prsPage.cursor], true
}

// prsPageOpenWeb is `enter`: open the cursor PR on GitHub in the browser.
func (m Model) prsPageOpenWeb() (Model, tea.Cmd) {
	pr, ok := m.cursorPR()
	if !ok {
		return m, nil
	}
	return m.openPRWeb(pr.Number)
}

// prsPageMerge is `m`: arm the merge confirm dialog for the cursor PR.
func (m Model) prsPageMerge() (Model, tea.Cmd) {
	pr, ok := m.cursorPR()
	if !ok {
		return m, nil
	}
	return m.beginMergeFor(pr.Number)
}

// Card geometry mirrors the worktree dashboard: a 2-col gutter (cursor bar `▌`
// + `▶` marker) plus a 3-line body, so the PR page and the worktree page share
// one visual rhythm.
const (
	prCardLines  = 3
	prCardGutter = 2
	prCardIndent = "   " // author / review lines indent under the title
)

// Foreground-only status colors (no chip bg): the CI glyph, the conflict mark,
// and the review dot each read as "state at a glance" against the row.
var (
	ciPassStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color(colorBadgePass))
	ciFailStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color(colorBadgeFail))
	ciPendStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color(colorBadgePending))
	conflictStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colorBadgeFail))

	reviewApprovedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colorBadgePass))
	reviewChangesStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color(colorBadgeFail))
	reviewPendingStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color(colorBadgeNone))
)

// renderPRsView renders the full-screen Pull Requests page sized to fit inside
// the page box (width × height), mirroring renderWorktreesView: a header, a
// scroll-windowed stack of 3-line cards, padded to exactly height so the box
// frame never jumps.
func (m Model) renderPRsView(width, height int) string {
	now := time.Now()
	title := fmt.Sprintf("[Pull requests · %d]", len(m.prList))
	fresh := ""
	if !m.refs.lastFetchAt.IsZero() {
		fresh = "fetched " + relativeShortAt(m.refs.lastFetchAt, now)
		if !strings.HasSuffix(fresh, "just now") {
			fresh += " ago"
		}
	}
	header := layoutLeftRight(modalHeaderS.Render(title), help.Render(fresh), width)

	areaH := height - 2
	if areaH < prCardLines {
		areaH = prCardLines
	}
	var cardLines []string
	if len(m.prList) == 0 {
		cardLines = []string{help.Render("(no open PRs)")}
	} else {
		cardLines = m.prCardArea(areaH, width, now)
	}

	lines := make([]string, 0, height)
	lines = append(lines, header, "")
	lines = append(lines, cardLines...)
	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

// prCardArea windows the 3-line cards around the cursor to fit areaH lines,
// flagging clipped rows with `↑ N more` / `↓ N more`. Cards are separated by
// one blank line. Mirrors worktreeCardArea.
func (m Model) prCardArea(areaH, width int, now time.Time) []string {
	cardSlot := prCardLines + 1 // a card is 3 lines + 1 trailing gap
	capacity := (areaH + 1) / cardSlot
	if capacity < 1 {
		capacity = 1
	}
	// When the list overflows, the ↑/↓ markers each cost a row that renderPRsView
	// would otherwise clamp away — shrink capacity by the worst case (both
	// markers) so the overflow affordance always survives. (worktreeCardArea
	// skips this: a worktree list rarely overflows, an open-PR list of up to 100
	// routinely does.)
	if len(m.prList) > capacity {
		capacity = (areaH - 2 + 1) / cardSlot
		if capacity < 1 {
			capacity = 1
		}
	}
	first := 0
	if len(m.prList) > capacity {
		first = m.prsPage.cursor - capacity/2
		if first < 0 {
			first = 0
		}
		if first > len(m.prList)-capacity {
			first = len(m.prList) - capacity
		}
	}
	last := first + capacity
	if last > len(m.prList) {
		last = len(m.prList)
	}

	var lines []string
	if first > 0 {
		lines = append(lines, help.Render(fmt.Sprintf("  ↑ %d more", first)))
	}
	for i := first; i < last; i++ {
		if i > first {
			lines = append(lines, "")
		}
		lines = append(lines, buildPRCard(m.prList[i], i == m.prsPage.cursor, width, now)...)
	}
	if last < len(m.prList) {
		lines = append(lines, help.Render(fmt.Sprintf("  ↓ %d more", len(m.prList)-last)))
	}
	return lines
}

// buildPRCard renders the 3 lines of one PR card, each padded to width:
//
//	▌▶ #124 Local Changes stash/discard/poll        ✓    3h
//	▌    @alice   feat/local-changes → develop
//	▌    ● approved   +312 -47   4 files
//
// Line 1 leads with `#N title` and right-anchors the status cluster (CI glyph,
// conflict mark, age). Line 2 puts the author up front (bright) with the
// head→base branch dimmed beside it — the author no longer rides the truncation
// tail where it used to vanish first. Line 3 carries the review dot + diff size.
// The cursor bar `▌` is the only per-row, bg-free highlight, on all three lines.
func buildPRCard(pr prInfo, cursor bool, width int, now time.Time) []string {
	bar, mark := " ", " "
	if cursor {
		bar = cursorStyle.Render("▌")
		mark = cursorStyle.Render("▶")
	}
	contentW := width - prCardGutter
	if contentW < 1 {
		contentW = 1
	}
	line1 := bar + mark + " " + prTitleLine(pr, cursor, contentW-1, now)
	line2 := bar + " " + prMetaLine(pr, contentW)
	line3 := bar + " " + prReviewLine(pr, contentW)
	return []string{padToWidth(line1, width), padToWidth(line2, width), padToWidth(line3, width)}
}

// prTitleLine left-anchors `#N title` and right-anchors the status cluster
// within width, truncating the title (never the cluster) under pressure.
func prTitleLine(pr prInfo, cursor bool, width int, now time.Time) string {
	style := func(s string) string {
		if cursor {
			return selectedStyle.Render(s)
		}
		return s
	}
	title := fmt.Sprintf("#%d %s", pr.Number, pr.Title)
	status, statusW := prStatusCluster(pr, now)
	// No cluster, or it alone fills the line: show the title (the row's
	// identity) rather than overflow-wrap the box frame.
	if status == "" || statusW+1 >= width {
		return style(runewidth.Truncate(title, width, "…"))
	}
	plain := runewidth.Truncate(title, width-statusW-1, "…")
	gap := width - runewidth.StringWidth(plain) - statusW // ≥ 1
	return style(plain) + strings.Repeat(" ", gap) + status
}

// prStatusCluster builds the right-anchored L1 cluster (CI glyph · conflict ·
// age) as a styled string plus its plain display width, so prTitleLine can lay
// it out without measuring the colored segments.
func prStatusCluster(pr prInfo, now time.Time) (string, int) {
	var parts []string
	w := 0
	add := func(styled, plain string) {
		if len(parts) > 0 {
			w += 2 // "  " separator
		}
		parts = append(parts, styled)
		w += runewidth.StringWidth(plain)
	}
	if g := prCheckGlyph(pr.Checks); g != "" {
		add(ciStyle(pr.Checks).Render(g), g)
	}
	if pr.Conflicting {
		add(conflictStyle.Render("⚠"), "⚠")
	}
	if !pr.UpdatedAt.IsZero() {
		ts := relativeShortAt(pr.UpdatedAt, now)
		add(ts, ts)
	}
	return strings.Join(parts, "  "), w
}

// prMetaLine renders L2: the author up front (bright) and the head→base branch
// dimmed beside it. The author never truncates; the branch absorbs the squeeze.
func prMetaLine(pr prInfo, contentW int) string {
	avail := contentW - runewidth.StringWidth(prCardIndent)
	if avail < 1 {
		avail = 1
	}
	branch := pr.HeadRef
	if pr.BaseRef != "" {
		if branch != "" {
			branch += " → " + pr.BaseRef
		} else {
			branch = pr.BaseRef
		}
	}
	if pr.Author == "" {
		return help.Render(prCardIndent + runewidth.Truncate(branch, avail, "…"))
	}
	author := "@" + pr.Author
	authorW := runewidth.StringWidth(author)
	branchAvail := avail - authorW - 3 // "   " gap
	if authorW >= avail || branch == "" || branchAvail < 1 {
		return prCardIndent + runewidth.Truncate(author, avail, "…")
	}
	return prCardIndent + author + "   " + help.Render(runewidth.Truncate(branch, branchAvail, "…"))
}

// prReviewLine renders L3: the colored review dot + label, then the diff size
// (`+adds -dels   N files`). Both are optional — a PR with no review decision
// and no diff data leaves the line blank.
func prReviewLine(pr prInfo, contentW int) string {
	avail := contentW - runewidth.StringWidth(prCardIndent)
	if avail < 1 {
		avail = 1
	}
	var b strings.Builder
	if lbl, ok := prReviewLabel(pr.Review); ok {
		b.WriteString("● " + lbl)
	}
	if diff := prDiffSummary(pr); diff != "" {
		if b.Len() > 0 {
			b.WriteString("   ")
		}
		b.WriteString(diff)
	}
	plain := b.String()
	if plain == "" {
		return ""
	}
	out := prCardIndent + runewidth.Truncate(plain, avail, "…")
	// Colorize only the leading dot (fixed position; the plain string was
	// already truncated). Replace is a no-op if a tiny width clipped the dot.
	return strings.Replace(out, "●", prReviewStyle(pr.Review).Render("●"), 1)
}

// ciStyle is the foreground color for a CI rollup glyph. Only called for a
// non-None state (None renders no glyph).
func ciStyle(s prCheckState) lipgloss.Style {
	switch s {
	case prChecksPassing:
		return ciPassStyle
	case prChecksFailing:
		return ciFailStyle
	default: // pending
		return ciPendStyle
	}
}

func prReviewLabel(s prReviewState) (string, bool) {
	switch s {
	case prReviewApproved:
		return "approved", true
	case prReviewChangesRequested:
		return "changes", true
	case prReviewPending:
		return "pending", true
	}
	return "", false
}

func prReviewStyle(s prReviewState) lipgloss.Style {
	switch s {
	case prReviewApproved:
		return reviewApprovedStyle
	case prReviewChangesRequested:
		return reviewChangesStyle
	default: // pending
		return reviewPendingStyle
	}
}

// prDiffSummary is `+adds -dels   N file(s)`, or "" when no diff data was
// fetched (all zero).
func prDiffSummary(pr prInfo) string {
	if pr.Additions == 0 && pr.Deletions == 0 && pr.Files == 0 {
		return ""
	}
	unit := "files"
	if pr.Files == 1 {
		unit = "file"
	}
	return fmt.Sprintf("+%d -%d   %d %s", pr.Additions, pr.Deletions, pr.Files, unit)
}
