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
	// onLocalChanges flags that the sticky "● Local Changes" row at the
	// top of the pane is the current focus instead of a ref. Kept as a
	// separate flag so cursor semantics ("n-th selectable ref") stay the
	// same — every SelectBy* helper / persist path is unchanged.
	onLocalChanges bool
	// worktreeHeader is the preformatted one-line "Worktree: <name> ·
	// <branch> · ●dirty" string painted at the very top of the pane. The
	// Model layer rewrites it on every refsLoadedMsg / switchWorktreeMsg /
	// dirty status result so the header tracks the live tree without the
	// view layer ever computing it. Empty string hides the row entirely.
	worktreeHeader string
}

func newRefsModel() refModel { return refModel{} }

type refsLoadedMsg struct{ refs []git.Ref }
type refsLoadFailedMsg struct{ err error }

// refSelectedMsg is emitted when the user presses 'o' on a ref. The root
// model uses it to jump the graph cursor onto the ref's tip commit.
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
// here — refsLoadedMsg resets it to 0 on arrival, and the Model layer's
// refsLoadedMsg handler then restores it from pendingRefCursorPersist (or
// SelectAfterDeleted in the same section when the previously focused ref
// was deleted between snapshot and reload).
func (r *refModel) ResetForReload() {
	r.loaded = false
	r.err = nil
}

func (r refModel) Update(msg tea.Msg) (refModel, tea.Cmd) {
	switch m := msg.(type) {
	case refsLoadedMsg:
		// cursor=0 / yOffset=0 stays as the baseline; the Model layer's
		// refsLoadedMsg handler runs after this and overwrites the cursor
		// from pendingRefCursorName / AfterDelete / Persist as appropriate.
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
			if r.onLocalChanges {
				return r, func() tea.Msg { return localChangesEnterRequestedMsg{} }
			}
			if ref, ok := r.Selected(); ok {
				return r, func() tea.Msg { return refCheckoutRequestedMsg{ref: ref} }
			}
			return r, nil
		case "o":
			if r.onLocalChanges {
				return r, nil
			}
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
		if r.onLocalChanges {
			// Leaving the sticky row downward → land on the first
			// selectable ref. yOffset=0 keeps the sticky in view as the
			// user begins scrolling.
			r.onLocalChanges = false
			r.cursor = 0
			r.yOffset = 0
			return r
		}
		if r.cursor < total-1 {
			r.cursor++
			r = r.scrollCursorIntoView()
		}
	case "k", "up":
		if r.onLocalChanges {
			return r
		}
		if r.cursor == 0 {
			r.onLocalChanges = true
			r.yOffset = 0
			return r
		}
		r.cursor--
		r = r.scrollCursorIntoView()
	case "g":
		r.onLocalChanges = true
		r.cursor = 0
		r.yOffset = 0
	case "G":
		if total > 0 {
			r.onLocalChanges = false
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
// LocalRefs returns the cached local-branch slice. Other panes reach for
// it (e.g., graph Enter's chip evaluator) without learning byKind's
// section-index encoding — refSections owns that detail.
func (r refModel) LocalRefs() []git.Ref { return r.byKind[0] }

// RemoteRefs returns the cached remote-tracking slice (origin/<branch>
// entries). The graph Enter evaluator uses it to detect cursor rows
// carrying only a remote chip — those drive the "checkout local that
// tracks this remote, then FF" cross-branch path.
func (r refModel) RemoteRefs() []git.Ref { return r.byKind[1] }

// SelectByName moves the cursor onto the first ref whose ShortName matches.
// Search order is the visible section order (local → remote → tag) so a
// post-create / post-rename jump lands on the local row even when a
// same-named remote-tracking ref exists. Returns true on a hit. yOffset is
// updated through scrollCursorIntoView so the new cursor row is visible.
func (r *refModel) SelectByName(name string) bool {
	if !r.loaded {
		return false
	}
	idx := 0
	for _, section := range r.byKind {
		for _, ref := range section {
			if ref.ShortName == name {
				r.cursor = idx
				*r = r.scrollCursorIntoView()
				return true
			}
			idx++
		}
	}
	return false
}

// SelectByNameKind is like SelectByName but constrained to the section
// whose kind matches. Used by the reload-cursor-persist restore path so a
// remote ref with the same ShortName as a local branch can't pull the
// cursor across sections after a reload.
func (r *refModel) SelectByNameKind(name string, kind git.RefKind) bool {
	if !r.loaded {
		return false
	}
	idx := 0
	for i, sec := range refSections {
		if sec.kind != kind {
			idx += len(r.byKind[i])
			continue
		}
		for _, ref := range r.byKind[i] {
			if ref.ShortName == name {
				r.cursor = idx
				*r = r.scrollCursorIntoView()
				return true
			}
			idx++
		}
		return false
	}
	return false
}

// SelectByNameKindOrNeighbor tries SelectByNameKind first; on a miss it
// falls back to SelectAfterDeleted in the same section so the cursor lands
// on the alphabetical neighbor (or previous row when the missing entry
// was last). Used by the persist-restore path when the previously-focused
// ref was removed between snapshot and reload.
func (r *refModel) SelectByNameKindOrNeighbor(name string, kind git.RefKind) {
	if r.SelectByNameKind(name, kind) {
		return
	}
	r.SelectAfterDeleted(name, kind)
}

// SelectAfterDeleted positions the cursor as if `prevName` used to occupy a
// row in the section identified by `kind`, picking the row that would now
// be "next" in flat order — or the previous row if the deleted entry was
// last in that section. Scoping to a single section is necessary so the
// cursor doesn't bleed into a neighboring section just because that
// section's first entry happens to sort alphabetically after `prevName`.
func (r *refModel) SelectAfterDeleted(prevName string, kind git.RefKind) {
	if !r.loaded {
		return
	}
	sectionIdx := -1
	for i, sec := range refSections {
		if sec.kind == kind {
			sectionIdx = i
			break
		}
	}
	if sectionIdx == -1 {
		return
	}
	// Walk to the start of the matching section in flat-row space.
	flatIdx := 0
	for i := 0; i < sectionIdx; i++ {
		flatIdx += len(r.byKind[i])
	}
	// Inside the section, take the alphabetical insertion point of prevName.
	// for-each-ref already returned refs sorted, so the surviving section
	// stays sorted.
	section := r.byKind[sectionIdx]
	for _, ref := range section {
		if ref.ShortName > prevName {
			r.cursor = flatIdx
			*r = r.scrollCursorIntoView()
			return
		}
		flatIdx++
	}
	// prevName was last in its section. Step back one row when possible so
	// the cursor stays in the same section instead of jumping forward.
	if len(section) > 0 {
		r.cursor = flatIdx - 1
		*r = r.scrollCursorIntoView()
		return
	}
	// Section emptied entirely — clamp to the new total.
	total := r.selectableCount()
	if total == 0 {
		r.cursor = 0
		r.yOffset = 0
		return
	}
	if r.cursor >= total {
		r.cursor = total - 1
	}
	*r = r.scrollCursorIntoView()
}

func (r refModel) Selected() (git.Ref, bool) {
	if r.onLocalChanges {
		return git.Ref{}, false
	}
	rows := r.flatRows()
	i, ok := r.cursorFlatRow(rows)
	if !ok {
		return git.Ref{}, false
	}
	row := rows[i]
	return r.byKind[row.sectionIdx][row.refIdx], true
}

// IsLocalChangesSelected reports whether the sticky `● Local Changes` row at
// the top of the pane is the current focus. The Model layer uses this to
// route enter on the refs pane into the mode-toggle path.
func (r refModel) IsLocalChangesSelected() bool { return r.onLocalChanges }

// SetWorktreeHeader rewrites the sticky one-line worktree summary at the
// very top of the pane. Empty string hides the row.
func (r *refModel) SetWorktreeHeader(s string) { r.worktreeHeader = s }

func (r refModel) selectableCount() int {
	total := 0
	for i := range r.byKind {
		total += len(r.byKind[i])
	}
	return total
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
	// refRowLocalChanges is the sticky `● Local Changes` row that lives
	// above every section. Selected state is driven by onLocalChanges
	// (not r.cursor), so the normal cursorFlatRow lookup keeps its
	// "n-th ref" semantics.
	refRowLocalChanges
	// refRowWorktreeHeader is the one-line summary of the current
	// worktree, painted above refRowLocalChanges when the Model layer has
	// set worktreeHeader. Never selectable.
	refRowWorktreeHeader
)

type refRow struct {
	kind       refRowKind
	sectionIdx int
	refIdx     int // valid only for refRowRef
}

// flatRows expands the sections into a flat row list in render order:
// optional worktree header → sticky Local Changes → gap → section header
// → empty/refs. This is the index space visible-window slicing and scroll
// math share.
func (r refModel) flatRows() []refRow {
	var rows []refRow
	if r.worktreeHeader != "" {
		rows = append(rows, refRow{kind: refRowWorktreeHeader})
	}
	rows = append(rows, refRow{kind: refRowLocalChanges})
	for i := range refSections {
		rows = append(rows, refRow{kind: refRowGap, sectionIdx: i})
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
	case refRowWorktreeHeader:
		return refHeaderStyle.Render(runewidth.Truncate(r.worktreeHeader, width, "…"))
	case refRowLocalChanges:
		text := runewidth.Truncate("● Local Changes", width, "…")
		if selected {
			return selectedStyle.Render(text)
		}
		return text
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
	if r.onLocalChanges {
		// Highlight the sticky row instead of any ref. With the optional
		// worktree-header row above it, the sticky's flat index isn't
		// constant anymore — scan for it.
		for i, row := range rows {
			if row.kind == refRowLocalChanges {
				cursorRow = i
				break
			}
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
