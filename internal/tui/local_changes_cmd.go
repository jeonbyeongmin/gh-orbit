// Async cmds and msg shapes for the Local Changes view. Mirrors refsaction.go's
// seam pattern: each git wrapper is reachable through a package-level var so
// tests can stub the subprocess calls; each cmd emits a typed msg the root
// model branches on.
package tui

import (
	"context"
	"errors"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

const localChangesCmdTimeout = 60 * time.Second

// Package-level seams over git.* — tests stub these to keep the cmd suite
// hermetic (no real subprocess in unit tests).
var (
	statusExec        = git.Status
	diffFileExec      = git.DiffFile
	diffFileRawExec   = git.DiffFileRaw
	diffUntrackedExec = git.DiffUntracked
	addExec           = git.Add
	restoreStagedExec = git.RestoreStaged
	applyCachedExec   = git.ApplyCached
)

// Status load
type localChangesStatusLoadedMsg struct {
	entries []git.StatusEntry
}

type localChangesStatusFailedMsg struct {
	err error
}

// Diff load
type localChangesDiffLoadedMsg struct {
	reqID  uint64
	path   string
	staged bool
	text   string
}

type localChangesDiffFailedMsg struct {
	reqID  uint64
	path   string
	staged bool
	err    error
}

// Stage / unstage action results
type localChangesAddSucceededMsg struct {
	path string
}

type localChangesAddFailedMsg struct {
	path string
	err  error
}

type localChangesRestoreSucceededMsg struct {
	path string
}

type localChangesRestoreFailedMsg struct {
	path string
	err  error
}

// Per-hunk apply results. staged carries the side the hunk came from so the
// success handler can re-land the cursor on the same tree row.
type localChangesApplySucceededMsg struct {
	path   string
	staged bool
}

type localChangesApplyFailedMsg struct {
	path string
	err  error
}

// Mode entry signal — refs.go emits this when enter is pressed on the sticky
// "Local Changes" row, so the root model can flip viewMode + kick off the
// first status load.
type localChangesEnterRequestedMsg struct{}

// loadStatusCmd dispatches a fresh `git status --porcelain=v2` snapshot.
func loadStatusCmd(dir string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), localChangesCmdTimeout)
		defer cancel()
		entries, err := statusExec(ctx, dir)
		if err != nil {
			return localChangesStatusFailedMsg{err: err}
		}
		return localChangesStatusLoadedMsg{entries: entries}
	}
}

// loadDiffCmd routes to DiffFile or DiffUntracked based on (untracked, staged).
// Conflict files use the staged=false branch (interview decision 5: "git diff
// <file> 그대로"); the caller passes untracked=false for them.
func loadDiffCmd(dir, path string, staged, untracked bool, reqID uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), localChangesCmdTimeout)
		defer cancel()
		var (
			text string
			err  error
		)
		if untracked && !staged {
			text, err = diffUntrackedExec(ctx, dir, path)
		} else {
			text, err = diffFileExec(ctx, dir, path, staged)
		}
		if err != nil {
			return localChangesDiffFailedMsg{reqID: reqID, path: path, staged: staged, err: err}
		}
		return localChangesDiffLoadedMsg{reqID: reqID, path: path, staged: staged, text: text}
	}
}

func addCmd(dir, path string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), localChangesCmdTimeout)
		defer cancel()
		if err := addExec(ctx, dir, path); err != nil {
			return localChangesAddFailedMsg{path: path, err: err}
		}
		return localChangesAddSucceededMsg{path: path}
	}
}

func restoreStagedCmd(dir, path string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), localChangesCmdTimeout)
		defer cancel()
		if err := restoreStagedExec(ctx, dir, path); err != nil {
			return localChangesRestoreFailedMsg{path: path, err: err}
		}
		return localChangesRestoreSucceededMsg{path: path}
	}
}

// stageHunkCmd stages (or, for a staged entry, unstages) a single hunk. It
// fetches the uncolored diff fresh — the viewport copy is ANSI-colored and
// `git apply` can't parse it — extracts the hunkIdx-th hunk into a minimal
// patch, and applies it to the index. staged=true means the hunk came from
// the staged side, so the patch is applied in reverse to remove it.
func stageHunkCmd(dir, path string, staged bool, hunkIdx int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), localChangesCmdTimeout)
		defer cancel()
		raw, err := diffFileRawExec(ctx, dir, path, staged)
		if err != nil {
			return localChangesApplyFailedMsg{path: path, err: err}
		}
		patch, ok := extractHunkPatch(raw, hunkIdx)
		if !ok {
			return localChangesApplyFailedMsg{path: path, err: errHunkOutOfRange}
		}
		if err := applyCachedExec(ctx, dir, patch, staged); err != nil {
			return localChangesApplyFailedMsg{path: path, err: err}
		}
		return localChangesApplySucceededMsg{path: path, staged: staged}
	}
}

// errHunkOutOfRange surfaces when the hunk index no longer maps to a hunk in
// the freshly-fetched diff (the working tree changed between the diff render
// and the stage keypress). The user reloads (`r`) and retries.
var errHunkOutOfRange = errors.New("hunk no longer present — reload (r) and retry")

// extractHunkPatch builds a minimal applyable patch from an uncolored single-
// file `git diff`: the file header (every line before the first `@@`) plus the
// hunkIdx-th `@@` hunk. Returns false when the diff has no hunk at that index.
func extractHunkPatch(diff string, hunkIdx int) (string, bool) {
	lines := strings.Split(diff, "\n")
	firstHunk := -1
	for i, ln := range lines {
		if strings.HasPrefix(ln, "@@") {
			firstHunk = i
			break
		}
	}
	if firstHunk < 0 {
		return "", false
	}
	// Hunk i spans [starts[i], starts[i+1]) — or to EOF for the last one.
	var starts []int
	for i := firstHunk; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "@@") {
			starts = append(starts, i)
		}
	}
	if hunkIdx < 0 || hunkIdx >= len(starts) {
		return "", false
	}
	end := len(lines)
	if hunkIdx+1 < len(starts) {
		end = starts[hunkIdx+1]
	}
	out := append([]string{}, lines[:firstHunk]...)
	out = append(out, lines[starts[hunkIdx]:end]...)
	patch := strings.Join(out, "\n")
	if !strings.HasSuffix(patch, "\n") {
		patch += "\n"
	}
	return patch, true
}
