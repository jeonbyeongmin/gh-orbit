package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
	diffText    string
	diffLoading bool
	diffErr     error

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
}

func newLocalChangesModel() localChangesModel {
	return localChangesModel{diff: viewport.New(0, 0)}
}

// SetSize takes the tree column and diff column dimensions separately because
// the outer layout already splits the right column horizontally — there is no
// single "right column width" to subdivide internally.
func (m *localChangesModel) SetSize(treeW, treeH, diffW, diffH int) {
	m.treeW = treeW
	m.treeH = treeH
	m.diffW = diffW
	m.diffH = diffH
	m.diff.Width = diffW
	m.diff.Height = diffH
	if m.diffText != "" {
		m.diff.SetContent(m.diffText)
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
	m.diffLoading = true
	m.diffErr = nil
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
	m.diffErr = nil
	m.diff.SetContent(text)
	m.diff.GotoTop()
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
	m.diff.SetContent("")
}

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
	for i, e := range m.entries {
		if e.Path != path {
			continue
		}
		if preferStaged && e.Section != sectionStaged {
			continue
		}
		m.cursor = i
		m.followCursor()
		return true
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
// outer key dispatcher can route j/k to cursor-move (tree) or viewport-scroll
// (diff).
func (m localChangesModel) Focused() localChangesPane { return m.focused }
func (m *localChangesModel) SetFocus(p localChangesPane) {
	m.focused = p
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
	if m.treeH <= 0 {
		m.yOffset = 0
		return
	}
	if cursorRow < m.yOffset {
		m.yOffset = cursorRow
	}
	if cursorRow >= m.yOffset+m.treeH {
		m.yOffset = cursorRow - m.treeH + 1
	}
	if m.yOffset < 0 {
		m.yOffset = 0
	}
	maxOffset := len(rows) - m.treeH
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
)

// TreeView renders the file tree column to a string. Empty / loading / error
// states are surfaced as a single line instead of an empty box.
func (m localChangesModel) TreeView() string {
	if !m.loaded {
		return centerPlaceholder(m.treeW, m.treeH, loadingPlaceholder(m.spinnerFrame))
	}
	if m.loadErr != nil {
		return "error: " + firstLine(m.loadErr.Error())
	}
	if len(m.entries) == 0 {
		return "(no changes)"
	}
	rows := m.flatRows()
	width := m.treeW
	if width <= 0 {
		width = 1
	}
	height := m.treeH
	if height <= 0 {
		height = len(rows)
	}
	end := m.yOffset + height
	if end > len(rows) {
		end = len(rows)
	}
	var lines []string
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
		e := m.entries[r.entryIdx]
		text := renderEntryLine(e, width)
		if r.entryIdx == m.cursor {
			return lcSelectedStyle.Render(text)
		}
		return text
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

func renderEntryLine(e localChangesEntry, width int) string {
	marker := entryMarker(e)
	label := e.Path
	if e.Renamed() {
		label = e.OrigPath + " → " + e.Path
	}
	body := "  " + marker + " " + label
	body = runewidth.Truncate(body, width, "…")
	if e.Conflict {
		return lcConflictStyle.Render(body)
	}
	if e.Untracked {
		return lcUntrackedStyle.Render(body)
	}
	return body
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

// DiffView renders the diff column. Empty / loading / error are surfaced as
// a one-liner; otherwise the viewport's rendered text is returned.
func (m localChangesModel) DiffView() string {
	if m.diffErr != nil {
		return "error: " + firstLine(m.diffErr.Error())
	}
	if m.diffLoading {
		return centerPlaceholder(m.diffW, m.diffH, loadingPlaceholder(m.spinnerFrame))
	}
	if strings.TrimSpace(m.diffText) == "" {
		return "(no file)"
	}
	return m.diff.View()
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
	var out []localChangesEntry
	for _, s := range src {
		switch {
		case s.Conflict:
			out = append(out, localChangesEntry{
				Path:          s.Path,
				OrigPath:      s.OrigPath,
				Section:       sectionConflicts,
				IndexState:    s.IndexState,
				WorktreeState: s.WorktreeState,
				Conflict:      true,
			})
		case s.Untracked:
			out = append(out, localChangesEntry{
				Path:          s.Path,
				Section:       sectionUnstaged,
				IndexState:    s.IndexState,
				WorktreeState: s.WorktreeState,
				Untracked:     true,
			})
		default:
			if s.IndexState != '.' && s.IndexState != 0 {
				out = append(out, localChangesEntry{
					Path:          s.Path,
					OrigPath:      s.OrigPath,
					Section:       sectionStaged,
					IndexState:    s.IndexState,
					WorktreeState: s.WorktreeState,
				})
			}
			if s.WorktreeState != '.' && s.WorktreeState != 0 {
				out = append(out, localChangesEntry{
					Path:          s.Path,
					Section:       sectionUnstaged,
					IndexState:    s.IndexState,
					WorktreeState: s.WorktreeState,
				})
			}
		}
	}
	return out
}
