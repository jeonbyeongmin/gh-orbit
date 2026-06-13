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
	"sort"
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
	reqID      uint64
	path       string
	dirtyCount int // changed-file count (0 = clean or unknown/timed-out)
	timedOut   bool
	// subject / when carry the row's last-commit metadata, fetched in the
	// same goroutine as the dirty probe so they land in one msg (no
	// incremental jitter between the `●` marker and the subject/time
	// columns). Both stay zero-valued when the tree has no commits yet or
	// the probe timed out — the modal renders a blank last-commit column.
	subject string
	when    time.Time
	// ahead / behind / hasUpstream carry the worktree's position vs its
	// upstream, from the same goroutine. hasUpstream=false (no upstream /
	// detached) omits the `↑↓` column.
	ahead, behind int
	hasUpstream   bool
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
// worktreeDirtyResultMsg as the per-worktree probes return. Using tea.Batch
// lets Bubble Tea schedule them concurrently — the rows light up
// incrementally instead of waiting for the slowest tree.
//
// Each goroutine gets a worktreeDirtyBudget deadline (3s) shared across the
// three probes it runs: the dirty `git status`, the last-commit `git log -1`,
// and the ahead/behind `git rev-list`. Folding them into one goroutine keeps a
// single reqID-tagged msg and makes the `●` marker, the subject/time columns,
// and the `↑↓` counts appear together. On
// timeout the msg carries timedOut=true so the sidebar can render a `?`
// dirty placeholder instead of trusting the (effectively unknown) value;
// the last-commit columns just stay blank. Without the budget, a stuck NFS
// / network mount could leave the sidebar visually stalled — E3 eng-review
// iron rule.
//
// Order matters: the dirty probe runs first so it owns the budget — dirty is
// the more critical signal. WorktreeLastCommit runs with the remaining time
// and its error is intentionally dropped (blank columns), since a missing
// subject is non-fatal and a genuine repo failure already surfaces via the
// dirty probe.
func worktreeDirtyFanoutCmd(reqID uint64, paths []string) tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(paths))
	for _, p := range paths {
		path := p // capture
		cmds = append(cmds, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), worktreeDirtyBudget)
			defer cancel()
			entries, err := git.Status(ctx, path)
			subject, when, _ := git.WorktreeLastCommit(ctx, path)
			ahead, behind, hasUpstream, _ := git.WorktreeAheadBehind(ctx, path)
			if err != nil {
				timedOut := errors.Is(err, context.DeadlineExceeded) || ctx.Err() == context.DeadlineExceeded
				return worktreeDirtyResultMsg{reqID: reqID, path: path, dirtyCount: 0, timedOut: timedOut, subject: subject, when: when, ahead: ahead, behind: behind, hasUpstream: hasUpstream}
			}
			return worktreeDirtyResultMsg{reqID: reqID, path: path, dirtyCount: len(entries), subject: subject, when: when, ahead: ahead, behind: behind, hasUpstream: hasUpstream}
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
// `d` on a worktree row in the sidebar. Rejects removing the main
// worktree (permanent constraint — git refuses regardless of state)
// or the current worktree (switch first). Guard order surfaces the
// stronger constraint first so a main-on-main case doesn't mislead
// the user into a switch-then-retry round trip.
func (m Model) beginWorktreeRemove(target git.Worktree) Model {
	if m.worktreeAction.actionInFlight {
		return m
	}
	if target.IsMain {
		m.status = "remove: cannot remove main worktree"
		m.statusStyle = statusErrS
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

// switchConfirmTTL is how long the "→ switched: A → B" status line
// stays before tea.Tick clears it. Long enough for the user to register
// the change visually (scenario 3: switch confirmation ambiguity); short
// enough that the line returns to its useful default before any follow-up
// action lands.
const switchConfirmTTL = 3 * time.Second

// statusClearTickMsg fires from tea.Tick(switchConfirmTTL) after a
// switch-confirmation status line is painted. seq is captured at
// dispatch time; on receipt the handler clears the line only when
// m.statusTickSeq still matches — a re-switch (or any other action that
// bumps statusTickSeq via setStatusWithTTL) invalidates the pending
// tick so it can't wipe a fresh status.
type statusClearTickMsg struct {
	seq uint64
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
	prevName := filepath.Base(m.workdir)
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
	// Hard-reset the graph before the reload: the new tree's history is a
	// different graph entirely, so keeping the old one on screen (the
	// stale-while-revalidate default) would mislead. The blank placeholder
	// here is an honest "we moved somewhere else" signal.
	resetCmd := m.graph.ResetForReload()
	// reloadCmd bumps sidebarWorktreesReqID and dispatches loadWorktreesCmd
	// itself — that covers the in-flight invalidation for the new tree, so
	// the switch handler no longer fans out explicitly.
	cmd := tea.Batch(resetCmd, m.reloadCmd())
	// Local Changes mode keeps its own status snapshot; reloadCmd doesn't
	// touch it. Re-fire the status load so the file tree reflects the new
	// tree immediately rather than waiting for the user to press `r`.
	if m.mode == viewModeLocalChanges {
		cmd = tea.Batch(cmd, loadStatusCmd(m.workdir))
	}
	// switch confirmation: "→ switched: prev → new" with a 3s tea.Tick
	// auto-clear. statusTickSeq snapshot at dispatch time; only the
	// matching tick is allowed to clear (scenario 3 polish — design doc
	// SC #7).
	m.statusTickSeq++
	seq := m.statusTickSeq
	m.status = fmt.Sprintf("→ switched: %s → %s", prevName, filepath.Base(path))
	m.statusStyle = statusOkS
	cmd = tea.Batch(cmd, tea.Tick(switchConfirmTTL, func(time.Time) tea.Msg {
		return statusClearTickMsg{seq: seq}
	}))
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

// worktreesModalState backs viewModeWorktreesModal — the full-screen
// worktree dashboard toggled by `w` (renders via renderWorktreesView, the
// same graph-replacing seam Local Changes uses). An int cursor into
// m.modalWorktrees() (the active display order) at open time; reloads (after
// add / remove) clamp via beginWorktreesModal on re-entry. sortByCommit is
// the session-local last-commit sort toggle (`s`); it resets on close because
// the whole struct is zeroed there. ("Modal" in the name is a historical
// artifact from when it was a centered overlay.)
type worktreesModalState struct {
	cursor       int
	sortByCommit bool
}

const helpTextWorktreesModal = "[j/k] navigate · [enter] switch · [O] review PR · [a] add · [d] remove · [s] sort · [esc] close"

// modalWorktrees returns the worktrees in the modal's active
// display order. With sortByCommit off it's the git natural order
// (Worktrees() as-is, main first). On, the main worktree stays pinned at
// the top and the rest sort by last-commit time descending, with unknown
// rows (zero-value `when` — still loading / timed out / no commits) last.
// The original slice is never mutated; render and the cursor helpers all
// route through this so the cursor index and the rendered rows agree.
func (m Model) modalWorktrees() []git.Worktree {
	wts := m.refs.Worktrees()
	if !m.worktreesModal.sortByCommit || len(wts) < 2 {
		return wts
	}
	sorted := make([]git.Worktree, len(wts))
	copy(sorted, wts)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].IsMain != sorted[j].IsMain {
			return sorted[i].IsMain // main pinned first
		}
		_, wi := m.refs.WorktreeLastCommit(sorted[i].Path)
		_, wj := m.refs.WorktreeLastCommit(sorted[j].Path)
		if wi.IsZero() != wj.IsZero() {
			return !wi.IsZero() // unknown last-commit sinks to the bottom
		}
		return wi.After(wj) // newest first
	})
	return sorted
}

// beginWorktreesModal opens viewModeWorktreesModal. Cursor lands on
// the current worktree if found, else 0. Empty inventory surfaces an
// inline error and stays in viewModeNormal.
func (m Model) beginWorktreesModal() (Model, tea.Cmd) {
	wts := m.modalWorktrees()
	if len(wts) == 0 {
		m.status = "worktrees: none loaded yet"
		m.statusStyle = statusErrS
		return m, nil
	}
	cursor := 0
	for i, wt := range wts {
		if wt.Path == m.workdir {
			cursor = i
			break
		}
	}
	m.worktreesModal.cursor = cursor
	m.mode = viewModeWorktreesModal
	m.status = ""
	return m, nil
}

func (m Model) worktreesModalMoveCursor(delta int) Model {
	wts := m.modalWorktrees()
	if len(wts) == 0 {
		return m
	}
	c := m.worktreesModal.cursor + delta
	if c < 0 {
		c = 0
	}
	if c >= len(wts) {
		c = len(wts) - 1
	}
	m.worktreesModal.cursor = c
	return m
}

// worktreesModalToggleSort flips the last-commit sort (`s`) and keeps the cursor
// on the same worktree across the reorder so the highlight doesn't jump to a
// different tree under the user's hands.
func (m Model) worktreesModalToggleSort() Model {
	before := m.modalWorktrees()
	curPath := ""
	if m.worktreesModal.cursor >= 0 && m.worktreesModal.cursor < len(before) {
		curPath = before[m.worktreesModal.cursor].Path
	}
	m.worktreesModal.sortByCommit = !m.worktreesModal.sortByCommit
	for i, wt := range m.modalWorktrees() {
		if wt.Path == curPath {
			m.worktreesModal.cursor = i
			break
		}
	}
	return m
}

// cursorWorktree returns the worktree under the dashboard cursor, or ok=false
// when the cursor is out of range (empty / mid-reload list). Shared by the
// enter / remove / review-PR handlers so the bounds guard lives in one place.
func (m Model) cursorWorktree() (git.Worktree, bool) {
	wts := m.modalWorktrees()
	if m.worktreesModal.cursor < 0 || m.worktreesModal.cursor >= len(wts) {
		return git.Worktree{}, false
	}
	return wts[m.worktreesModal.cursor], true
}

// worktreesModalEnter closes the modal and dispatches a switchWorktreeMsg
// for the cursor entry. The Model's existing switchWorktree handler does
// the validate + retarget + reload chain; closing first means the switch
// confirmation status renders on the normal layout, not under an overlay.
func (m Model) worktreesModalEnter() (Model, tea.Cmd) {
	wt, ok := m.cursorWorktree()
	if !ok {
		return m, nil
	}
	m.mode = viewModeNormal
	m.worktreesModal = worktreesModalState{}
	if wt.Path == m.workdir {
		m.status = "already on this worktree"
		m.statusStyle = statusOkS
		return m, nil
	}
	return m, func() tea.Msg { return switchWorktreeMsg{path: wt.Path} }
}

// worktreesModalRemove arms the existing remove-confirm sub-modal for the
// cursor entry. beginWorktreeRemove already rejects removing the
// current worktree with a status line.
func (m Model) worktreesModalRemove() (Model, tea.Cmd) {
	target, ok := m.cursorWorktree()
	if !ok {
		return m, nil
	}
	m = m.beginWorktreeRemove(target)
	return m, nil
}

// worktreesModalReviewPR opens the cursor worktree's open PR in the review
// overlay — the same beginPRReviewFor path graph `O` and the `l` modal use.
// reviewFromWorktrees is armed so the overlay returns to the dashboard on
// close / merge (the review-and-compare loop). A worktree whose branch has no
// open PR reports on the status line instead of opening an empty overlay.
func (m Model) worktreesModalReviewPR() (Model, tea.Cmd) {
	wt, ok := m.cursorWorktree()
	if !ok {
		return m, nil
	}
	pr, hasPR := m.prs[wt.Branch]
	if !hasPR {
		m.status = "no open PR for this worktree's branch"
		m.statusStyle = statusErrS
		return m, nil
	}
	m.status = "" // opening the overlay — drop any stale dashboard status
	m.reviewReturnMode = viewModeWorktreesModal
	return m.beginPRReviewFor(pr.Number)
}
