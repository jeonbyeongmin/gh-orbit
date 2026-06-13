package tui

import (
	"context"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

const diffPatchTimeout = 60 * time.Second

// fileBoundary pairs a 0-indexed line within patchText with the destination
// path parsed from that `diff --git` header — the index lets `[ ` / `]`
// jump the viewport without re-scanning, the path feeds the bottom-hint
// "<path> [N/M]" indicator so the reviewer always knows where they are.
type fileBoundary struct {
	line int
	path string
}

// diffModel hosts the full-screen patch overlay opened with `d`.
type diffModel struct {
	viewport     viewport.Model
	currentHash  string
	patchText    string
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
	d.viewport.Width = w
	d.viewport.Height = h
	d.patchViewportInit = true
	if d.patchText != "" {
		d.viewport.SetContent(d.patchText)
	}
}

func (d *diffModel) BeginPatchLoad(hash string, reqID uint64) {
	d.currentHash = hash
	d.patchText = ""
	d.loadingPatch = true
	d.err = nil
	d.reqID = reqID
	d.files = nil
	d.activeFile = -1
	d.viewport.SetContent("")
	d.viewport.GotoTop()
}

// ClosePatch releases the patch text and clears the viewport so a 10MB
// vendor diff doesn't sit in memory after the user closes the overlay.
func (d *diffModel) ClosePatch() {
	d.patchText = ""
	d.files = nil
	d.activeFile = -1
	d.viewport.SetContent("")
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
	d.files = parseFileBoundaries(text)
	d.resetActiveFile()
	d.err = nil
	if d.patchViewportInit {
		d.viewport.SetContent(text)
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
// scrolling past a `diff --git` header with `j` updates the indicator the
// same as if they had pressed `]`.
func (d *diffModel) ScrollPatch(msg tea.KeyMsg) tea.Cmd {
	var cmd tea.Cmd
	d.viewport, cmd = d.viewport.Update(msg)
	d.syncActiveFileFromYOffset()
	return cmd
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
	d.viewport.SetYOffset(d.files[d.activeFile].line)
}

// syncActiveFileFromYOffset finds the largest file boundary at or below
// the current YOffset and parks activeFile there. Called after every
// scroll so manual j/k navigation keeps the indicator in sync. When the
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

func (d diffModel) PatchView() string {
	if d.err != nil {
		return "error: " + firstLine(d.err.Error())
	}
	if d.loadingPatch {
		return loadingPane(d.viewport.Width, d.viewport.Height, d.spinnerFrame)
	}
	if strings.TrimSpace(d.patchText) == "" {
		return "(no changes)"
	}
	return d.viewport.View()
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
// gh-orbit invokes git with `color.ui=always` so the overlay can render
// the green/red diff palette, which means every line — header included —
// carries ANSI escape sequences. The parser strips them per-line before
// matching so the prefix / "b/" lookups see the clean text. The
// `fileBoundary.line` index counts rendered lines (newlines in the raw
// colored text), which is exactly what viewport.SetYOffset wants.
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
// ANSI escapes on the line (from `color.ui=always`) are stripped before
// matching; the path returned is plain text suitable for the bottom-hint
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
