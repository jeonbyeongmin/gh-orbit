package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

const diffPatchTimeout = 60 * time.Second

// fileBoundary pairs a 0-indexed line within patchText with the destination
// path parsed from that `diff --git` header — the index lets `{` / `}`
// jump the viewport whole-file without re-scanning, the path feeds the
// in-box header so the reviewer always knows which file they are reading.
type fileBoundary struct {
	line int
	path string
}

// diffModel hosts the in-page commit / PR diff opened with `→`.
type diffModel struct {
	viewport     viewport.Model
	currentHash  string
	patchText    string // plain unified diff from `git show`
	rendered     string // patchText styled for the viewport (syntax + word-level), cached per load
	loadingPatch bool
	err          error
	reqID        uint64
	// patchViewportInit guards the one-shot SetSize on first patch load —
	// before that, viewport has zero dims and SetContent's truncation gives
	// nothing back.
	patchViewportInit bool
	// files is the cached list of `diff --git` headers in patchText, in
	// the order they appear. Empty for merge commits with no diff and for
	// "no changes" rendering. Populated by ApplyPatchLoaded.
	files []fileBoundary
	// activeFile is the index into files[] that the reviewer is currently
	// reading. Tracked independently of viewport.YOffset because the
	// viewport clamps SetYOffset to MaxYOffset — on patches that fit the
	// viewport entirely, YOffset can't advance past 0, but the reviewer
	// still expects `]` / `[` to move the "current file" indicator. -1
	// means no file is active (empty / failed / not-yet-loaded patch).
	activeFile int
	// hunkStarts holds the line index of every `@@` hunk header in patchText,
	// across all files; hunkCursor indexes into it. `[`/`]` move the cursor
	// (MoveHunk), `{`/`}` jump whole files. Mirrors localChangesModel's hunk
	// model so the graph diff navigates like the local-changes diff.
	hunkStarts []int
	hunkCursor int
	// spinnerFrame is pushed in by Model on every spinnerTickMsg so the
	// loading placeholder animates. Only read while loadingPatch.
	spinnerFrame int
}

func (d *diffModel) resetActiveFile() {
	if len(d.files) > 0 {
		d.activeFile = 0
	} else {
		d.activeFile = -1
	}
}

func newDiffModel() diffModel {
	return diffModel{
		viewport:   viewport.New(0, 0),
		activeFile: -1,
	}
}

func (d *diffModel) SetPatchViewportSize(w, h int) {
	// Floor the height at 1 so a tiny terminal — or the `?` help panel open
	// over the diff, which can shrink the box to one row — never collapses the
	// body to zero visible lines (mirrors localChangesModel.SetSize's clamp).
	if h < 1 {
		h = 1
	}
	widthChanged := w != d.viewport.Width
	d.viewport.Width = w
	d.viewport.Height = h
	d.patchViewportInit = true
	if d.patchText != "" {
		if widthChanged {
			d.rendered = renderDiffContent(d.patchText, w)
		}
		d.refreshPatchViewport()
	}
}

func (d *diffModel) BeginPatchLoad(hash string, reqID uint64) {
	d.currentHash = hash
	d.patchText = ""
	d.rendered = ""
	d.loadingPatch = true
	d.err = nil
	d.reqID = reqID
	d.files = nil
	d.activeFile = -1
	d.hunkStarts = nil
	d.hunkCursor = 0
	d.viewport.SetContent("")
	d.viewport.GotoTop()
}

// ClosePatch releases the patch text and clears the viewport so a 10MB
// vendor diff doesn't sit in memory after the user closes the overlay.
func (d *diffModel) ClosePatch() {
	d.patchText = ""
	d.rendered = ""
	d.files = nil
	d.activeFile = -1
	d.hunkStarts = nil
	d.hunkCursor = 0
	d.viewport.SetContent("")
}

// RerenderTheme repaints the loaded patch under the now-current activeDiffTheme
// (the Settings picker just cycled it), so the color change shows live behind
// the dialog. A no-op when no patch is loaded.
func (d *diffModel) RerenderTheme() {
	if d.patchText == "" {
		return
	}
	d.rendered = renderDiffContent(d.patchText, d.viewport.Width)
	d.refreshPatchViewport()
}

// accepts gates Apply* against stale dispatches. reqID rejects responses from
// a previous overlay invocation; the hash check catches the rare same-reqID
// mismatch (re-dispatch on the same id with a new hash).
func (d *diffModel) accepts(reqID uint64, hash string) bool {
	return reqID == d.reqID && hash == d.currentHash
}

func (d *diffModel) ApplyPatchLoaded(reqID uint64, hash, text string) {
	if !d.accepts(reqID, hash) {
		return
	}
	d.loadingPatch = false
	d.patchText = text
	d.rendered = renderDiffContent(text, d.viewport.Width)
	d.files = parseFileBoundaries(text)
	d.hunkStarts = parseHunkStarts(text)
	d.hunkCursor = 0
	d.resetActiveFile()
	d.err = nil
	if d.patchViewportInit {
		d.refreshPatchViewport()
		d.viewport.GotoTop()
	}
}

func (d *diffModel) ApplyPatchFailed(reqID uint64, hash string, err error) {
	if !d.accepts(reqID, hash) {
		return
	}
	d.loadingPatch = false
	d.err = err
}

// ScrollPatch forwards a scroll key to the patch viewport and returns any cmd
// the viewport produced (mouse-wheel handling, etc.) so the caller can batch
// it. After the viewport advances, activeFile re-syncs from the new YOffset
// so the hint indicator stays consistent with what the reviewer is reading —
// scrolling past a `diff --git` header with `↓` updates the indicator the
// same as if they had pressed `]`.
func (d *diffModel) ScrollPatch(msg tea.KeyMsg) tea.Cmd {
	var cmd tea.Cmd
	d.viewport, cmd = d.viewport.Update(msg)
	d.syncActiveFileFromYOffset()
	d.syncHunkFromYOffset()
	// Repaint so the accented `@@` line tracks the hunk the scroll landed on —
	// without this the header's [hunk K/N] advances but the highlight stays on
	// the previous hunk.
	d.refreshPatchViewport()
	return cmd
}

// MoveHunk advances the hunk cursor by delta (clamped, no wrap) and scrolls
// the viewport to that `@@` header. The `[`/`]` counterpart to `{`/`}`'s
// whole-file jump — mirrors localChangesModel.MoveHunk.
func (d *diffModel) MoveHunk(delta int) {
	if len(d.hunkStarts) == 0 {
		return
	}
	d.hunkCursor += delta
	if d.hunkCursor < 0 {
		d.hunkCursor = 0
	}
	if d.hunkCursor >= len(d.hunkStarts) {
		d.hunkCursor = len(d.hunkStarts) - 1
	}
	// Pair activeFile to the hunk itself, not the post-clamp YOffset — on a
	// patch shorter than the viewport SetYOffset clamps to 0 and would drag the
	// header's file back to file 0 while the hunk index advanced.
	if fi := d.fileIndexForHunk(d.hunkCursor); fi >= 0 {
		d.activeFile = fi
	}
	d.refreshPatchViewport()
	d.viewport.SetYOffset(d.hunkStarts[d.hunkCursor])
}

// syncHunkFromYOffset parks hunkCursor on the largest hunk header at or below
// the current YOffset, so manual scroll / file jumps keep the `[hunk K/N]`
// header in sync. Empty hunk list leaves the cursor at 0.
func (d *diffModel) syncHunkFromYOffset() {
	if len(d.hunkStarts) == 0 {
		d.hunkCursor = 0
		return
	}
	cur := d.viewport.YOffset
	idx := 0
	for i, s := range d.hunkStarts {
		if s <= cur {
			idx = i
		} else {
			break
		}
	}
	d.hunkCursor = idx
}

// CurrentHunk returns the 1-based hunk index and total hunk count, or (0, 0)
// when the patch has no hunks (empty / failed / merge commit).
func (d diffModel) CurrentHunk() (index, total int) {
	if len(d.hunkStarts) == 0 {
		return 0, 0
	}
	return d.hunkCursor + 1, len(d.hunkStarts)
}

// fileIndexForHunk returns the index into files of the file owning the hunk at
// hunkStarts[hi] (the largest `diff --git` boundary at or before it), or -1
// when there are no files. Deriving activeFile from the hunk rather than the
// viewport's YOffset keeps the header's file/hunk pair consistent even when the
// patch is shorter than the viewport and SetYOffset clamps to 0.
func (d diffModel) fileIndexForHunk(hi int) int {
	if hi < 0 || hi >= len(d.hunkStarts) || len(d.files) == 0 {
		return -1
	}
	line := d.hunkStarts[hi]
	idx := 0
	for i, f := range d.files {
		if f.line <= line {
			idx = i
		} else {
			break
		}
	}
	return idx
}

// firstHunkForFile returns the index into hunkStarts of the first hunk at or
// after files[fi]'s header — the hunk a `{`/`}` file jump should land on — or
// the current cursor when that file carries no hunk.
func (d diffModel) firstHunkForFile(fi int) int {
	if fi < 0 || fi >= len(d.files) {
		return d.hunkCursor
	}
	line := d.files[fi].line
	for i, s := range d.hunkStarts {
		if s >= line {
			return i
		}
	}
	return d.hunkCursor
}

// refreshPatchViewport repaints the viewport, accenting the selected hunk's
// `@@` header so `[`/`]` navigation is visible. Mirrors
// localChangesModel.refreshDiffViewport.
func (d *diffModel) refreshPatchViewport() {
	if d.patchText == "" {
		d.viewport.SetContent("")
		return
	}
	if d.rendered == "" {
		d.rendered = renderDiffContent(d.patchText, d.viewport.Width)
	}
	if len(d.hunkStarts) == 0 {
		d.viewport.SetContent(d.rendered)
		return
	}
	lines := strings.Split(d.rendered, "\n")
	sel := d.hunkStarts[d.hunkCursor]
	if sel >= 0 && sel < len(lines) {
		lines[sel] = lcSelectedStyle.Render(ansi.Strip(lines[sel]))
	}
	d.viewport.SetContent(strings.Join(lines, "\n"))
}

// JumpToNextFile advances activeFile by one and slides the viewport down
// to the new file's `diff --git` header. No wrap — at the last file the
// call is a no-op (a surprise jump back to the top reads as a bug, not a
// feature). The viewport's SetYOffset clamps to MaxYOffset on patches
// shorter than the viewport, so the visible scroll may not move; the
// activeFile bump is still meaningful because the hint indicator picks
// it up. -1 / single-file / empty are all silent no-ops.
func (d *diffModel) JumpToNextFile() {
	if d.activeFile < 0 || d.activeFile >= len(d.files)-1 {
		return
	}
	d.activeFile++
	// Land the hunk cursor on the file's first hunk directly — syncing from
	// the (possibly clamped) YOffset would leave it on the prior file's hunk.
	d.hunkCursor = d.firstHunkForFile(d.activeFile)
	d.refreshPatchViewport()
	d.viewport.SetYOffset(d.files[d.activeFile].line)
}

// JumpToPrevFile retreats activeFile by one and slides the viewport up
// to the new file's header. No wrap — at the first file the call is a
// no-op. Same clamp story as JumpToNextFile: on short patches the
// visible scroll won't move, but the indicator still updates.
func (d *diffModel) JumpToPrevFile() {
	if d.activeFile <= 0 {
		return
	}
	d.activeFile--
	d.hunkCursor = d.firstHunkForFile(d.activeFile)
	d.refreshPatchViewport()
	d.viewport.SetYOffset(d.files[d.activeFile].line)
}

// syncActiveFileFromYOffset finds the largest file boundary at or below
// the current YOffset and parks activeFile there. Called after every
// scroll so manual ↑/↓ navigation keeps the indicator in sync. When the
// viewport is parked above the first boundary (transient — viewport
// always starts at the first header on load), the active stays at 0.
func (d *diffModel) syncActiveFileFromYOffset() {
	if len(d.files) == 0 {
		d.activeFile = -1
		return
	}
	cur := d.viewport.YOffset
	idx := 0
	for i, f := range d.files {
		if f.line <= cur {
			idx = i
		} else {
			break
		}
	}
	d.activeFile = idx
}

// CurrentFile returns the path of the active file, its 1-based index,
// and the total file count. Returns ("", 0, 0) when no file is active
// (empty / failed / not-yet-loaded patch). The indicator tracks
// activeFile rather than viewport.YOffset so it stays meaningful on
// patches that fit the viewport entirely — see the diffModel.activeFile
// comment for why those decouple.
func (d diffModel) CurrentFile() (path string, index, total int) {
	if d.activeFile < 0 || d.activeFile >= len(d.files) {
		return "", 0, 0
	}
	return d.files[d.activeFile].path, d.activeFile + 1, len(d.files)
}

// PatchView renders the in-box diff: a one-row file header (path + hunk
// position) over the patch body. Mirrors localChangesModel.DiffView so the
// graph diff and the local-changes diff read identically. The header is always
// present so error / loading / empty bodies still name what's (not) shown.
func (d diffModel) PatchView() string {
	bodyH := d.viewport.Height
	if bodyH < 1 {
		bodyH = 1
	}
	var body string
	switch {
	case d.err != nil:
		body = "error: " + firstLine(d.err.Error())
	case d.loadingPatch:
		body = loadingPane(d.viewport.Width, bodyH, d.spinnerFrame)
	case strings.TrimSpace(d.patchText) == "":
		body = "(no changes)"
	default:
		body = d.viewport.View()
	}
	return d.patchHeader() + "\n" + body
}

// patchHeader is the one-row title above the diff body: the current file and,
// when the patch has hunks, the cursor's hunk position. The local-changes
// header carries the staged/unstaged side here instead — a commit patch has no
// side, so this shows hunk progress.
func (d diffModel) patchHeader() string {
	w := d.viewport.Width
	if w < 1 {
		w = 1
	}
	path, _, _ := d.CurrentFile()
	if path == "" {
		return lcHeaderStyle.Render(runewidth.Truncate("Diff", w, "…"))
	}
	label := path
	if k, n := d.CurrentHunk(); n > 0 {
		label = fmt.Sprintf("%s  [hunk %d/%d]", path, k, n)
	}
	return lcHeaderStyle.Render(runewidth.Truncate(label, w, "…"))
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// parseFileBoundaries walks patchText once and records every `diff --git`
// header line. The destination side (b/<path>) is the canonical "current"
// path for additions, modifications, and renames — for deletions git still
// emits the same b/ entry, so we just always use it. Quoted paths
// (`"a/with space"`) have their enclosing quotes stripped; embedded
// backslash escapes are left as-is (the reviewer reads the same string
// git would print).
//
// The patch text is uncolored (the viewport styles it in-process), so the
// ansi.Strip below is just defensive. The `fileBoundary.line` index counts
// lines (newlines in patchText), which is exactly what viewport.SetYOffset
// wants — the in-process renderer emits one styled line per source line, so
// the indices stay valid against the rendered content too.
func parseFileBoundaries(text string) []fileBoundary {
	if text == "" {
		return nil
	}
	var out []fileBoundary
	for i, line := range strings.Split(text, "\n") {
		path, ok := parseFileDiffHeader(line)
		if !ok {
			continue
		}
		out = append(out, fileBoundary{line: i, path: path})
	}
	return out
}

// parseFileDiffHeader pulls the b-side path out of a single `diff --git`
// line. Returns ("", false) for any other line so the caller can use it
// as a predicate. The b-side is found via the literal " b/" marker that
// separates the two paths — paths containing that exact sequence as a
// substring (e.g. `dir b/file`) would mis-split, but git never emits
// such a path without quoting it, and the quoted branch handles that.
//
// The line is ANSI-stripped before matching (defensive — the patch is now
// uncolored); the path returned is plain text suitable for the bottom-hint
// indicator.
func parseFileDiffHeader(line string) (string, bool) {
	plain := ansi.Strip(line)
	const prefix = "diff --git "
	if !strings.HasPrefix(plain, prefix) {
		return "", false
	}
	rest := plain[len(prefix):]
	// Quoted form: `"a/<path>" "b/<path>"`. Find the last `"b/` opener
	// and trim the trailing closing quote.
	if strings.HasPrefix(rest, "\"a/") {
		marker := "\" \"b/"
		if idx := strings.Index(rest, marker); idx >= 0 {
			path := rest[idx+len(marker):]
			path = strings.TrimSuffix(path, "\"")
			return path, true
		}
		return "", false
	}
	if idx := strings.Index(rest, " b/"); idx >= 0 {
		return rest[idx+len(" b/"):], true
	}
	return "", false
}

type diffPatchLoadedMsg struct {
	reqID uint64
	hash  string
	text  string
}

type diffPatchFailedMsg struct {
	reqID uint64
	hash  string
	err   error
}

func loadDiffPatchCmd(dir, hash string, reqID uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), diffPatchTimeout)
		defer cancel()
		text, err := git.Patch(ctx, dir, hash)
		if err != nil {
			return diffPatchFailedMsg{reqID: reqID, hash: hash, err: err}
		}
		return diffPatchLoadedMsg{reqID: reqID, hash: hash, text: text}
	}
}
