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
	loaded  bool
	err     error
}

func newRefsModel() refModel { return refModel{} }

type refsLoadedMsg struct{ refs []git.Ref }
type refsLoadFailedMsg struct{ err error }

// refSelectedMsg is emitted when the user presses 'o' on a ref. The root
// model uses it to jump the graph cursor onto the ref's tip commit. (This
// used to be Enter's job; Enter now triggers checkout.)
type refSelectedMsg struct{ ref git.Ref }

// refCheckoutRequestedMsg is emitted when the user presses Enter on a ref.
// The root model is responsible for translating the ref into the right
// `git checkout` argument — for remote-tracking refs the "<remote>/"
// prefix is stripped so git's dwim creates a local tracking branch; local
// branches and tags pass through verbatim.
type refCheckoutRequestedMsg struct{ ref git.Ref }

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
		switch m.String() {
		case "enter":
			if ref, ok := r.Selected(); ok {
				return r, func() tea.Msg { return refCheckoutRequestedMsg{ref: ref} }
			}
			return r, nil
		case "o":
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
			r = r.scrollCursorIntoView()
		}
	case "k", "up":
		if r.cursor > 0 {
			r.cursor--
			r = r.scrollCursorIntoView()
		}
	case "g":
		r.cursor = 0
		r.yOffset = 0
	case "G":
		if total > 0 {
			r.cursor = total - 1
			r = r.scrollCursorIntoView()
		}
	}
	return r
}

// scrollCursorIntoView pulls yOffset so the cursor row is inside the window
// in one shot. Used by g/G/z and SetSize, where the cursor may have jumped
// far from the previous offset. When the cursor moves above the viewport, we
// prefer to pull yOffset up to the cursor's section header so the user sees
// which section they're in — but only if header+cursor still fit in height.
func (r refModel) scrollCursorIntoView() refModel {
	rows := r.flatRows()
	cursorRow, ok := r.cursorFlatRow(rows)
	if !ok {
		return r
	}
	if r.height <= 0 {
		return r
	}
	if cursorRow < r.yOffset {
		start := sectionStartRow(rows, cursorRow)
		if cursorRow-start < r.height {
			r.yOffset = start
		} else {
			r.yOffset = cursorRow
		}
	} else if cursorRow >= r.yOffset+r.height {
		r.yOffset = cursorRow - r.height + 1
	}
	return r.clampOffset(len(rows), r.height)
}

// sectionStartRow walks up from cursorRow to find the nearest header row.
// Used to keep the section header attached to its first ref when scrolling
// upward.
func sectionStartRow(rows []refRow, cursorRow int) int {
	for i := cursorRow; i >= 0; i-- {
		if rows[i].kind == refRowHeader {
			return i
		}
	}
	return 0
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

// Selected returns the ref under the cursor, if any.
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
	return len(r.byKind[0]) + len(r.byKind[1]) + len(r.byKind[2])
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

// refRowKind tags every visible line so View can slice by yOffset.
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
// gap (between sections), header, then either empty placeholder or the ref
// list. This is the index space visible-window slicing and scroll math share.
func (r refModel) flatRows() []refRow {
	var rows []refRow
	for i := range refSections {
		if i > 0 {
			rows = append(rows, refRow{kind: refRowGap, sectionIdx: i})
		}
		rows = append(rows, refRow{kind: refRowHeader, sectionIdx: i})
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
// Returns false when there is no selectable ref (every section is empty).
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
	cursorRow, _ := r.cursorFlatRow(rows)

	start, end := 0, len(rows)
	if r.height > 0 {
		start = r.yOffset
		if start < 0 {
			start = 0
		}
		if start > end {
			start = end
		}
		if bodyEnd := start + r.height; bodyEnd < end {
			end = bodyEnd
		}
	}

	var b strings.Builder
	for i := start; i < end; i++ {
		if i > start {
			b.WriteByte('\n')
		}
		b.WriteString(r.renderRow(rows[i], width, i == cursorRow))
	}
	return b.String()
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
