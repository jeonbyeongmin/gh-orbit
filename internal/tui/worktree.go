// Worktree switching + (in later steps) the `w` modal that drives it.
// Step 3 lands the switch msg + handler so a future modal — and any other
// surface that wants to retarget the TUI at a different worktree — has a
// single seam to call.
package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// Package-level seams over git.Worktree* so tests can swap in stubs.
// Mirrors the checkoutExec pattern in checkout.go.
var (
	worktreesExec      = git.Worktrees
	worktreeAddExec    = git.WorktreeAdd
	worktreeRemoveExec = git.WorktreeRemove
)

// worktreeActionState backs the add-input / remove-confirm sub-modals
// triggered from a worktree row in the sidebar. reqID survives across
// the two sub-modals so a slow add response can't collide with a
// subsequent remove dispatch. actionInFlight gates a/d key repeats while
// a git wrapper is running.
//
// addInput / addInlineErr back viewModeWorktreeAddInput.
// removeTarget backs viewModeWorktreeRemoveConfirm.
type worktreeActionState struct {
	reqID          uint64
	actionInFlight bool
	addInput       textinput.Model
	addInlineErr   string
	removeTarget   git.Worktree
	// pendingAddPath remembers the absolute path the in-flight add is
	// creating so worktreeAddSucceededMsg can land the cursor on it once
	// the post-add reload completes.
	pendingAddPath string
}

// worktreesLoadedMsg carries the porcelain list result back to the modal.
// reqID lets the handler drop a list that arrived after the user closed
// and reopened the modal.
type worktreesLoadedMsg struct {
	reqID   uint64
	entries []git.Worktree
}

// worktreesLoadFailedMsg surfaces a porcelain-list failure; the modal
// shows the error in place of the list and keeps esc working.
type worktreesLoadFailedMsg struct {
	reqID uint64
	err   error
}

// worktreeDirtyResultMsg carries one path's dirty-or-clean state back
// from the fan-out. reqID matches the sidebar-load generation; mismatched
// msgs are dropped by the handler. timedOut=true signals the per-row 3s
// budget was exhausted (E3) — the row renders a `?` placeholder so the
// sidebar never silently lies about a slow worktree.
type worktreeDirtyResultMsg struct {
	reqID    uint64
	path     string
	dirty    bool
	timedOut bool
}

// worktreeAddSucceededMsg / worktreeAddFailedMsg report the outcome of
// the `a` action. Success carries the path so the post-reload handler
// can land the cursor on the new entry.
type worktreeAddSucceededMsg struct {
	reqID  uint64
	path   string
	branch string
}
type worktreeAddFailedMsg struct {
	reqID uint64
	err   error
}

// worktreeRemoveSucceededMsg / worktreeRemoveFailedMsg report the outcome
// of the `d` action. failure carries dirty/locked classification so the
// modal can branch to the force prompt instead of dumping git's stderr.
type worktreeRemoveSucceededMsg struct {
	reqID uint64
	path  string
}
type worktreeRemoveFailedMsg struct {
	reqID  uint64
	path   string
	err    error
	dirty  bool
	locked bool
}

// loadWorktreesCmd runs `git worktree list --porcelain -z` and emits the
// result tagged with reqID.
func loadWorktreesCmd(dir string, reqID uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), worktreeDirtyTimeout)
		defer cancel()
		entries, err := worktreesExec(ctx, dir)
		if err != nil {
			return worktreesLoadFailedMsg{reqID: reqID, err: err}
		}
		return worktreesLoadedMsg{reqID: reqID, entries: entries}
	}
}

// worktreeDirtyFanoutCmd dispatches one cmd per path; each emits its own
// worktreeDirtyResultMsg as the per-worktree `git status` returns. Using
// tea.Batch lets Bubble Tea schedule them concurrently — the rows light
// up incrementally instead of waiting for the slowest tree.
//
// Each goroutine gets a worktreeDirtyBudget deadline (3s). On timeout the
// msg carries timedOut=true so the sidebar can render a `?` placeholder
// instead of trusting the (effectively unknown) dirty value. Without the
// budget, a stuck NFS / network mount could leave the sidebar visually
// stalled — E3 eng-review iron rule.
func worktreeDirtyFanoutCmd(reqID uint64, paths []string) tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(paths))
	for _, p := range paths {
		path := p // capture
		cmds = append(cmds, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), worktreeDirtyBudget)
			defer cancel()
			entries, err := git.Status(ctx, path)
			if err != nil {
				timedOut := errors.Is(err, context.DeadlineExceeded) || ctx.Err() == context.DeadlineExceeded
				return worktreeDirtyResultMsg{reqID: reqID, path: path, dirty: false, timedOut: timedOut}
			}
			return worktreeDirtyResultMsg{reqID: reqID, path: path, dirty: len(entries) > 0}
		})
	}
	return tea.Batch(cmds...)
}

// beginWorktreeAdd opens the add-input sub-modal triggered by `a` on a
// worktree row in the sidebar. Bumps the action reqID so any in-flight
// reply from an earlier add/remove drops on arrival.
func (m Model) beginWorktreeAdd() (Model, tea.Cmd) {
	if m.worktreeAction.actionInFlight {
		return m, nil
	}
	m.worktreeAction.reqID++
	ti := textinput.New()
	ti.Placeholder = "branch name"
	ti.CharLimit = 200
	ti.Width = 40
	ti.Focus()
	m.worktreeAction.addInput = ti
	m.worktreeAction.addInlineErr = ""
	m.mode = viewModeWorktreeAddInput
	return m, textinput.Blink
}

// beginWorktreeRemove opens the remove-confirm sub-modal triggered by
// `d` on a worktree row in the sidebar. Rejects removing the current
// worktree (the user must switch first — git refuses anyway, but we
// surface a friendlier message before invoking the wrapper).
func (m Model) beginWorktreeRemove(target git.Worktree) Model {
	if m.worktreeAction.actionInFlight {
		return m
	}
	if target.Path == m.workdir {
		m.status = "remove: cannot remove current worktree — switch first"
		m.statusStyle = statusErrS
		return m
	}
	m.worktreeAction.reqID++
	m.worktreeAction.removeTarget = target
	m.mode = viewModeWorktreeRemoveConfirm
	return m
}

// deriveAddPath computes the default new-worktree path: sibling directory
// of the active worktree's parent. e.g. activePath=/repo/main, branch=feat
// → /repo/feat. Matches the convention `git worktree add ../feat -b feat`
// would land on if invoked from main.
func deriveAddPath(activePath, branch string) string {
	if activePath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(activePath), branch)
}

// worktreeAddCmd runs WorktreeAdd. Invalid branch names are caught by
// `git worktree add` itself — the wrapper surfaces git's stderr verbatim
// via worktreeAddFailedMsg, so the inline error line reads naturally
// ("fatal: '<x>' is not a valid branch name") without a separate
// validation hop.
func worktreeAddCmd(dir, path, branch string, reqID uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), worktreeDirtyTimeout)
		defer cancel()
		if err := worktreeAddExec(ctx, dir, path, branch, true); err != nil {
			return worktreeAddFailedMsg{reqID: reqID, err: err}
		}
		return worktreeAddSucceededMsg{reqID: reqID, path: path, branch: branch}
	}
}

// worktreeRemoveCmd runs WorktreeRemove and classifies a dirty/locked
// failure so the modal can transition to a force prompt instead of just
// surfacing stderr.
func worktreeRemoveCmd(dir, path string, force bool, reqID uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), worktreeDirtyTimeout)
		defer cancel()
		if err := worktreeRemoveExec(ctx, dir, path, force); err != nil {
			return worktreeRemoveFailedMsg{
				reqID:  reqID,
				path:   path,
				err:    err,
				dirty:  errors.Is(err, git.ErrWorktreeDirty),
				locked: errors.Is(err, git.ErrWorktreeLocked),
			}
		}
		return worktreeRemoveSucceededMsg{reqID: reqID, path: path}
	}
}

// helpTextWorktreeAddInput / helpTextWorktreeRemoveConfirm — hints for
// the two action sub-modals triggered from a worktree row in the sidebar.
const helpTextWorktreeAddInput = "[enter] add · [esc] cancel"
const helpTextWorktreeRemoveClean = "[y] remove · [esc] cancel"
const helpTextWorktreeRemoveDirty = "[Y] force (discards changes) · [y/esc] cancel"
const helpTextWorktreeRemoveLocked = "[Y] force (overrides lock) · [y/esc] cancel"

// renderWorktreeAddInputInner — header, textinput row, derived-path hint,
// inline error or spacer (constant rows so the hint never bounces), and
// the action hint.
func (m Model) renderWorktreeAddInputInner() string {
	header := modalHeaderS.Render("[New worktree]")
	input := m.worktreeAction.addInput.View()
	branch := strings.TrimSpace(m.worktreeAction.addInput.Value())
	pathHint := " "
	if branch != "" {
		pathHint = help.Render("→ " + deriveAddPath(m.workdir, branch))
	}
	errLine := " "
	switch {
	case m.worktreeAction.addInlineErr != "":
		errLine = statusErrS.Render(m.worktreeAction.addInlineErr)
	case m.worktreeAction.actionInFlight:
		errLine = statusBusyS.Render("adding…")
	}
	hint := help.Render(helpTextWorktreeAddInput)
	return strings.Join([]string{header, input, pathHint, errLine, hint}, "\n")
}

// renderWorktreeRemoveConfirmInner derives the prompt from the cached
// dirty/locked state of the cursor entry. Force-removing a locked-AND-
// dirty entry uses one combined `[Y] force` matrix — git itself handles
// either failure mode with --force.
func (m Model) renderWorktreeRemoveConfirmInner() string {
	t := m.worktreeAction.removeTarget
	header := confirmPromptS.Render("Remove worktree '" + filepath.Base(t.Path) + "'?")
	sub := help.Render(t.Path)
	hintText := helpTextWorktreeRemoveClean
	switch {
	case t.Locked:
		hintText = helpTextWorktreeRemoveLocked
	case m.refs.WorktreeDirty(t.Path):
		hintText = helpTextWorktreeRemoveDirty
	}
	if m.worktreeAction.actionInFlight {
		hintText = "removing…"
	}
	hint := help.Render(hintText)
	return strings.Join([]string{header, sub, hint}, "\n")
}

// worktreeDirtyBudget caps how long a per-worktree `git status` call may
// run before the dirty fan-out drops it. Per E3 eng-review: 3 seconds is
// short enough that a stuck NFS / slow network mount can't visibly stall
// the sidebar, but long enough that a cold-cache repo still answers.
const worktreeDirtyBudget = 3 * time.Second

// worktreeDirtyTimeout caps git list / add / remove calls. These need a
// more generous deadline (`git worktree list` can be slow on big repos);
// the per-row fan-out uses worktreeDirtyBudget instead.
const worktreeDirtyTimeout = 30 * time.Second

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
	// The new tree may not host any of the refs the old stream was
	// filtered to. Reset to the unified --all view so the first load
	// shows something meaningful; the user can re-filter from there.
	m.currentRefs = []string{refsAllSentinel}
	// Switch is intentionally cursor-amnesiac for the worktree row state —
	// the sidebar's cursor model (onWorktree / onLocalChanges) gets reset by
	// SetWorktrees post-reload anyway. Refs-cursor persist state was retired
	// with the refs LIST subtract.
	// Arm the HEAD jump so the post-reload refsLoadedMsg snaps the graph
	// cursor onto the new tree's HEAD commit instead of position 0.
	m.pendingHEADHash = pendingHEADSentinel
	// Bump the sidebar reqID so any in-flight worktree-load reply from the
	// previous tree drops on arrival.
	m.sidebarWorktreesReqID++
	cmd := tea.Batch(m.reloadCmd(), loadWorktreesCmd(m.workdir, m.sidebarWorktreesReqID), loadLocalChangesSummaryCmd(m.workdir))
	// Local Changes mode keeps its own status snapshot; reloadCmd doesn't
	// touch it. Re-fire the status load so the file tree reflects the new
	// tree immediately rather than waiting for the user to press `r`.
	if m.mode == viewModeLocalChanges {
		cmd = tea.Batch(cmd, loadStatusCmd(m.workdir))
	}
	m.status = "worktree: " + filepath.Base(path)
	m.statusStyle = statusOkS
	// Sidebar's current-worktree marker depends on m.workdir; nudge the
	// existing snapshot so the ▶ row flips immediately while the fresh
	// list (with its dirty fan-out) is in flight. The next sidebar load
	// will overwrite this with authoritative data.
	m.refs.SetWorktrees(m.refs.Worktrees(), m.workdir)
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
