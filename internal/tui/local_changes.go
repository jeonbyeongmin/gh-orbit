package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// localChangesSection groups entries in the file tree. Ordering matches the
// render order — conflicts surface to the top so resolution work isn't
// buried under benign modified-in-worktree noise.
type localChangesSection int

const (
	sectionConflicts localChangesSection = iota
	sectionUnstaged
	sectionStaged
)

// localChangesPane is the sub-focus within the right column when the mode is
// active. Outer Model.focused tracks "refs vs. right column"; this enum
// tracks "tree vs. diff" inside the right column.
type localChangesPane int

const (
	paneLCTree localChangesPane = iota
	paneLCDiff
)

// localChangesEntry is one row in the file tree. A single status entry can
// produce two rows (e.g. a tracked file that's both worktree-modified and
// index-modified gives one Unstaged row and one Staged row), so this is not
// the same as a git.StatusEntry.
//
// IndexState / WorktreeState carry the porcelain XY bytes from the source so
// the row label can render the right marker ("M"/"A"/"D"/"R"/...).
type localChangesEntry struct {
	Path          string
	OrigPath      string // populated when the source git.StatusEntry was a rename
	Section       localChangesSection
	IndexState    byte
	WorktreeState byte
	Untracked     bool
	Conflict      bool
}

// Staged reports whether this entry represents the index side of its file.
// The caller uses it to pick git diff --cached vs. git diff when loading the
// diff viewport, and to pick `git restore --staged` vs. `git add` on `space`.
func (e localChangesEntry) Staged() bool { return e.Section == sectionStaged }

// localChangesModel hosts the file tree + diff viewport for viewModeLocalChanges.
// Input handling lives in model.go so this stays a pure data/view model —
// keeps tests simple and lets the mode-guard own all key routing.
type localChangesModel struct {
	entries []localChangesEntry
	cursor  int // selectable index (over entries only; headers excluded)
	yOffset int // first visible row in the flat row list (rows include headers)

	focused localChangesPane

	treeW, treeH int
	diffW, diffH int

	loaded  bool
	loadErr error

	diff        viewport.Model
	diffText    string // plain unified diff (source for hunk parsing + apply)
	rendered    string // diffText styled for the viewport (syntax + word-level), cached per load
	diffLoading bool
	diffErr     error

	// hunkStarts holds the line index (into diffText's lines) of each `@@`
	// hunk header, recomputed on every diff load. hunkCursor indexes into it
	// — the hunk `[`/`]` navigate and `space` stages while the diff pane is
	// focused. Both reset on each diff load. Empty for diffs with no hunks
	// (rename-only, "(no file)").
	hunkStarts []int
	hunkCursor int

	// srcToDisp maps each source line of the rendered diff to the display row it
	// wraps to (see wrapDiffLines), so scrollToHunk lands on the right row after
	// long lines fold. Rebuilt on every refreshDiffViewport (width/content
	// dependent).
	srcToDisp []int

	// diffReqID is the freshest dispatch id. ApplyDiffLoaded ignores stale
	// responses whose reqID doesn't match, so a slow git-diff for a file the
	// user already scrolled past never repaints the viewport. The counter
	// is monotonically incremented per dispatch, so reqID alone is enough
	// to gate staleness — no parallel (path, staged) tracking needed.
	diffReqID uint64

	// pendingSelectPath / pendingSelectPreferStaged carry a "after the next
	// status reload, land cursor on this path" hint across the round-trip
	// triggered by `space`. Cleared in ApplyStatusLoaded after consumption.
	pendingSelectPath         string
	pendingSelectPreferStaged bool

	// spinnerFrame is pushed in by Model on every spinnerTickMsg so the
	// loading placeholders animate. Only read while !loaded / diffLoading.
	spinnerFrame int

	// unstagedStat / stagedStat hold the per-path +/- counts rendered at the
	// right edge of each tree row, keyed by path and split by side (the counts
	// come from two separate `git diff --numstat` probes). Nil when the probe
	// failed or returned nothing; untracked paths never appear (no baseline).
	unstagedStat map[string]git.FileStat
	stagedStat   map[string]git.FileStat

	// sequencer is the in-progress cherry-pick / rebase / merge / revert (set
	// from each status load), driving the top-of-tree banner and whether `C`
	// (continue) / abort apply. SequencerNone in the normal case.
	sequencer git.SequencerKind
}

func newLocalChangesModel() localChangesModel {
	vp := viewport.New(0, 0)
	// Scroll is arrow-only — the j/k vim bindings were dropped, so override
	// viewport's default Up/Down (which include k/j).
	vp.KeyMap.Up = key.NewBinding(key.WithKeys("up"))
	vp.KeyMap.Down = key.NewBinding(key.WithKeys("down"))
	return localChangesModel{diff: vp}
}

// SetSize takes the tree column and diff column dimensions separately because
// the outer layout already splits the right column horizontally — there is no
// single "right column width" to subdivide internally.
func (m *localChangesModel) SetSize(treeW, treeH, diffW, diffH int) {
	m.treeW = treeW
	m.treeH = treeH
	m.diffW = diffW
	m.diffH = diffH
	widthChanged := diffW != m.diff.Width
	m.diff.Width = diffW
	// One row of the diff pane is the file header (diffHeader); the viewport
	// takes the rest, so it never paints over the header line.
	vpH := diffH - 1
	if vpH < 1 {
		vpH = 1
	}
	m.diff.Height = vpH
	if m.diffText != "" {
		if widthChanged {
			m.rendered = renderDiffContent(m.diffText, diffW)
		}
		// Go through refresh so the content is re-wrapped to the new width and
		// srcToDisp is rebuilt — a raw SetContent would leave long lines unfolded.
		m.refreshDiffViewport()
	}
	m.followCursor()
}

// ApplyStatusLoaded ingests a fresh git.Status snapshot, classifies each entry
// into conflicts / unstaged / staged, and clamps the cursor / scroll offset
// so a shrinking list never strands the selection past the end. If a stage /
// unstage round-trip left a `pendingSelectPath` hint, the cursor lands on
// that path's row in the preferred section so the user keeps context.
//
// A file that is both staged and worktree-modified produces two entries (one
// per side) — that matches the interview's section structure.
func (m *localChangesModel) ApplyStatusLoaded(src []git.StatusEntry) {
	m.entries = classifyStatus(src)
	m.loaded = true
	m.loadErr = nil
	m.clampCursor()
	if path := m.pendingSelectPath; path != "" {
		m.SelectByPath(path, m.pendingSelectPreferStaged)
		m.pendingSelectPath = ""
		m.pendingSelectPreferStaged = false
	}
	m.followCursor()
}

// ScheduleSelectAfterReload records a "next status reload should land
// cursor on this path" hint, consumed by ApplyStatusLoaded. preferStaged
// chooses between the file's Staged / Unstaged row when both exist.
func (m *localChangesModel) ScheduleSelectAfterReload(path string, preferStaged bool) {
	m.pendingSelectPath = path
	m.pendingSelectPreferStaged = preferStaged
}

// SetStats ingests the two numstat slices from a status reload, indexing each
// by path for O(1) row lookup. Paired with ApplyStatusLoaded — the caller
// applies both from the same localChangesStatusLoadedMsg.
func (m *localChangesModel) SetStats(unstaged, staged []git.FileStat) {
	m.unstagedStat = indexStats(unstaged)
	m.stagedStat = indexStats(staged)
}

func indexStats(fs []git.FileStat) map[string]git.FileStat {
	if len(fs) == 0 {
		return nil
	}
	out := make(map[string]git.FileStat, len(fs))
	for _, f := range fs {
		out[f.Path] = f
	}
	return out
}

// ApplyStatusFailed records a load error so the tree can render a one-liner
// instead of pretending the working tree is clean.
func (m *localChangesModel) ApplyStatusFailed(err error) {
	m.loaded = true
	m.loadErr = err
}

// BeginDiffLoad arms the model for a fresh diff dispatch. The caller picks
// the reqID (Model.localChangesReqID counter); the in-flight (path, staged)
// is implicit in the reqID since the counter is bumped per dispatch.
func (m *localChangesModel) BeginDiffLoad(reqID uint64) {
	m.diffReqID = reqID
	m.diffText = ""
	m.rendered = ""
	m.diffLoading = true
	m.diffErr = nil
	// Drop hunk state up front: between here and ApplyDiffLoaded the diff
	// belongs to no file, so a `space`/`[`/`]` pressed in the load window must
	// not act on the previous file's hunks. CurrentHunk → false until the new
	// diff lands.
	m.hunkStarts = nil
	m.hunkCursor = 0
	m.diff.SetContent("")
	m.diff.GotoTop()
}

// ApplyDiffLoaded paints the diff viewport with text, dropping stale
// responses whose reqID no longer matches the freshest dispatch.
func (m *localChangesModel) ApplyDiffLoaded(reqID uint64, text string) {
	if reqID != m.diffReqID {
		return
	}
	m.diffLoading = false
	m.diffText = text
	m.rendered = renderDiffContent(text, m.diff.Width)
	m.diffErr = nil
	m.hunkStarts = parseHunkStarts(text)
	m.hunkCursor = 0
	m.refreshDiffViewport()
	if m.focused == paneLCDiff && len(m.hunkStarts) > 0 {
		m.scrollToHunk()
	} else {
		m.diff.GotoTop()
	}
}

// ApplyDiffFailed records a diff load error so DiffView can show the cause.
// Stale errors get dropped just like stale successes.
func (m *localChangesModel) ApplyDiffFailed(reqID uint64, err error) {
	if reqID != m.diffReqID {
		return
	}
	m.diffLoading = false
	m.diffErr = err
}

// ClosePatch releases the diff text and viewport content. Mirrors
// diff.go's ClosePatch so an exit-then-re-enter cycle doesn't keep a
// large untracked-file diff resident.
func (m *localChangesModel) ClosePatch() {
	m.diffText = ""
	m.rendered = ""
	m.hunkStarts = nil
	m.hunkCursor = 0
	m.srcToDisp = nil
	m.diff.SetContent("")
}

// RerenderTheme repaints the loaded diff under the now-current activeDiffTheme
// (the Settings picker just cycled it), so the color change shows live behind
// the dialog. A no-op when no diff is loaded.
func (m *localChangesModel) RerenderTheme() {
	if m.diffText == "" {
		return
	}
	m.rendered = renderDiffContent(m.diffText, m.diff.Width)
	m.refreshDiffViewport()
}

// parseHunkStarts records the line index of every `@@` hunk header in the
// diff. It runs on the plain (uncolored) diff text; the ANSI strip is defensive
// so a stray escape can't hide a `@@` header.
func parseHunkStarts(diffText string) []int {
	if diffText == "" {
		return nil
	}
	var starts []int
	for i, ln := range strings.Split(diffText, "\n") {
		if strings.HasPrefix(ansi.Strip(ln), "@@") {
			starts = append(starts, i)
		}
	}
	return starts
}

// refreshDiffViewport repaints the viewport. When the diff pane is focused and
// has hunks, the selected hunk's `@@` header is reverse-highlighted so the
// reviewer sees which hunk `space` will stage. The header is ANSI-stripped
// before restyling so the reverse doesn't fight the rendered line's own color
// codes (a .Render over an already-styled line breaks on the inner reset).
func (m *localChangesModel) refreshDiffViewport() {
	if m.diffText == "" {
		m.srcToDisp = nil
		m.diff.SetContent("")
		return
	}
	if m.rendered == "" {
		m.rendered = renderDiffContent(m.diffText, m.diff.Width)
	}
	content := m.rendered
	if m.focused == paneLCDiff && len(m.hunkStarts) > 0 {
		lines := strings.Split(m.rendered, "\n")
		sel := m.hunkStarts[m.hunkCursor]
		if sel >= 0 && sel < len(lines) {
			lines[sel] = lcSelectedStyle.Render(ansi.Strip(lines[sel]))
		}
		content = strings.Join(lines, "\n")
	}
	// Soft-wrap long lines so nothing is truncated at the viewport edge, and cache
	// the source→display map scrollToHunk translates through.
	wrapped, srcToDisp := wrapDiffLines(content, m.diff.Width)
	m.srcToDisp = srcToDisp
	m.diff.SetContent(wrapped)
}

// scrollToHunk slides the viewport so the selected hunk's header sits at the
// top. The viewport clamps the offset, so the last hunk just scrolls as far
// as it can.
func (m *localChangesModel) scrollToHunk() {
	if len(m.hunkStarts) == 0 {
		return
	}
	m.diff.SetYOffset(dispRowForSrc(m.srcToDisp, m.hunkStarts[m.hunkCursor]))
}

// MoveHunk shifts the hunk selection by delta (clamped) and follows it into
// view. No-op when the diff has no hunks. Driven by `[` / `]` in the diff pane.
func (m *localChangesModel) MoveHunk(delta int) {
	if len(m.hunkStarts) == 0 {
		return
	}
	m.hunkCursor += delta
	if m.hunkCursor < 0 {
		m.hunkCursor = 0
	}
	if m.hunkCursor >= len(m.hunkStarts) {
		m.hunkCursor = len(m.hunkStarts) - 1
	}
	m.refreshDiffViewport()
	m.scrollToHunk()
}

// CurrentHunk returns the selected hunk index, or false when the diff has no
// hunks (rename-only, "(no file)").
func (m localChangesModel) CurrentHunk() (int, bool) {
	if len(m.hunkStarts) == 0 || m.hunkCursor < 0 || m.hunkCursor >= len(m.hunkStarts) {
		return 0, false
	}
	return m.hunkCursor, true
}

// HasChanges reports whether the tree holds any entry — the gate the
// whole-tree stash / discard actions use to skip a no-op git call (and say
// "nothing to stash" instead) when the working tree is clean.
func (m localChangesModel) HasChanges() bool { return len(m.entries) > 0 }

// StagedCount returns how many entries sit in the Staged section — the gate
// for `c` (commit), which refuses with a status line rather than letting git
// fail on an empty index, and the count shown in the commit modal header.
func (m localChangesModel) StagedCount() int {
	n := 0
	for _, e := range m.entries {
		if e.Section == sectionStaged {
			n++
		}
	}
	return n
}

// ConflictCount returns how many entries sit in the Conflicts section — the
// gate for `C` (continue), which refuses while any conflict is unresolved. A
// resolved-but-unstaged file still reads as a conflict in the index (its
// unmerged stage entries persist until `git add`), so a zero count means every
// conflict has been resolved *and* staged — exactly what `--continue` needs.
func (m localChangesModel) ConflictCount() int {
	n := 0
	for _, e := range m.entries {
		if e.Section == sectionConflicts {
			n++
		}
	}
	return n
}

// SetSequencer records the in-progress operation detected with the latest
// status snapshot.
func (m *localChangesModel) SetSequencer(k git.SequencerKind) { m.sequencer = k }

// Sequencer returns the in-progress operation (SequencerNone when the tree is
// in a normal state) so the key handlers can gate continue / abort on it.
func (m localChangesModel) Sequencer() git.SequencerKind { return m.sequencer }

// CurrentEntry returns the entry the tree cursor is on, or false when the
// tree is empty.
func (m localChangesModel) CurrentEntry() (localChangesEntry, bool) {
	if m.cursor < 0 || m.cursor >= len(m.entries) {
		return localChangesEntry{}, false
	}
	return m.entries[m.cursor], true
}

// ScrollDiff forwards a scroll key to the diff viewport. Mirrors diff.go's
// ScrollPatch — the parent batches the returned cmd.
func (m *localChangesModel) ScrollDiff(msg tea.KeyMsg) tea.Cmd {
	var cmd tea.Cmd
	m.diff, cmd = m.diff.Update(msg)
	return cmd
}

// MoveCursor shifts the selection by `delta` (negative=up, positive=down)
// and follows it into view. Returns the new entry (if any) so the caller can
// dispatch a diff load for it.
func (m *localChangesModel) MoveCursor(delta int) (localChangesEntry, bool) {
	if len(m.entries) == 0 {
		return localChangesEntry{}, false
	}
	m.cursor += delta
	m.clampCursor()
	m.followCursor()
	return m.entries[m.cursor], true
}

// JumpCursor sets the cursor to top (g) or bottom (G) and follows it.
func (m *localChangesModel) JumpCursor(toBottom bool) (localChangesEntry, bool) {
	if len(m.entries) == 0 {
		return localChangesEntry{}, false
	}
	if toBottom {
		m.cursor = len(m.entries) - 1
	} else {
		m.cursor = 0
	}
	m.followCursor()
	return m.entries[m.cursor], true
}

// SelectByPath places the cursor on the first entry matching path (and side
// preference when both rows exist). Returns false when no match is found —
// the caller falls back to MoveCursor(0) or similar to keep cursor valid.
func (m *localChangesModel) SelectByPath(path string, preferStaged bool) bool {
	// Prefer the requested side. This matters when a file sits in BOTH
	// sections at once — per-hunk staging leaves the rest of the file on the
	// other side — so staying on the side you acted from lets you keep
	// working through hunks instead of the cursor jumping across.
	want := sectionUnstaged
	if preferStaged {
		want = sectionStaged
	}
	for i, e := range m.entries {
		if e.Path == path && e.Section == want {
			m.cursor = i
			m.followCursor()
			return true
		}
	}
	for i, e := range m.entries {
		if e.Path == path {
			m.cursor = i
			m.followCursor()
			return true
		}
	}
	return false
}

// Focused / SetFocus expose the tree-vs-diff sub-focus to the parent so the
// outer key dispatcher can route ↑/↓ to cursor-move (tree) or viewport-scroll
// (diff).
func (m localChangesModel) Focused() localChangesPane { return m.focused }
func (m *localChangesModel) SetFocus(p localChangesPane) {
	m.focused = p
	// The hunk highlight is baked into the viewport content, so a focus flip
	// has to repaint to add it (→ diff) or drop it (→ tree).
	m.refreshDiffViewport()
	if p == paneLCDiff && len(m.hunkStarts) > 0 {
		m.scrollToHunk()
	}
}

// bannerRows is the number of rows the sequencer banner claims at the top of
// the tree pane (1 while an op is in progress, 0 otherwise). TreeView reserves
// them and followCursor subtracts them from the scroll budget so the banner
// never overlaps the file list.
func (m localChangesModel) bannerRows() int {
	if m.sequencer == git.SequencerNone {
		return 0
	}
	return 1
}

// visibleTreeRows is the row budget left for the file list once the banner has
// taken its share of treeH.
func (m localChangesModel) visibleTreeRows() int {
	h := m.treeH - m.bannerRows()
	if h < 0 {
		h = 0
	}
	return h
}

// sequencerBanner is the one-row in-progress notice at the top of the tree
// pane. It names the operation and the resume / unwind keys; while conflicts
// remain it says to resolve them first (continue is gated until then). Empty
// when no op is in progress. Truncated to width so it always stays one row.
func (m localChangesModel) sequencerBanner(width int) string {
	if m.sequencer == git.SequencerNone {
		return ""
	}
	op := m.sequencer.String()
	var msg string
	if m.ConflictCount() > 0 {
		msg = "! " + op + " in progress — resolve conflicts, then [C]ontinue · [^X] abort"
	} else {
		msg = "! " + op + " in progress — [C]ontinue · [^X] abort"
	}
	return lcConflictStyle.Render(runewidth.Truncate(msg, width, "…"))
}

func (m *localChangesModel) clampCursor() {
	if len(m.entries) == 0 {
		m.cursor = 0
		return
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.entries) {
		m.cursor = len(m.entries) - 1
	}
}

// followCursor slides yOffset so the row at cursor stays inside the visible
// tree height. flatRows() returns rows including section headers, so the
// math has to map cursor (entry index) → flat row index first.
func (m *localChangesModel) followCursor() {
	rows := m.flatRows()
	cursorRow := m.cursorFlatRow(rows)
	if cursorRow < 0 {
		m.yOffset = 0
		return
	}
	h := m.visibleTreeRows()
	if h <= 0 {
		m.yOffset = 0
		return
	}
	if cursorRow < m.yOffset {
		m.yOffset = cursorRow
	}
	if cursorRow >= m.yOffset+h {
		m.yOffset = cursorRow - h + 1
	}
	if m.yOffset < 0 {
		m.yOffset = 0
	}
	maxOffset := len(rows) - h
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.yOffset > maxOffset {
		m.yOffset = maxOffset
	}
}

// lcRowKind tags each rendered row so View can slice yOffset → on-screen rows
// and find the cursor highlight without a parallel-array search.
type lcRowKind int

const (
	lcRowHeader lcRowKind = iota
	lcRowEntry
	lcRowEmpty
)

type lcRow struct {
	kind     lcRowKind
	section  localChangesSection
	entryIdx int // valid only for lcRowEntry
}

// flatRows is the canonical render order. A section with zero entries renders
// neither a header nor an empty placeholder — its disappearance is the empty
// state (rule from interview decision 5: "Conflicts 섹션은 없으면 숨김").
// Unstaged + Staged headers always render (they're the "normal" tree state),
// emitting an `(empty)` placeholder when the section has no entries so the
// tree isn't a blank box.
func (m localChangesModel) flatRows() []lcRow {
	var rows []lcRow
	bySection := map[localChangesSection][]int{}
	for i, e := range m.entries {
		bySection[e.Section] = append(bySection[e.Section], i)
	}
	if conflicts := bySection[sectionConflicts]; len(conflicts) > 0 {
		rows = append(rows, lcRow{kind: lcRowHeader, section: sectionConflicts})
		for _, idx := range conflicts {
			rows = append(rows, lcRow{kind: lcRowEntry, section: sectionConflicts, entryIdx: idx})
		}
	}
	for _, sec := range []localChangesSection{sectionUnstaged, sectionStaged} {
		rows = append(rows, lcRow{kind: lcRowHeader, section: sec})
		ids := bySection[sec]
		if len(ids) == 0 {
			rows = append(rows, lcRow{kind: lcRowEmpty, section: sec})
			continue
		}
		for _, idx := range ids {
			rows = append(rows, lcRow{kind: lcRowEntry, section: sec, entryIdx: idx})
		}
	}
	return rows
}

func (m localChangesModel) cursorFlatRow(rows []lcRow) int {
	if len(m.entries) == 0 {
		return -1
	}
	for i, r := range rows {
		if r.kind == lcRowEntry && r.entryIdx == m.cursor {
			return i
		}
	}
	return -1
}

var (
	lcHeaderStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color(colorTime)).Bold(true)
	lcSelectedStyle  = lipgloss.NewStyle().Reverse(true)
	lcUntrackedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colorTime))
	lcConflictStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color(colorTime)).Bold(true)
	lcStatAddStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color(colorStatAdd))
	lcStatDelStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color(colorStatDel))
)

// colorStatAdd / colorStatDel tint the tree's "+N -M" column — green for
// insertions, red for deletions, matching diff convention.
const (
	colorStatAdd = "2"
	colorStatDel = "1"
)

// TreeView renders the file tree column to a string. Empty / loading / error
// states are surfaced as a single line instead of an empty box.
func (m localChangesModel) TreeView() string {
	if !m.loaded {
		return loadingPane(m.treeW, m.treeH, m.spinnerFrame)
	}
	if m.loadErr != nil {
		return "error: " + firstLine(m.loadErr.Error())
	}
	width := m.treeW
	if width <= 0 {
		width = 1
	}
	banner := m.sequencerBanner(width)
	if len(m.entries) == 0 {
		// A still-active sequencer with every conflict resolved + staged leaves
		// a clean tree but must keep the banner so `C` stays reachable.
		if banner != "" {
			return banner + "\n(no changes)"
		}
		return "(no changes)"
	}
	rows := m.flatRows()
	height := m.visibleTreeRows()
	if height <= 0 {
		height = len(rows)
	}
	end := m.yOffset + height
	if end > len(rows) {
		end = len(rows)
	}
	var lines []string
	if banner != "" {
		lines = append(lines, banner)
	}
	for i := m.yOffset; i < end; i++ {
		lines = append(lines, m.renderRow(rows[i], width))
	}
	return strings.Join(lines, "\n")
}

func (m localChangesModel) renderRow(r lcRow, width int) string {
	switch r.kind {
	case lcRowHeader:
		return lcHeaderStyle.Render(runewidth.Truncate(headerTitle(r.section), width, "…"))
	case lcRowEmpty:
		return lcUntrackedStyle.Render(runewidth.Truncate("  (empty)", width, "…"))
	case lcRowEntry:
		return m.renderEntryRow(m.entries[r.entryIdx], width, r.entryIdx == m.cursor)
	}
	return ""
}

func headerTitle(s localChangesSection) string {
	switch s {
	case sectionConflicts:
		return "Conflicts"
	case sectionUnstaged:
		return "Unstaged"
	case sectionStaged:
		return "Staged"
	}
	return ""
}

// renderEntryRow renders one file row: "  <marker> <path>" left-aligned with
// the "+N -M" stat (when known) right-aligned at the width edge. Layout is
// computed in plain text so the visible columns line up; color is applied to
// the already-sized segments afterward. The selected row is reverse-styled as
// one plain span — restyling a span that already carries the stat's color ANSI
// would break on the inner reset.
func (m localChangesModel) renderEntryRow(e localChangesEntry, width int, selected bool) string {
	label := e.Path
	if e.Renamed() {
		label = e.OrigPath + " → " + e.Path
	}
	left, gap, stat := layoutEntryRow("  "+entryMarker(e)+" "+label, m.statText(e), width)

	if selected {
		return lcSelectedStyle.Render(left + gap + stat)
	}
	leftOut := left
	switch {
	case e.Conflict:
		leftOut = lcConflictStyle.Render(left)
	case e.Untracked:
		leftOut = lcUntrackedStyle.Render(left)
	}
	return leftOut + gap + colorizeStat(stat)
}

// layoutEntryRow fits "<left> … <stat>" into width with the stat pinned to the
// right edge and at least one space of gap. With no stat (or no room for one)
// it truncates left to the full width and returns empty gap/stat. All widths
// are measured in plain text so callers can color the segments without
// disturbing alignment.
func layoutEntryRow(left, stat string, width int) (string, string, string) {
	if width < 1 {
		width = 1
	}
	sw := runewidth.StringWidth(stat)
	if stat == "" || sw+2 > width {
		return runewidth.Truncate(left, width, "…"), "", ""
	}
	l := runewidth.Truncate(left, width-sw-1, "…")
	gap := width - runewidth.StringWidth(l) - sw
	if gap < 1 {
		gap = 1
	}
	return l, strings.Repeat(" ", gap), stat
}

// statText returns the "+N -M" string for the entry's side, "bin" for binary
// files, or "" when no stat is known (untracked, conflict absent from the
// diff, or the numstat probe failed).
func (m localChangesModel) statText(e localChangesEntry) string {
	var (
		fs git.FileStat
		ok bool
	)
	if e.Staged() {
		fs, ok = m.stagedStat[e.Path]
	} else {
		fs, ok = m.unstagedStat[e.Path]
	}
	if !ok {
		return ""
	}
	if fs.Binary() {
		return "bin"
	}
	// Drop the zero side so a pure add/delete reads as "+4" / "-1" instead of
	// "+4 -0"; only mixed changes carry both tokens.
	switch {
	case fs.Insertions > 0 && fs.Deletions > 0:
		return fmt.Sprintf("+%d -%d", fs.Insertions, fs.Deletions)
	case fs.Insertions > 0:
		return fmt.Sprintf("+%d", fs.Insertions)
	case fs.Deletions > 0:
		return fmt.Sprintf("-%d", fs.Deletions)
	default:
		return ""
	}
}

// colorizeStat tints "+N" green and "-M" red. The stat may carry one token
// (e.g. "+4" or "-1") when the other side is zero, or both ("+4 -2"). "bin" /
// "" pass through uncolored.
func colorizeStat(stat string) string {
	if stat == "" || stat == "bin" {
		return stat
	}
	parts := strings.Fields(stat)
	for i, p := range parts {
		switch {
		case strings.HasPrefix(p, "+"):
			parts[i] = lcStatAddStyle.Render(p)
		case strings.HasPrefix(p, "-"):
			parts[i] = lcStatDelStyle.Render(p)
		}
	}
	return strings.Join(parts, " ")
}

func entryMarker(e localChangesEntry) string {
	switch {
	case e.Conflict:
		return "!"
	case e.Untracked:
		return "?"
	case e.Section == sectionStaged:
		return string(e.IndexState)
	default:
		return string(e.WorktreeState)
	}
}

func (e localChangesEntry) Renamed() bool { return e.OrigPath != "" }

// DiffView renders the full-screen diff pane: a one-row file header over the
// diff body. Empty / loading / error bodies are surfaced as a one-liner so the
// header still names what's (not) being shown.
func (m localChangesModel) DiffView() string {
	bodyH := m.diffH - 1
	if bodyH < 1 {
		bodyH = 1
	}
	var body string
	switch {
	case m.diffErr != nil:
		body = "error: " + firstLine(m.diffErr.Error())
	case m.diffLoading:
		body = loadingPane(m.diffW, bodyH, m.spinnerFrame)
	case strings.TrimSpace(m.diffText) == "":
		body = "(no file)"
	default:
		body = m.diff.View()
	}
	return m.diffHeader() + "\n" + body
}

// diffHeader is the one-row title above the diff body. It names the file and
// which side (unstaged / staged / conflict) the diff belongs to — context the
// tree column used to carry before the layout went single-pane drill-down.
func (m localChangesModel) diffHeader() string {
	w := m.diffW
	if w < 1 {
		w = 1
	}
	e, ok := m.CurrentEntry()
	if !ok {
		return lcHeaderStyle.Render(runewidth.Truncate("Diff", w, "…"))
	}
	side := "unstaged"
	switch {
	case e.Conflict:
		side = "conflict"
	case e.Staged():
		side = "staged"
	}
	label := e.Path
	if e.Renamed() {
		label = e.OrigPath + " → " + e.Path
	}
	return lcHeaderStyle.Render(fitHeaderLabel(label, "("+side+")", w))
}

// HasEntry reports whether an entry for path exists on the requested side.
// The full-screen diff pane uses it after a status reload to decide whether
// the file it was showing still has changes on that side — if a per-hunk
// stage consumed the last hunk, the side is gone and the caller drops back to
// the tree.
func (m localChangesModel) HasEntry(path string, staged bool) bool {
	want := sectionUnstaged
	if staged {
		want = sectionStaged
	}
	for _, e := range m.entries {
		if e.Path == path && e.Section == want {
			return true
		}
	}
	return false
}

// classifyStatus is the placement rule for splitting one git.StatusEntry into
// 1 or 2 localChangesEntry rows. Pulled out as a free function so tests can
// exercise it without constructing a localChangesModel.
//
// Rules:
//   - Conflict → single entry in sectionConflicts.
//   - Untracked → single entry in sectionUnstaged (with Untracked=true).
//   - Ordinary:
//     IndexState != '.'    → entry in sectionStaged
//     WorktreeState != '.' → entry in sectionUnstaged
//     A file with both will appear in both sections (one entry each).
func classifyStatus(src []git.StatusEntry) []localChangesEntry {
	var conflicts, unstaged, staged []localChangesEntry
	for _, s := range src {
		switch {
		case s.Conflict:
			conflicts = append(conflicts, localChangesEntry{
				Path:          s.Path,
				OrigPath:      s.OrigPath,
				Section:       sectionConflicts,
				IndexState:    s.IndexState,
				WorktreeState: s.WorktreeState,
				Conflict:      true,
			})
		case s.Untracked:
			unstaged = append(unstaged, localChangesEntry{
				Path:          s.Path,
				Section:       sectionUnstaged,
				IndexState:    s.IndexState,
				WorktreeState: s.WorktreeState,
				Untracked:     true,
			})
		default:
			if s.IndexState != '.' && s.IndexState != 0 {
				staged = append(staged, localChangesEntry{
					Path:          s.Path,
					OrigPath:      s.OrigPath,
					Section:       sectionStaged,
					IndexState:    s.IndexState,
					WorktreeState: s.WorktreeState,
				})
			}
			if s.WorktreeState != '.' && s.WorktreeState != 0 {
				unstaged = append(unstaged, localChangesEntry{
					Path:          s.Path,
					Section:       sectionUnstaged,
					IndexState:    s.IndexState,
					WorktreeState: s.WorktreeState,
				})
			}
		}
	}
	// Emit in render order (Conflicts → Unstaged → Staged) so the entries
	// slice index lines up with flatRows' visual order: cursor 0 is the first
	// visible row, and ↑/↓ step the way the eye expects. The git status stream
	// interleaves sections arbitrarily, so without this the cursor would land
	// off the top row and ↑/↓ would jump across sections.
	out := make([]localChangesEntry, 0, len(conflicts)+len(unstaged)+len(staged))
	out = append(out, conflicts...)
	out = append(out, unstaged...)
	out = append(out, staged...)
	return out
}
