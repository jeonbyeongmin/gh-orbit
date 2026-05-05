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

// diffModel hosts the full-screen patch overlay opened with `d`. The Changes
// tab owns per-file stats and the follower patch viewport (changesModel).
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
	width, height     int
}

func newDiffModel() diffModel {
	return diffModel{
		viewport: viewport.New(0, 0),
	}
}

func (d *diffModel) SetSize(w, h int) {
	d.width = w
	d.height = h
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
	d.viewport.SetContent("")
	d.viewport.GotoTop()
}

// ClosePatch releases the patch text and clears the viewport so a 10MB
// vendor diff doesn't sit in memory after the user closes the overlay.
func (d *diffModel) ClosePatch() {
	d.patchText = ""
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

type diffStatLoadedMsg struct {
	reqID uint64
	hash  string
	files []git.FileStat
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

type diffDebounceMsg struct {
	reqID uint64
	hash  string
}

type commitSelectedMsg struct {
	hash string
}

// scheduleDiffStatCmd uses tea.Tick (not time.AfterFunc) because pending ticks
// can't be cancelled mid-flight; the reqID guard in Update drops all but the
// freshest one when the user keeps moving the cursor inside the window.
func scheduleDiffStatCmd(reqID uint64, hash string) tea.Cmd {
	return tea.Tick(diffDebounceWindow, func(time.Time) tea.Msg {
		return diffDebounceMsg{reqID: reqID, hash: hash}
	})
}

func loadDiffStatCmd(dir, hash string, reqID uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), diffStatTimeout)
		defer cancel()
		files, err := git.Stat(ctx, dir, hash)
		if err != nil {
			return diffStatFailedMsg{reqID: reqID, hash: hash, err: err}
		}
		return diffStatLoadedMsg{reqID: reqID, hash: hash, files: files}
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
