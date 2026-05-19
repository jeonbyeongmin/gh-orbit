// Async cmds and msg shapes for the Local Changes view. Mirrors refsaction.go's
// seam pattern: each git wrapper is reachable through a package-level var so
// tests can stub the subprocess calls; each cmd emits a typed msg the root
// model branches on.
package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

const localChangesCmdTimeout = 60 * time.Second

// Package-level seams over git.* — tests stub these to keep the cmd suite
// hermetic (no real subprocess in unit tests).
var (
	statusExec        = git.Status
	numstatExec       = git.LocalChangesNumstat
	diffFileExec      = git.DiffFile
	diffUntrackedExec = git.DiffUntracked
	addExec           = git.Add
	restoreStagedExec = git.RestoreStaged
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

// Mode entry signal — refs.go emits this when enter is pressed on the sticky
// "Local Changes" row, so the root model can flip viewMode + kick off the
// first status load.
type localChangesEnterRequestedMsg struct{}

// Sidebar summary (numstat) — feeds the inline meta on the `● Local Changes`
// sticky row. Independent of the status-load round trip used by
// viewModeLocalChanges so the sidebar can keep its meta fresh without
// triggering a diff dispatch.
type localChangesSummaryLoadedMsg struct {
	summary  git.LocalChangesSummary
	loadedAt time.Time
}

type localChangesSummaryFailedMsg struct {
	err error
}

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

// loadLocalChangesSummaryCmd dispatches `git diff --numstat HEAD` for the
// sidebar's inline meta. loadedAt is stamped on success so the row can show
// "Xm ago" without round-tripping a separate timestamp source.
func loadLocalChangesSummaryCmd(dir string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), localChangesCmdTimeout)
		defer cancel()
		summary, err := numstatExec(ctx, dir)
		if err != nil {
			return localChangesSummaryFailedMsg{err: err}
		}
		return localChangesSummaryLoadedMsg{summary: summary, loadedAt: time.Now()}
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
