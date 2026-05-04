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
	// byKind caches refs partitioned in render order so View / Selected /
	// keypress bounds checks don't re-walk r.refs on every frame. Index
	// matches refSections.
	byKind [3][]git.Ref
	width  int
	height int
	cursor int
	// yOffset is the first flat-row index visible inside the pane. Lazy
	// scroll moves it ±1 only when cursor reaches the visible window edge.
	yOffset int
	// folded[i] toggles section i (Local/Remote/Tags) between expanded and
	// collapsed. Zero value = all expanded, matching the previous behaviour.
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
			r = r.ensureCursorVisible(false)
		}
	case "k", "up":
		if r.cursor > 0 {
			r.cursor--
			r = r.ensureCursorVisible(false)
		}
	case "g":
		r.cursor = 0
		r.yOffset = 0
	case "G":
		if total > 0 {
			r.cursor = total - 1
			r = r.ensureCursorVisible(true)
		}
	}
	return r
}

// visibleHeight is how many flat-rows fit under the sticky header. We always
// reserve 1 row for the sticky line when height >= 2, even when the sticky is
// suppressed (cursor sits on the row right after its section header) — the
// reserved row stays empty in that frame, which is fine and keeps the scroll
// math frame-independent.
func (r refModel) visibleHeight() int {
	if r.height <= 0 {
		return 0
	}
	if r.height < 2 {
		return r.height
	}
	return r.height - 1
}

// ensureCursorVisible nudges yOffset so the cursor row stays inside the
// visible window. step (jump=false) follows the lazy rule from the plan: only
// shift by 1 when the cursor has moved exactly to the row above/below the
// window. jump (jump=true) does a one-shot correction so the cursor lands
// inside the window after g/G/z, even if it was far away.
func (r refModel) ensureCursorVisible(jump bool) refModel {
	rows := r.flatRows()
	cursorRow, ok := r.cursorFlatRow(rows)
	if !ok {
		return r
	}
	vh := r.visibleHeight()
	if vh <= 0 {
		return r
	}
	if jump {
		if cursorRow < r.yOffset {
			r.yOffset = cursorRow
		} else if cursorRow >= r.yOffset+vh {
			r.yOffset = cursorRow - vh + 1
		}
	} else {
		if cursorRow == r.yOffset-1 {
			r.yOffset--
		} else if cursorRow == r.yOffset+vh {
			r.yOffset++
		}
	}
	if r.yOffset < 0 {
		r.yOffset = 0
	}
	max := len(rows) - vh
	if max < 0 {
		max = 0
	}
	if r.yOffset > max {
		r.yOffset = max
	}
	return r
}

// Selected returns the ref under the cursor, if any. Headers, gaps, and
// empty-section placeholders are not selectable — and refs in folded sections
// drop out of the count entirely so the cursor only ever lands on visible refs.
func (r refModel) Selected() (git.Ref, bool) {
	idx := r.cursor
	if idx < 0 {
		return git.Ref{}, false
	}
	for i, items := range r.byKind {
		if r.folded[i] {
			continue
		}
		if idx < len(items) {
			return items[idx], true
		}
		idx -= len(items)
	}
	return git.Ref{}, false
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

// refRowKind tags every visible line so View can slice by yOffset and so the
// sticky-header pass (later step) can find headers without re-walking byKind.
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
		max := start + r.height
		if stickyIdx >= 0 {
			max = start + r.height - 1
		}
		if max < end {
			end = max
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
	r.width = w
	r.height = h
}
