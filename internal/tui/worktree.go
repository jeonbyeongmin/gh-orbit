// Worktree switching + (in later steps) the `w` modal that drives it.
// Step 3 lands the switch msg + handler so a future modal — and any other
// surface that wants to retarget the TUI at a different worktree — has a
// single seam to call.
package tui

import (
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
)

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
	// Arm the HEAD jump so the post-reload refsLoadedMsg snaps the graph
	// cursor onto the new tree's HEAD commit instead of position 0.
	m.pendingHEADHash = pendingHEADSentinel
	cmd := m.reloadCmd()
	// Local Changes mode keeps its own status snapshot; reloadCmd doesn't
	// touch it. Re-fire the status load so the file tree reflects the new
	// tree immediately rather than waiting for the user to press `r`.
	if m.mode == viewModeLocalChanges {
		cmd = tea.Batch(cmd, loadStatusCmd(m.workdir))
	}
	m.status = "worktree: " + filepath.Base(path)
	m.statusStyle = statusOkS
	return m, cmd
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
