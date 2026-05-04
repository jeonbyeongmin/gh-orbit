// Right-pane diff sub-model. Stat is rendered inline (short text — no
// viewport needed); patch is rendered through bubbles/viewport in the d-window
// overlay. All git invocations go through tea.Cmd → tea.Msg with a request id
// so cursor moves that race past in-flight git show calls drop stale results.
package tui

import (
	"context"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

const (
	diffStatTimeout    = 30 * time.Second
	diffPatchTimeout   = 60 * time.Second
	diffDebounceWindow = 200 * time.Millisecond
)

// diffMode tracks which payload is currently rendered. Stat lives in the
// right pane; patch lives in the full-screen overlay opened by `d`.
type diffMode int

const (
	diffModeStat diffMode = iota
	diffModePatch
)

type diffModel struct {
	viewport     viewport.Model
	mode         diffMode
	currentHash  string
	statText     string
	patchText    string
	loadingStat  bool
	loadingPatch bool
	err          error
	// reqID is the latest dispatch token. Update keeps it in sync with the
	// root model so stale msg's can drop themselves.
	reqID uint64
	// patchViewportInit guards the one-shot SetSize on first patch load —
	// before that, viewport has zero dims and SetContent's truncation gives
	// nothing back.
	patchViewportInit bool
	// width/height of the *right pane* (stat view). The d-window uses the
	// full terminal so it sets the viewport directly.
	width, height int
}

func newDiffModel() diffModel {
	return diffModel{
		viewport: viewport.New(0, 0),
	}
}

// SetSize is called when the parent's right-pane content area changes. The
// viewport gets the same dims so when we flip to diffWindow it inherits them
// before being resized to full screen.
func (d *diffModel) SetSize(w, h int) {
	d.width = w
	d.height = h
}

// SetPatchViewportSize is the d-window's full-screen size. Called when entering
// diffWindow mode and on subsequent WindowSizeMsg's while the overlay is up.
func (d *diffModel) SetPatchViewportSize(w, h int) {
	d.viewport.Width = w
	d.viewport.Height = h
	d.patchViewportInit = true
	if d.patchText != "" {
		d.viewport.SetContent(d.patchText)
	}
}

// MarkLoadingStat stamps "we're about to load stat for hash" before the
// debounce tick fires. Done eagerly so View() can show "loading…" right away
// instead of stale text from the previous commit.
func (d *diffModel) MarkLoadingStat(hash string, reqID uint64) {
	d.mode = diffModeStat
	d.currentHash = hash
	d.statText = ""
	d.loadingStat = true
	d.err = nil
	d.reqID = reqID
}

// BeginPatchLoad transitions to patch mode for d-window. patchText is cleared
// so the overlay shows "loading…" until the cmd resolves.
func (d *diffModel) BeginPatchLoad(hash string, reqID uint64) {
	d.mode = diffModePatch
	d.currentHash = hash
	d.patchText = ""
	d.loadingPatch = true
	d.err = nil
	d.reqID = reqID
	d.viewport.SetContent("")
	d.viewport.GotoTop()
}

// ApplyStatLoaded merges a stat result if it matches the current reqID. Stale
// responses (older reqID) are silently dropped — the caller already moved on.
func (d *diffModel) ApplyStatLoaded(reqID uint64, hash, text string) {
	if reqID != d.reqID || hash != d.currentHash {
		return
	}
	d.loadingStat = false
	d.statText = text
	d.err = nil
}

// ApplyStatFailed mirrors ApplyStatLoaded for the error path.
func (d *diffModel) ApplyStatFailed(reqID uint64, hash string, err error) {
	if reqID != d.reqID || hash != d.currentHash {
		return
	}
	d.loadingStat = false
	d.err = err
}

// ApplyPatchLoaded merges a patch result for the d-window.
func (d *diffModel) ApplyPatchLoaded(reqID uint64, hash, text string) {
	if reqID != d.reqID || hash != d.currentHash {
		return
	}
	d.loadingPatch = false
	d.patchText = text
	d.err = nil
	if d.patchViewportInit {
		d.viewport.SetContent(text)
		d.viewport.GotoTop()
	}
}

// ApplyPatchFailed mirrors ApplyPatchLoaded for the error path.
func (d *diffModel) ApplyPatchFailed(reqID uint64, hash string, err error) {
	if reqID != d.reqID || hash != d.currentHash {
		return
	}
	d.loadingPatch = false
	d.err = err
}

// ScrollViewport forwards a key to the patch viewport. Used by the d-window
// key handler to delegate j/k/pgup/pgdown without exposing the viewport field.
func (d *diffModel) ScrollViewport(msg tea.Msg) {
	d.viewport, _ = d.viewport.Update(msg)
}

// StatView is what the right pane renders in normal mode. Short text → plain
// string is enough; ANSI escapes pass through lipgloss border untouched.
func (d diffModel) StatView() string {
	if d.currentHash == "" {
		return "(no commit selected)"
	}
	if d.err != nil {
		return "error: " + firstLine(d.err.Error())
	}
	if d.loadingStat {
		return "loading…"
	}
	if strings.TrimSpace(d.statText) == "" {
		return "(no changes)"
	}
	return d.statText
}

// PatchView is the d-window's body. The viewport handles ANSI-aware truncation
// per line; we just need to surface loading/error states first.
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

// --- messages and commands ---------------------------------------------------

type diffStatLoadedMsg struct {
	reqID uint64
	hash  string
	text  string
}

type diffStatFailedMsg struct {
	reqID uint64
	hash  string
	err   error
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

// diffDebounceMsg fires diffDebounceWindow after a cursor change. The root
// model checks reqID against its own counter and drops the message if the
// cursor moved again in the meantime.
type diffDebounceMsg struct {
	reqID uint64
	hash  string
}

// commitSelectedMsg is emitted by graphModel whenever the cursor lands on a
// new commit. The root model uses it to start the debounce timer for stat.
type commitSelectedMsg struct {
	hash string
}

// scheduleDiffStatCmd returns a tea.Tick that delivers diffDebounceMsg after
// diffDebounceWindow. Pending ticks aren't cancellable, but the reqID guard
// in Update drops everything but the freshest one.
func scheduleDiffStatCmd(reqID uint64, hash string) tea.Cmd {
	return tea.Tick(diffDebounceWindow, func(time.Time) tea.Msg {
		return diffDebounceMsg{reqID: reqID, hash: hash}
	})
}

func loadDiffStatCmd(dir, hash string, reqID uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), diffStatTimeout)
		defer cancel()
		text, err := git.Stat(ctx, dir, hash)
		if err != nil {
			return diffStatFailedMsg{reqID: reqID, hash: hash, err: err}
		}
		return diffStatLoadedMsg{reqID: reqID, hash: hash, text: text}
	}
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
