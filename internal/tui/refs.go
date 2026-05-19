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
	// onLocalChanges flags that the sticky "● Local Changes" row is the
	// current focus instead of a ref or worktree. Kept as a separate flag
	// so cursor semantics ("n-th selectable ref") stay the same — every
	// SelectBy* helper / persist path is unchanged.
	onLocalChanges bool
	// onWorktree is -1 when the cursor is not on a worktree row, otherwise
	// the 0-indexed position of the selected worktree. Worktree rows are
	// outside the n-th-ref cursor space so they get their own flag,
	// matching the onLocalChanges pattern.
	onWorktree int
	// worktrees is the porcelain-list snapshot rendered at the very top
	// of the sidebar (sticky inventory). currentWorktreePath marks which
	// entry is the active one — rendered with a ▶ prefix + bold + selected
	// color so the user can see at a glance which tree the rest of the
	// sidebar describes. worktreeDirty maps each entry.Path to its dirty
	// state from the per-tree fan-out; absent entries render without a
	// dirty marker (still loading). worktreeTimedOut maps the paths whose
	// dirty fan-out exceeded the per-goroutine timeout, so the row can
	// render a `?` placeholder instead of misleadingly showing "clean".
	worktrees           []git.Worktree
	currentWorktreePath string
	worktreeDirty       map[string]bool
	worktreeTimedOut    map[string]bool
	// localChangesSummary feeds the inline meta on the `● Local Changes`
	// sticky row (`N files · +X -Y · Zm ago`). Empty() == true means render
	// the bare label; the freshness clock comes from
	// localChangesSummaryLoadedAt so the meta stays meaningful even when the
	// last status reload reported zero changes.
	localChangesSummary         git.LocalChangesSummary
	localChangesSummaryLoadedAt time.Time
}

func newRefsModel() refModel { return refModel{onWorktree: -1} }

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

// refWorktreeSwitchRequestedMsg is emitted when the user presses Enter on
// a worktree row in the sidebar. The Model translates the path into a
// switchWorktreeMsg (the existing seam shared with the legacy modal
// switch flow).
type refWorktreeSwitchRequestedMsg struct{ path string }

// refWorktreeAddRequestedMsg / refWorktreeRemoveRequestedMsg fire when
// the user presses `a` / `d` on a worktree row. The Model opens the
// matching action sub-modal (add input / remove confirm) — the row's
// cursor context already names the target for remove; add derives its
// path from m.workdir's parent.
type refWorktreeAddRequestedMsg struct{}
type refWorktreeRemoveRequestedMsg struct{ target git.Worktree }

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
			if wt, ok := r.SelectedWorktree(); ok {
				return r, func() tea.Msg { return refWorktreeSwitchRequestedMsg{path: wt.Path} }
			}
			if r.onLocalChanges {
				return r, func() tea.Msg { return localChangesEnterRequestedMsg{} }
			}
			if ref, ok := r.Selected(); ok {
				return r, func() tea.Msg { return refCheckoutRequestedMsg{ref: ref} }
			}
			return r, nil
		case "o":
			if r.onLocalChanges || r.onWorktree != -1 {
				return r, nil
			}
			if ref, ok := r.Selected(); ok {
				return r, func() tea.Msg { return refSelectedMsg{ref: ref} }
			}
			return r, nil
		case "a":
			// Worktree-row context only: 'a' triggers add-worktree. On any
			// other row, swallow (refs panes used to have an `a` "all refs"
			// label but the handler was always a no-op; PR 4 cut the label).
			if r.onWorktree != -1 {
				return r, func() tea.Msg { return refWorktreeAddRequestedMsg{} }
			}
			return r, nil
		}
		return r.handleKey(m), nil
	}
	return r, nil
}

func (r refModel) handleKey(msg tea.KeyMsg) refModel {
	total := r.selectableCount()
	wtCount := len(r.worktrees)
	switch msg.String() {
	case "j", "down":
		if r.onWorktree != -1 {
			// Walk to the next worktree row, or fall through to Local
			// Changes when leaving the worktree section.
			if r.onWorktree < wtCount-1 {
				r.onWorktree++
				r = r.scrollCursorIntoView()
				return r
			}
			r.onWorktree = -1
			r.onLocalChanges = true
			r = r.scrollCursorIntoView()
			return r
		}
		if r.onLocalChanges {
			// Leaving the sticky row downward → land on the first
			// selectable ref. scrollCursorIntoView keeps the sticky in
			// view as the user begins scrolling.
			r.onLocalChanges = false
			r.cursor = 0
			r = r.scrollCursorIntoView()
			return r
		}
		if r.cursor < total-1 {
			r.cursor++
			r = r.scrollCursorIntoView()
		}
	case "k", "up":
		if r.onWorktree != -1 {
			if r.onWorktree == 0 {
				// At the very top of the worktree section — nowhere to go.
				return r
			}
			r.onWorktree--
			r = r.scrollCursorIntoView()
			return r
		}
		if r.onLocalChanges {
			// Step up into the worktree section if any entries exist.
			if wtCount > 0 {
				r.onLocalChanges = false
				r.onWorktree = wtCount - 1
				r = r.scrollCursorIntoView()
			}
			return r
		}
		if r.cursor == 0 {
			r.onLocalChanges = true
			r = r.scrollCursorIntoView()
			return r
		}
		r.cursor--
		r = r.scrollCursorIntoView()
	case "g":
		// Jump to the top of the visible inventory: first worktree if any,
		// otherwise the Local Changes sticky row.
		if wtCount > 0 {
			r.onLocalChanges = false
			r.onWorktree = 0
		} else {
			r.onLocalChanges = true
			r.onWorktree = -1
		}
		r.cursor = 0
		r = r.scrollCursorIntoView()
	case "G":
		if total > 0 {
			r.onLocalChanges = false
			r.onWorktree = -1
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
	cursorRow, ok := r.activeFlatRow(rows)
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

// SetLocalChangesSummary publishes the latest numstat + reload-time into the
// sidebar so the sticky row's inline meta can render. loadedAt is the wall
// clock of the most recent successful status reload; the row formats it as
// "Xs/m/h ago" via humanizeAge(now-loadedAt).
func (r *refModel) SetLocalChangesSummary(summary git.LocalChangesSummary, loadedAt time.Time) {
	r.localChangesSummary = summary
	r.localChangesSummaryLoadedAt = loadedAt
}

// ResetLocalChangesSummary clears the inline meta. The Model layer calls
// this when the working tree's freshness signal is no longer trustworthy
// (e.g. directory change), so the sidebar falls back to the bare label
// instead of showing stale "5m ago" math.
func (r *refModel) ResetLocalChangesSummary() {
	r.localChangesSummary = git.LocalChangesSummary{}
	r.localChangesSummaryLoadedAt = time.Time{}
}

// SelectedWorktree returns the worktree under the cursor, if the cursor is
// on a worktree row. The Model uses it to dispatch enter / a / d actions
// against the right entry.
func (r refModel) SelectedWorktree() (git.Worktree, bool) {
	if r.onWorktree < 0 || r.onWorktree >= len(r.worktrees) {
		return git.Worktree{}, false
	}
	return r.worktrees[r.onWorktree], true
}

// SetWorktrees rewrites the sidebar's worktree section. currentPath marks
// which entry to render with the ▶ + bold cursor highlight (the active
// worktree the rest of the sidebar describes). worktreeDirty/timedOut
// state is preserved across calls — only paths that disappear are pruned.
func (r *refModel) SetWorktrees(entries []git.Worktree, currentPath string) {
	r.worktrees = entries
	r.currentWorktreePath = currentPath
	if r.worktreeDirty == nil {
		r.worktreeDirty = make(map[string]bool)
	}
	if r.worktreeTimedOut == nil {
		r.worktreeTimedOut = make(map[string]bool)
	}
	// Prune stale entries.
	live := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		live[e.Path] = struct{}{}
	}
	for p := range r.worktreeDirty {
		if _, ok := live[p]; !ok {
			delete(r.worktreeDirty, p)
		}
	}
	for p := range r.worktreeTimedOut {
		if _, ok := live[p]; !ok {
			delete(r.worktreeTimedOut, p)
		}
	}
	// Cursor may have been on an entry that just disappeared.
	if r.onWorktree >= len(entries) {
		r.onWorktree = -1
	}
}

// SetWorktreeDirty records the dirty state for one path from the per-tree
// fan-out. timedOut=true means the per-goroutine 3s budget was exhausted;
// the row renders a `?` placeholder instead of trusting the (likely zero)
// dirty value.
func (r *refModel) SetWorktreeDirty(path string, dirty, timedOut bool) {
	if r.worktreeDirty == nil {
		r.worktreeDirty = make(map[string]bool)
	}
	if r.worktreeTimedOut == nil {
		r.worktreeTimedOut = make(map[string]bool)
	}
	r.worktreeDirty[path] = dirty
	if timedOut {
		r.worktreeTimedOut[path] = true
	} else {
		delete(r.worktreeTimedOut, path)
	}
}

// Worktrees returns the current sidebar snapshot. Model uses it to drive
// the post-load dirty fan-out without exposing the field directly.
func (r refModel) Worktrees() []git.Worktree { return r.worktrees }

// WorktreeDirty reports whether path is currently marked dirty in the
// sidebar's fan-out result map. False covers both "clean" and "not yet
// loaded"; the remove-confirm modal uses it to decide whether the force
// path is needed.
func (r refModel) WorktreeDirty(path string) bool {
	return r.worktreeDirty[path]
}

func (r refModel) selectableCount() int {
	total := 0
	for i := range r.byKind {
		total += len(r.byKind[i])
	}
	return total
}

// partitionByKind sorts refs into the [local, remote, tag] section slots,
// applying the Q5 remote filter: a remote-tracking ref whose stripped
// name matches a local branch is hidden. Solo-dev workflow has ~99% of
// remotes mirrored by a local, so the sidebar reads as a clean list of
// "what's only on the remote" — zombie / cross-machine branches still
// surface, but the redundant mirror noise is gone.
func partitionByKind(refs []git.Ref) [3][]git.Ref {
	// Collect local names first so the remote-filter walk has the set
	// ready in one pass.
	localNames := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if ref.Kind == git.RefKindLocal {
			localNames[ref.ShortName] = struct{}{}
		}
	}
	var out [3][]git.Ref
	for i, sec := range refSections {
		for _, ref := range refs {
			if ref.Kind != sec.kind {
				continue
			}
			if sec.kind == git.RefKindRemote {
				if _, mirrored := localNames[git.CheckoutTarget(ref)]; mirrored {
					continue
				}
			}
			out[i] = append(out[i], ref)
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
	// refRowWorktreeHeader marks the "Worktrees" section label rendered
	// at the very top of the pane (above the worktree rows).
	refRowWorktreeHeader
	// refRowWorktree is one worktree entry inside the sticky inventory
	// section. Selected state is driven by onWorktree (the entry index),
	// so the normal n-th-ref cursor still indexes refs only.
	refRowWorktree
)

type refRow struct {
	kind       refRowKind
	sectionIdx int
	refIdx     int // valid only for refRowRef
	wtIdx      int // valid only for refRowWorktree
}

// flatRows expands the sections into a flat row list in render order:
// Worktrees header → worktree rows (sticky inventory) → gap → sticky
// Local Changes → gap → ref-section header → empty/refs. This is the
// index space visible-window slicing and scroll math share.
func (r refModel) flatRows() []refRow {
	var rows []refRow
	if len(r.worktrees) > 0 {
		rows = append(rows, refRow{kind: refRowWorktreeHeader})
		for i := range r.worktrees {
			rows = append(rows, refRow{kind: refRowWorktree, wtIdx: i})
		}
		rows = append(rows, refRow{kind: refRowGap})
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

// activeFlatRow returns the flat index of whichever row currently owns
// the visual cursor — worktree row, Local Changes sticky, or n-th ref —
// depending on the onWorktree / onLocalChanges flags. scroll math and
// View() use this single source so they stay in sync across the three
// cursor regions.
func (r refModel) activeFlatRow(rows []refRow) (int, bool) {
	switch {
	case r.onWorktree != -1:
		for i, row := range rows {
			if row.kind == refRowWorktree && row.wtIdx == r.onWorktree {
				return i, true
			}
		}
		return -1, false
	case r.onLocalChanges:
		for i, row := range rows {
			if row.kind == refRowLocalChanges {
				return i, true
			}
		}
		return -1, false
	}
	return r.cursorFlatRow(rows)
}

func (r refModel) renderRow(row refRow, width int, selected bool) string {
	switch row.kind {
	case refRowWorktreeHeader:
		return refHeaderStyle.Render(runewidth.Truncate("Worktrees", width, "…"))
	case refRowWorktree:
		wt := r.worktrees[row.wtIdx]
		isCurrent := wt.Path == r.currentWorktreePath
		dirtyMark := ""
		if r.worktreeTimedOut[wt.Path] {
			dirtyMark = "?"
		} else if r.worktreeDirty[wt.Path] {
			dirtyMark = "●"
		}
		return renderWorktreeSidebarRow(wt, isCurrent, selected, dirtyMark, width)
	case refRowLocalChanges:
		return r.renderLocalChangesRow(width, selected, time.Now())
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

// renderLocalChangesRow composes the sticky `● Local Changes` row including
// the inline meta `N files · +X -Y · Zm ago` when a summary is loaded. The
// label always gets accent weight (cursor highlight when unselected, bold
// + highlight when selected) so the row reads as a cockpit signal, not just
// a button. Meta is rendered dim so it stays peripheral information.
func (r refModel) renderLocalChangesRow(width int, selected bool, now time.Time) string {
	label := "● Local Changes"
	meta := r.formatLocalChangesMeta(now)
	return composeLocalChangesRow(label, meta, width, selected)
}

// formatLocalChangesMeta builds the inline meta string. Returns "" when the
// summary carries no signal so the sidebar falls back to a bare label
// instead of showing `0 files · +0 -0`. "just now" (< 1m) is rendered
// without an " ago" suffix since the phrase already reads as a moment.
func (r refModel) formatLocalChangesMeta(now time.Time) string {
	if r.localChangesSummary.Empty() {
		return ""
	}
	s := r.localChangesSummary
	filesWord := "files"
	if s.FilesChanged == 1 {
		filesWord = "file"
	}
	parts := []string{
		fmt.Sprintf("%d %s", s.FilesChanged, filesWord),
		fmt.Sprintf("+%d -%d", s.Insertions, s.Deletions),
	}
	if !r.localChangesSummaryLoadedAt.IsZero() {
		age := relativeShortAt(r.localChangesSummaryLoadedAt, now)
		if age == "just now" {
			parts = append(parts, age)
		} else {
			parts = append(parts, age+" ago")
		}
	}
	return strings.Join(parts, " · ")
}

// composeLocalChangesRow lays out label + meta against a width budget. When
// the meta doesn't fit, it's truncated (with `…`) before the label is — the
// label is the row's primary identity and must stay readable. Pulled out
// of renderLocalChangesRow so tests can pin layout behavior without
// faking `time.Now`.
func composeLocalChangesRow(label, meta string, width int, selected bool) string {
	labelStyle := cursorStyle
	if selected {
		labelStyle = selectedStyle
	}
	if meta == "" {
		text := runewidth.Truncate(label, width, "…")
		return labelStyle.Render(text)
	}
	const sep = "  "
	labelW := runewidth.StringWidth(label)
	sepW := runewidth.StringWidth(sep)
	if labelW+sepW >= width {
		text := runewidth.Truncate(label, width, "…")
		return labelStyle.Render(text)
	}
	availForMeta := width - labelW - sepW
	metaOut := meta
	if runewidth.StringWidth(meta) > availForMeta {
		metaOut = runewidth.Truncate(meta, availForMeta, "…")
	}
	return labelStyle.Render(label) + sep + timeStyle.Render(metaOut)
}

// renderWorktreeSidebarRow formats one worktree entry inside the sidebar
// inventory. Current worktree (the one m.workdir lives in) is prefixed
// with `▶` and bolded; others get a 2-col indent so the rows line up. The
// dirtyMark (`●` clean-fail, `?` for fan-out timeout, "" for clean / not
// yet loaded) sits after the branch label. Truncate via runewidth so a
// long worktree name doesn't blow up the layout.
func renderWorktreeSidebarRow(wt git.Worktree, isCurrent, selected bool, dirtyMark string, width int) string {
	const prefixWidth = 2
	prefix := "  "
	if isCurrent {
		prefix = cursorStyle.Render("▶") + " "
	}
	name := wt.Path
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	parts := []string{name}
	switch {
	case wt.Detached:
		parts = append(parts, "(detached)")
	case wt.Branch != "":
		parts = append(parts, wt.Branch)
	}
	if dirtyMark != "" {
		parts = append(parts, dirtyMark)
	}
	body := strings.Join(parts, " · ")
	avail := width - prefixWidth
	if avail < 1 {
		return prefix
	}
	body = runewidth.Truncate(body, avail, "…")
	switch {
	case selected:
		body = selectedStyle.Render(body)
	case isCurrent:
		body = cursorStyle.Render(body)
	}
	return prefix + body
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
	cursorRow, _ := r.activeFlatRow(rows)

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
