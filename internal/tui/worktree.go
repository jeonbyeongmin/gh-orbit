// Worktree switching + (in later steps) the `w` modal that drives it.
// Step 3 lands the switch msg + handler so a future modal — and any other
// surface that wants to retarget the TUI at a different worktree — has a
// single seam to call.
package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// worktreeDirtyTimeout caps how long a per-worktree `git status` call may
// run before the dirty fan-out drops it. Long enough for cold-cache repos,
// short enough that a stuck git invocation doesn't leave a stale modal.
const worktreeDirtyTimeout = 30 * time.Second

// currentWorktreeDirtyMsg carries the dirty-or-clean result for the
// CURRENT m.workdir. dir is included so the Update handler can drop a
// stale result that arrived after a worktree switch.
type currentWorktreeDirtyMsg struct {
	dir   string
	dirty bool
}

// loadCurrentWorktreeDirtyCmd runs `git status --porcelain` against `dir`
// and folds the result into a boolean. Status (not WorktreeStatus) is
// already async-safe and locale-locked by gitEnv; we discard the parsed
// entries and only keep whether any are present.
func loadCurrentWorktreeDirtyCmd(dir string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), worktreeDirtyTimeout)
		defer cancel()
		entries, err := git.Status(ctx, dir)
		if err != nil {
			// Soft-fail: header just won't show a dirty marker. The status
			// surface for the user's manual operations still routes via the
			// normal cmd error chain.
			return currentWorktreeDirtyMsg{dir: dir, dirty: false}
		}
		return currentWorktreeDirtyMsg{dir: dir, dirty: len(entries) > 0}
	}
}

// formatWorktreeHeader produces the one-line summary painted at the very
// top of the refs pane: "Worktree: <name> · <branch|(detached)> · ●dirty".
// Empty path returns "" so the header row vanishes — that's the case
// before New()'s Getwd has run or when it fails.
func formatWorktreeHeader(path, branch string, detached, dirty bool) string {
	if path == "" {
		return ""
	}
	name := filepath.Base(path)
	var parts []string
	parts = append(parts, "Worktree: "+name)
	switch {
	case detached:
		parts = append(parts, "(detached)")
	case branch != "":
		parts = append(parts, branch)
	}
	if dirty {
		parts = append(parts, "●dirty")
	}
	return strings.Join(parts, " · ")
}

// switchWorktreeMsg retargets the TUI at a different worktree path. The
// handler validates the path is a real working tree before mutating any
// state — a stale modal entry (worktree pruned externally between list and
// switch) surfaces as a status-bar error instead of leaving the TUI
// pointing at a non-existent dir.
type switchWorktreeMsg struct {
	path string
}

// switchWorktree applies switchWorktreeMsg: validates path, rewrites
// m.workdir, drops any cursor-persist state from the previous tree, and
// triggers a full reload (commits + refs + head ancestors + — if the user
// is inside Local Changes mode — a fresh status load too).
//
// Returning (m, nil) on a validation failure leaves the previous workdir
// intact and surfaces an error on the status bar; the modal layer reacts
// to that by staying open.
func (m Model) switchWorktree(path string) (Model, tea.Cmd) {
	if err := validateWorktreePath(path); err != nil {
		m.status = fmt.Sprintf("worktree switch: %s", err)
		m.statusStyle = statusErrS
		return m, nil
	}
	if path == m.workdir {
		m.status = "already on this worktree"
		m.statusStyle = statusOkS
		return m, nil
	}
	m.workdir = path
	// The new tree may not host any of the refs / stash entries the old
	// stream was filtered to. Reset to the unified --all view so the first
	// load shows something meaningful; the user can re-filter from there.
	m.currentRefs = []string{refsAllSentinel}
	m.currentStashHashes = nil
	m.currentStashByHash = nil
	// Switch is intentionally cursor-amnesiac: same branch name in two
	// trees is rare and would land the cursor on the wrong row anyway.
	// Drop persist state explicitly so reloadCmd's snapshot below doesn't
	// repopulate it from the (about-to-be-replaced) refs pane.
	m.pendingRefCursorPersist = persistedRefHandle{}
	m.pendingRefCursorName = ""
	m.pendingRefCursorAfterDelete = deletedRefHandle{}
	// Reset dirty so the header doesn't flash the previous tree's marker
	// while the new dirty fan-out is in flight.
	m.currentWorktreeDirty = false
	// Arm the HEAD jump so the post-reload refsLoadedMsg snaps the graph
	// cursor onto the new tree's HEAD commit instead of position 0.
	m.pendingHEADHash = pendingHEADSentinel
	cmd := tea.Batch(m.reloadCmd(), loadCurrentWorktreeDirtyCmd(m.workdir))
	// Local Changes mode keeps its own status snapshot; reloadCmd doesn't
	// touch it. Re-fire the status load so the file tree reflects the new
	// tree immediately rather than waiting for the user to press `r`.
	if m.mode == viewModeLocalChanges {
		cmd = tea.Batch(cmd, loadStatusCmd(m.workdir))
	}
	m.status = "worktree: " + filepath.Base(path)
	m.statusStyle = statusOkS
	// Show the header immediately with the data we have; the dirty fan-out
	// will repaint when it returns.
	m.refreshWorktreeHeader()
	return m, cmd
}

// refreshWorktreeHeader rebuilds the refs-sidebar sticky header from the
// model's current view of the live worktree. Branch comes from the local
// refs section's IsHead entry; absent IsHead among non-empty locals
// means HEAD points outside refs/heads/ (a tag, a remote, or a raw hash)
// so the header reads as detached. An empty local-refs slice — either
// pre-load or a fresh repo with no commits — leaves both blank so the
// header just shows the worktree name without misleading state.
func (m *Model) refreshWorktreeHeader() {
	branch, detached := "", false
	locals := m.refs.LocalRefs()
	if len(locals) > 0 {
		detached = true
		for _, ref := range locals {
			if ref.IsHead {
				branch = ref.ShortName
				detached = false
				break
			}
		}
	}
	m.refs.SetWorktreeHeader(formatWorktreeHeader(m.workdir, branch, detached, m.currentWorktreeDirty))
}

// validateWorktreePath fails fast on the two cases a stale list entry can
// produce: the path no longer exists (worktree pruned) or the path is a
// directory but isn't a git worktree (no `.git` entry — either file or
// directory). git itself would surface this on the first wrapper call, but
// catching it before m.workdir flips keeps the rollback story trivial
// (just don't mutate anything).
func validateWorktreePath(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("path: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("path is not a directory: %s", path)
	}
	gitEntry := filepath.Join(path, ".git")
	if _, err := os.Stat(gitEntry); err != nil {
		return fmt.Errorf("not a git worktree (no .git): %s", path)
	}
	return nil
}
