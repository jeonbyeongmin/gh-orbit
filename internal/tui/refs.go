// Left-pane ref list (Local / Remote / Tags). Like graphModel, git access
// is async via tea.Cmd → tea.Msg so the TUI never blocks on for-each-ref.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

const refLoadTimeout = 30 * time.Second

type refModel struct {
	// byKind index matches refSections.
	byKind  [3][]git.Ref
	width   int
	height  int
	cursor  int
	yOffset int
	// folded[i] toggles section i (Local/Remote/Tags) between expanded and
	// collapsed. Zero value leaves every section expanded.
	folded [3]bool
	loaded bool
	err    error
}

func newRefsModel() refModel { return refModel{} }

type refsLoadedMsg struct{ refs []git.Ref }
type refsLoadFailedMsg struct{ err error }

// refSelectedMsg is emitted when the user picks a ref (enter). The root model
// uses it to reload the graph pane against the chosen ref.
type refSelectedMsg struct{ ref git.Ref }

func loadRefsCmd(dir string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), refLoadTimeout)
		defer cancel()
		refs, err := git.ForEachRef(ctx, git.ForEachRefOptions{Dir: dir})
		if err != nil {
			return refsLoadFailedMsg{err: err}
		}
		return refsLoadedMsg{refs: refs}
	}
}

func (r refModel) Init() tea.Cmd { return nil }

// ResetForReload clears loaded/err so View renders the "loading…" placeholder
// while a fresh loadRefsCmd is in flight. cursor is intentionally left alone
// here — refsLoadedMsg already resets it to 0 on arrival, which doubles as a
// safe fallback when the previously selected ref was deleted by another tool.
func (r *refModel) ResetForReload() {
	r.loaded = false
	r.err = nil
}

func (r refModel) Update(msg tea.Msg) (refModel, tea.Cmd) {
	switch m := msg.(type) {
	case refsLoadedMsg:
		r.byKind = partitionByKind(m.refs)
		r.cursor = 0
		r.yOffset = 0
		r.loaded = true
		r.err = nil
		return r, nil
	case refsLoadFailedMsg:
		r.loaded = true
		r.err = m.err
		return r, nil
	case tea.KeyMsg:
		if m.String() == "enter" {
			if ref, ok := r.Selected(); ok {
				return r, func() tea.Msg { return refSelectedMsg{ref: ref} }
			}
			return r, nil
		}
		return r.handleKey(m), nil
	}
	return r, nil
}

func (r refModel) handleKey(msg tea.KeyMsg) refModel {
	total := r.selectableCount()
	switch msg.String() {
	case "j", "down":
		if r.cursor < total-1 {
			r.cursor++
			r = r.nudgeOffsetOnEdge()
		}
	case "k", "up":
		if r.cursor > 0 {
			r.cursor--
			r = r.nudgeOffsetOnEdge()
		}
	case "g":
		r.cursor = 0
		r.yOffset = 0
	case "G":
		if total > 0 {
			r.cursor = total - 1
			r = r.scrollCursorIntoView()
		}
	case "z":
		r = r.toggleFoldAtCursor()
		r = r.scrollCursorIntoView()
	}
	return r
}

// toggleFoldAtCursor flips the fold state of the cursor's section. When that
// just-folded section was the cursor's home, the cursor jumps to the first
// expanded, non-empty section below; failing that, the first one above.
func (r refModel) toggleFoldAtCursor() refModel {
	sec, onRef := r.cursorSection()
	if !onRef {
		// Cursor isn't on a real ref (everything empty / all folded). Pick
		// section 0 so a fresh repo's first `z` still toggles something.
		sec = 0
	}
	r.folded[sec] = !r.folded[sec]
	if !r.folded[sec] {
		// Expanding never invalidates a previously valid cursor index.
		return r
	}
	if !onRef {
		return r
	}
	if newCursor, ok := r.firstRefIndexInExpandedSection(sec, +1); ok {
		r.cursor = newCursor
		return r
	}
	if newCursor, ok := r.firstRefIndexInExpandedSection(sec, -1); ok {
		r.cursor = newCursor
		return r
	}
	r.cursor = 0
	return r
}

// cursorSection reports which section the cursor sits in. The second return
// is false when there is no selectable ref at all (everything empty/folded).
func (r refModel) cursorSection() (int, bool) {
	rows := r.flatRows()
	i, ok := r.cursorFlatRow(rows)
	if !ok {
		return 0, false
	}
	return rows[i].sectionIdx, true
}

// firstRefIndexInExpandedSection scans sections in direction dir (+1 down,
// -1 up) starting just past `from`, and returns the cursor index of the first
// ref in the first expanded section that has any refs.
func (r refModel) firstRefIndexInExpandedSection(from, dir int) (int, bool) {
	for i := from + dir; i >= 0 && i < len(r.byKind); i += dir {
		if r.folded[i] || len(r.byKind[i]) == 0 {
			continue
		}
		// cursor index = number of selectable refs in expanded sections
		// that come before section i.
		n := 0
		for j := 0; j < i; j++ {
			if r.folded[j] {
				continue
			}
			n += len(r.byKind[j])
		}
		return n, true
	}
	return 0, false
}

// visibleHeight is the number of body rows that fit beside the sticky header.
// height >= 2 always reserves one row for the sticky line (even on frames
// where it is suppressed, so scroll math doesn't depend on visibility); below
// that, sticky is disabled and the full pane height is body.
func (r refModel) visibleHeight() int {
	if r.height <= 0 {
		return 0
	}
	if r.height < 2 {
		return r.height
	}
	return r.height - 1
}

// scrollCursorIntoView pulls yOffset so the cursor row is inside the window
// in one shot. Used by g/G/z and SetSize, where the cursor may have jumped
// far from the previous offset.
func (r refModel) scrollCursorIntoView() refModel {
	rows := r.flatRows()
	cursorRow, ok := r.cursorFlatRow(rows)
	if !ok {
		return r
	}
	vh := r.visibleHeight()
	if vh <= 0 {
		return r
	}
	if cursorRow < r.yOffset {
		r.yOffset = cursorRow
	} else if cursorRow >= r.yOffset+vh {
		r.yOffset = cursorRow - vh + 1
	}
	return r.clampOffset(len(rows), vh)
}

// nudgeOffsetOnEdge shifts yOffset by ±1 only when the cursor moved exactly
// to the row immediately above or below the visible window. Used by j/k so
// cursor and viewport advance together at the edges but otherwise stay put.
func (r refModel) nudgeOffsetOnEdge() refModel {
	rows := r.flatRows()
	cursorRow, ok := r.cursorFlatRow(rows)
	if !ok {
		return r
	}
	vh := r.visibleHeight()
	if vh <= 0 {
		return r
	}
	if cursorRow == r.yOffset-1 {
		r.yOffset--
	} else if cursorRow == r.yOffset+vh {
		r.yOffset++
	}
	return r.clampOffset(len(rows), vh)
}

func (r refModel) clampOffset(rowsLen, vh int) refModel {
	if r.yOffset < 0 {
		r.yOffset = 0
	}
	maxOffset := rowsLen - vh
	if maxOffset < 0 {
		maxOffset = 0
	}
	if r.yOffset > maxOffset {
		r.yOffset = maxOffset
	}
	return r
}

// Selected returns the ref under the cursor, if any. Refs in folded sections
// are excluded — the cursor only ever lands on visible refs.
func (r refModel) Selected() (git.Ref, bool) {
	rows := r.flatRows()
	i, ok := r.cursorFlatRow(rows)
	if !ok {
		return git.Ref{}, false
	}
	row := rows[i]
	return r.byKind[row.sectionIdx][row.refIdx], true
}

func (r refModel) selectableCount() int {
	n := 0
	for i, items := range r.byKind {
		if r.folded[i] {
			continue
		}
		n += len(items)
	}
	return n
}

func partitionByKind(refs []git.Ref) [3][]git.Ref {
	var out [3][]git.Ref
	for i, sec := range refSections {
		for _, ref := range refs {
			if ref.Kind == sec.kind {
				out[i] = append(out[i], ref)
			}
		}
	}
	return out
}

var refHeaderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colorTime)).Bold(true)

type refSection struct {
	title string
	kind  git.RefKind
}

var refSections = [3]refSection{
	{"Local branches", git.RefKindLocal},
	{"Remote branches", git.RefKindRemote},
	{"Tags", git.RefKindTag},
}

// refRowKind tags every visible line so View can slice by yOffset and the
// sticky-header pass can find headers without re-walking byKind.
type refRowKind int

const (
	refRowGap refRowKind = iota
	refRowHeader
	refRowEmpty
	refRowRef
)

type refRow struct {
	kind       refRowKind
	sectionIdx int
	refIdx     int // valid only for refRowRef
}

// flatRows expands the three sections into a flat row list in render order:
// gap (between sections), header, then either empty placeholder, the ref list,
// or — for a folded section — nothing past the header. This is the index space
// every later concern (visible-window slicing, sticky header, scroll math)
// shares.
func (r refModel) flatRows() []refRow {
	var rows []refRow
	for i := range refSections {
		if i > 0 {
			rows = append(rows, refRow{kind: refRowGap, sectionIdx: i})
		}
		rows = append(rows, refRow{kind: refRowHeader, sectionIdx: i})
		if r.folded[i] {
			continue
		}
		items := r.byKind[i]
		if len(items) == 0 {
			rows = append(rows, refRow{kind: refRowEmpty, sectionIdx: i})
			continue
		}
		for j := range items {
			rows = append(rows, refRow{kind: refRowRef, sectionIdx: i, refIdx: j})
		}
	}
	return rows
}

// cursorFlatRow maps r.cursor (n-th selectable ref) onto its flatRows index.
// Returns false when there is no selectable ref (everything empty or folded).
func (r refModel) cursorFlatRow(rows []refRow) (int, bool) {
	n := 0
	for i, row := range rows {
		if row.kind != refRowRef {
			continue
		}
		if n == r.cursor {
			return i, true
		}
		n++
	}
	return -1, false
}

func (r refModel) renderRow(row refRow, width int, selected bool) string {
	switch row.kind {
	case refRowGap:
		return ""
	case refRowHeader:
		return refHeaderStyle.Render(runewidth.Truncate(refSections[row.sectionIdx].title, width, "…"))
	case refRowEmpty:
		return timeStyle.Render(runewidth.Truncate("  (empty)", width, "…"))
	case refRowRef:
		ref := r.byKind[row.sectionIdx][row.refIdx]
		return renderRefLine(ref, width, selected)
	}
	return ""
}

func (r refModel) View() string {
	if !r.loaded {
		return "loading…"
	}
	if r.err != nil {
		return fmt.Sprintf("(load error: %s)", r.err)
	}
	width := r.width
	if width < 1 {
		width = 1
	}

	rows := r.flatRows()
	cursorRow, hasCursor := r.cursorFlatRow(rows)

	// stickyIdx is the index of the section-header row that should be pinned
	// to the first line, or -1 when no sticky is needed (no cursor, height
	// too small, or the section's real header is already the top visible
	// row — drawing it twice would just duplicate the line).
	stickyIdx := -1
	if hasCursor && r.height >= 2 {
		stickyIdx = sectionHeaderRowOf(rows, rows[cursorRow].sectionIdx)
		if stickyIdx >= 0 && stickyIdx == r.yOffset {
			stickyIdx = -1
		}
	}

	start, end := 0, len(rows)
	if r.height > 0 {
		start = r.yOffset
		if start < 0 {
			start = 0
		}
		if start > end {
			start = end
		}
		bodyRows := r.height
		if stickyIdx >= 0 {
			bodyRows = r.visibleHeight()
		}
		if bodyEnd := start + bodyRows; bodyEnd < end {
			end = bodyEnd
		}
	}

	var b strings.Builder
	wroteAny := false
	if stickyIdx >= 0 {
		b.WriteString(r.renderRow(rows[stickyIdx], width, false))
		wroteAny = true
	}
	for i := start; i < end; i++ {
		if wroteAny {
			b.WriteByte('\n')
		}
		b.WriteString(r.renderRow(rows[i], width, i == cursorRow))
		wroteAny = true
	}
	return b.String()
}

// sectionHeaderRowOf returns the flatRows index of section sectionIdx's header,
// or -1 when the row list does not contain that header (shouldn't happen for
// valid sectionIdx in 0..2).
func sectionHeaderRowOf(rows []refRow, sectionIdx int) int {
	for i, row := range rows {
		if row.kind == refRowHeader && row.sectionIdx == sectionIdx {
			return i
		}
	}
	return -1
}

func renderRefLine(ref git.Ref, width int, selected bool) string {
	const prefixWidth = 2

	// HEAD '*' always wins the prefix slot so HEAD stays identifiable even
	// when the cursor sits on it. Selection is conveyed by bold + color on the
	// name itself.
	prefix := "  "
	if ref.IsHead {
		prefix = cursorStyle.Render("*") + " "
	}

	avail := width - prefixWidth
	if avail < 1 {
		return prefix
	}
	name := runewidth.Truncate(ref.ShortName, avail, "…")
	switch {
	case selected:
		name = selectedStyle.Render(name)
	case ref.IsHead:
		name = cursorStyle.Render(name)
	}
	return prefix + name
}

func (r *refModel) SetSize(w, h int) {
	if r.width == w && r.height == h {
		return
	}
	r.width = w
	r.height = h
	if r.loaded {
		*r = r.scrollCursorIntoView()
	}
}
