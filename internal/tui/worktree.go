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
	"github.com/mattn/go-runewidth"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// Package-level seams over git.Worktree* so tests can swap in stubs.
// Mirrors the checkoutExec pattern in checkout.go.
var (
	worktreesExec      = git.Worktrees
	worktreeAddExec    = git.WorktreeAdd
	worktreeRemoveExec = git.WorktreeRemove
)

// worktreeModalState backs viewModeWorktreeList. Modal open bumps reqID
// and arms loading=true; entries land via worktreesLoadedMsg and the
// dirty fan-out tags each row via worktreeDirtyResultMsg keyed by path.
// The map is keyed by entry.Path (absolute, as git emits) so a fanout
// goroutine's result lookup is O(1).
//
// addInput / addInlineErr back viewModeWorktreeAddInput.
// removeTarget backs viewModeWorktreeRemoveConfirm.
type worktreeModalState struct {
	entries        []git.Worktree
	dirty          map[string]bool
	cursor         int
	loading        bool
	loadErr        error
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
// from the fan-out. reqID matches the modal-open generation; mismatched
// msgs are dropped by the handler.
type worktreeDirtyResultMsg struct {
	reqID uint64
	path  string
	dirty bool
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
func worktreeDirtyFanoutCmd(reqID uint64, paths []string) tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(paths))
	for _, p := range paths {
		path := p // capture
		cmds = append(cmds, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), worktreeDirtyTimeout)
			defer cancel()
			entries, err := git.Status(ctx, path)
			if err != nil {
				return worktreeDirtyResultMsg{reqID: reqID, path: path, dirty: false}
			}
			return worktreeDirtyResultMsg{reqID: reqID, path: path, dirty: len(entries) > 0}
		})
	}
	return tea.Batch(cmds...)
}

// openWorktreeModal bumps reqID, arms loading, and dispatches the porcelain
// list. Callers (the `w` key handler) flip m.mode to viewModeWorktreeList
// before returning the cmd this produces.
func (m *Model) openWorktreeModal() tea.Cmd {
	m.worktreeModal.reqID++
	m.worktreeModal = worktreeModalState{
		reqID:   m.worktreeModal.reqID,
		loading: true,
		dirty:   make(map[string]bool),
	}
	return loadWorktreesCmd(m.workdir, m.worktreeModal.reqID)
}

// reloadWorktreeModal re-fires the porcelain list against the current
// reqID — used after a successful add/remove so the user sees the new
// list shape without closing and reopening the modal.
func (m *Model) reloadWorktreeModal() tea.Cmd {
	m.worktreeModal.loading = true
	m.worktreeModal.dirty = make(map[string]bool)
	return loadWorktreesCmd(m.workdir, m.worktreeModal.reqID)
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

// openWorktreeAddInput initializes the branch-name textinput for `a` on
// the list modal. Caller flips m.mode to viewModeWorktreeAddInput before
// returning the cmd.
func (m *Model) openWorktreeAddInput() tea.Cmd {
	ti := textinput.New()
	ti.Placeholder = "branch name"
	ti.CharLimit = 200
	ti.Width = 40
	ti.Focus()
	m.worktreeModal.addInput = ti
	m.worktreeModal.addInlineErr = ""
	return textinput.Blink
}

// renderWorktreeModalInner returns the multi-line content for the
// viewModeWorktreeList modal. Built for composeOverlay — no border /
// hint chrome here.
func (m Model) renderWorktreeModalInner() string {
	header := modalHeaderS.Render("[Worktrees]")
	hint := help.Render(helpTextWorktreeModal)

	if m.worktreeModal.loading {
		return strings.Join([]string{header, "loading…", hint}, "\n")
	}
	if m.worktreeModal.loadErr != nil {
		return strings.Join([]string{
			header,
			statusErrS.Render("load: " + firstLine(m.worktreeModal.loadErr.Error())),
			hint,
		}, "\n")
	}
	if len(m.worktreeModal.entries) == 0 {
		return strings.Join([]string{header, help.Render("(no worktrees)"), hint}, "\n")
	}

	width := worktreeModalRowWidth(m.width)
	lines := []string{header}
	for i, e := range m.worktreeModal.entries {
		row := renderWorktreeRow(e, m.workdir, m.worktreeModal.dirty[e.Path], width)
		if i == m.worktreeModal.cursor {
			row = selectedStyle.Render(row)
		}
		lines = append(lines, row)
	}
	lines = append(lines, hint)
	return strings.Join(lines, "\n")
}

// renderWorktreeRow formats one entry: "* basename · branch · locked · prunable · ●dirty".
// `*` flags the active worktree (m.workdir match); a leading "  " keeps
// non-active rows aligned. Width truncate uses runewidth so unicode
// basenames don't blow up the layout.
func renderWorktreeRow(e git.Worktree, activePath string, dirty bool, width int) string {
	prefix := "  "
	if e.Path == activePath {
		prefix = cursorStyle.Render("*") + " "
	}
	parts := []string{filepath.Base(e.Path)}
	switch {
	case e.Detached:
		parts = append(parts, "(detached)")
	case e.Branch != "":
		parts = append(parts, e.Branch)
	}
	if e.Locked {
		if e.LockReason != "" {
			parts = append(parts, "locked: "+e.LockReason)
		} else {
			parts = append(parts, "locked")
		}
	}
	if e.Prunable {
		parts = append(parts, "prunable")
	}
	if dirty {
		parts = append(parts, "●dirty")
	}
	body := strings.Join(parts, " · ")
	avail := width - 2 // prefix width
	if avail < 1 {
		return prefix
	}
	return prefix + runewidth.Truncate(body, avail, "…")
}

// worktreeModalRowWidth picks a body width slightly narrower than the
// screen so the modal box's border + padding leaves the row legible.
func worktreeModalRowWidth(screenWidth int) int {
	w := screenWidth - 8
	if w < 20 {
		w = 20
	}
	return w
}

// helpTextWorktreeModal is the hint line painted under the list. The
// individual key labels mirror the matrix the action handlers gate on.
const helpTextWorktreeModal = "[enter] switch · [a] add · [d] remove · [esc] close"

// helpTextWorktreeAddInput / helpTextWorktreeRemoveConfirm — hints for
// the two sub-modals reached from the list.
const helpTextWorktreeAddInput = "[enter] add · [esc] cancel"
const helpTextWorktreeRemoveClean = "[y] remove · [esc] cancel"
const helpTextWorktreeRemoveDirty = "[Y] force (discards changes) · [y/esc] cancel"
const helpTextWorktreeRemoveLocked = "[Y] force (overrides lock) · [y/esc] cancel"

// renderWorktreeAddInputInner — header, textinput row, derived-path hint,
// inline error or spacer (constant rows so the hint never bounces), and
// the action hint. Mirrors renderRefNameInputInner's shape.
func (m Model) renderWorktreeAddInputInner() string {
	header := modalHeaderS.Render("[New worktree]")
	input := m.worktreeModal.addInput.View()
	branch := strings.TrimSpace(m.worktreeModal.addInput.Value())
	pathHint := " "
	if branch != "" {
		pathHint = help.Render("→ " + deriveAddPath(m.workdir, branch))
	}
	errLine := " "
	switch {
	case m.worktreeModal.addInlineErr != "":
		errLine = statusErrS.Render(m.worktreeModal.addInlineErr)
	case m.worktreeModal.actionInFlight:
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
	t := m.worktreeModal.removeTarget
	header := confirmPromptS.Render("Remove worktree '" + filepath.Base(t.Path) + "'?")
	sub := help.Render(t.Path)
	hintText := helpTextWorktreeRemoveClean
	switch {
	case t.Locked:
		hintText = helpTextWorktreeRemoveLocked
	case m.worktreeModal.dirty[t.Path]:
		hintText = helpTextWorktreeRemoveDirty
	}
	if m.worktreeModal.actionInFlight {
		hintText = "removing…"
	}
	hint := help.Render(hintText)
	return strings.Join([]string{header, sub, hint}, "\n")
}

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
	// The new tree may not host any of the refs the old stream was
	// filtered to. Reset to the unified --all view so the first load
	// shows something meaningful; the user can re-filter from there.
	m.currentRefs = []string{refsAllSentinel}
	// Switch is intentionally cursor-amnesiac: same branch name in two
	// trees is rare and would land the cursor on the wrong row anyway.
	// Drop persist state explicitly so reloadCmd's snapshot below doesn't
	// repopulate it from the (about-to-be-replaced) refs pane.
	m.pendingRefCursorPersist = persistedRefHandle{}
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
