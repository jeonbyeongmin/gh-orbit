package tui

import (
	"context"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

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
}

func newDiffModel() diffModel {
	return diffModel{
		viewport: viewport.New(0, 0),
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
	d.viewport.SetContent("")
	d.viewport.GotoTop()
}

// ClosePatch releases the patch text and clears the viewport so a 10MB
// vendor diff doesn't sit in memory after the user closes the overlay.
func (d *diffModel) ClosePatch() {
	d.patchText = ""
	d.files = nil
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
// the viewport produced (mouse-wheel handling, etc.) so the caller can batch it.
func (d *diffModel) ScrollPatch(msg tea.KeyMsg) tea.Cmd {
	var cmd tea.Cmd
	d.viewport, cmd = d.viewport.Update(msg)
	return cmd
}

// JumpToNextFile sets the viewport offset to the next `diff --git` header
// strictly below the current cursor. No-op if already on/past the last
// header (no wrap — the user pressing `]` at the last file expects no
// motion, not a surprise jump back to the top).
func (d *diffModel) JumpToNextFile() {
	cur := d.viewport.YOffset
	for _, f := range d.files {
		if f.line > cur {
			d.viewport.SetYOffset(f.line)
			return
		}
	}
}

// JumpToPrevFile sets the viewport offset to the previous `diff --git`
// header strictly above the current cursor. No-op when no boundary
// precedes the cursor.
func (d *diffModel) JumpToPrevFile() {
	cur := d.viewport.YOffset
	for i := len(d.files) - 1; i >= 0; i-- {
		if d.files[i].line < cur {
			d.viewport.SetYOffset(d.files[i].line)
			return
		}
	}
}

// CurrentFile returns the path of the file the viewport is currently
// inside (largest header line <= YOffset), the 1-based index of that
// file, and the total file count. Returns ("", 0, 0) when there are no
// files (merge commits with empty diff) or the cursor sits above the
// first header (transient — viewport always starts at the first header).
func (d diffModel) CurrentFile() (path string, index, total int) {
	if len(d.files) == 0 {
		return "", 0, 0
	}
	cur := d.viewport.YOffset
	idx := -1
	for i, f := range d.files {
		if f.line <= cur {
			idx = i
		} else {
			break
		}
	}
	if idx < 0 {
		return "", 0, len(d.files)
	}
	return d.files[idx].path, idx + 1, len(d.files)
}

func (d diffModel) PatchView() string {
	if d.err != nil {
		return "error: " + firstLine(d.err.Error())
	}
	if d.loadingPatch {
		return "loading…"
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
func parseFileDiffHeader(line string) (string, bool) {
	const prefix = "diff --git "
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}
	rest := line[len(prefix):]
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
