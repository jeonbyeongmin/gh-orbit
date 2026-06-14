// Key dispatch for Model.Update, extracted out of the giant type switch.
// updateKey owns the shared quit-disarm preamble and routes to one
// handle*Key method per viewMode, preserving the original guard order:
// overlay/modal modes first (so esc closes the surface instead of the
// app), then the normal-mode global shortcuts with the graph as final
// fallthrough.
package tui

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

func (m Model) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Any key other than ctrl+c disarms a pending quit. Handled here once,
	// before the per-mode dispatch, so every branch shares one disarm
	// point. Only the arm hint is cleared so an unrelated status survives.
	if m.quitArmed && msg.String() != "ctrl+c" {
		m.quitArmed = false
		if m.status == quitArmHint {
			m.status = ""
			m.statusStyle = statusOkS
		}
	}
	// The viewMode guard runs before the global ctrl+c quit branch so
	// `esc` inside the overlay closes the overlay instead of killing the app.
	if m.mode == viewModeDiffWindow {
		return m.handleDiffWindowKey(msg)
	}
	if m.mode == viewModeWorktreeAddInput {
		return m.handleWorktreeAddInputKey(msg)
	}
	if m.mode == viewModeWorktreeRemoveConfirm {
		return m.handleWorktreeRemoveConfirmKey(msg)
	}
	if m.mode == viewModeBranchPicker {
		return m.handleBranchPickerKey(msg)
	}
	if m.mode == viewModeRefDeleteConfirm {
		return m.handleRefDeleteConfirmKey(msg)
	}
	if m.mode == viewModeRebaseConfirm {
		return m.handleRebaseConfirmKey(msg)
	}
	if m.mode == viewModeCherryPickConfirm {
		return m.handleCherryPickConfirmKey(msg)
	}
	if m.mode == viewModeRevertConfirm {
		return m.handleRevertConfirmKey(msg)
	}
	if m.mode == viewModeResetConfirm {
		return m.handleResetConfirmKey(msg)
	}
	if m.mode == viewModeBranchCreateInput {
		return m.handleBranchCreateInputKey(msg)
	}
	if m.mode == viewModeBranchesModal {
		return m.handleBranchesModalKey(msg)
	}
	if m.mode == viewModeWorktreesModal {
		return m.handleWorktreesModalKey(msg)
	}
	if m.mode == viewModePRsModal {
		return m.handlePRsModalKey(msg)
	}
	if m.mode == viewModeZombieCleanupConfirm {
		return m.handleZombieCleanupConfirmKey(msg)
	}
	if m.mode == viewModeLocalChanges {
		return m.handleLocalChangesKey(msg)
	}
	if m.mode == viewModeCheckoutConfirm {
		return m.handleCheckoutConfirmKey(msg)
	}
	return m.handleNormalKey(msg)
}

func (m Model) handleDiffWindowKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// PR review sub-states gate the overlay's scroll keymap. Only reached
	// when a PR review opened the overlay (reviewPRNumber != 0); a plain
	// commit patch (reviewPRNumber == 0) falls straight through to scroll/close below.
	if m.reviewPRNumber != 0 {
		if m.prReviewInFlight {
			// Mid approve/merge — swallow everything but quit so a second
			// press can't fork a parallel gh call.
			if msg.String() == "ctrl+c" {
				return m.handleCtrlC()
			}
			return m, nil
		}
		// Any key dismisses a lingering result notice (approve ok / failed).
		m.prReviewNotice = ""
		switch m.prAction {
		case prActionApprove:
			switch msg.String() {
			case "y":
				return m.dispatchPRApprove()
			case "q", "esc":
				m.prAction = prActionNone
				return m, nil
			case "ctrl+c":
				return m.handleCtrlC()
			}
			return m, nil
		case prActionMerge:
			switch msg.String() {
			case "s":
				return m.dispatchPRMerge("squash")
			case "m":
				return m.dispatchPRMerge("merge")
			case "r":
				return m.dispatchPRMerge("rebase")
			case "q", "esc":
				m.prAction = prActionNone
				return m, nil
			case "ctrl+c":
				return m.handleCtrlC()
			}
			return m, nil
		case prActionComment, prActionRequestChanges:
			switch msg.String() {
			case "ctrl+s":
				return m.dispatchPRReviewBody()
			case "esc":
				m.prAction = prActionNone
				m.prReviewBody = textarea.Model{}
				m.prReviewBodyErr = ""
				return m, nil
			case "ctrl+c":
				return m.handleCtrlC()
			}
			// Everything else is body text — forward to the editor (`a`/`m`/
			// `c`/`r` etc. type literally, not re-arm actions).
			var cmd tea.Cmd
			m.prReviewBody, cmd = m.prReviewBody.Update(msg)
			return m, cmd
		}
		// Browse state: arm the inline confirms; everything else (scroll,
		// file jump, close) falls through to the shared keymap below.
		switch msg.String() {
		case "a":
			m.prAction = prActionApprove
			return m, nil
		case "m":
			m.prAction = prActionMerge
			return m, nil
		case "c":
			return m.beginPRReviewBody(prActionComment)
		case "r":
			return m.beginPRReviewBody(prActionRequestChanges)
		}
	}
	switch msg.String() {
	case "left":
		// `←` climbs back out of the diff, the mirror of the `→` that opened
		// it. It is the sole exit now — q/esc no longer close, matching the
		// arrow-only navigation the local-changes diff uses.
		m.mode = m.reviewReturnMode
		m.reviewReturnMode = viewModeNormal
		m.diff.ClosePatch()
		m.reviewPRNumber = 0
		m.prAction = prActionNone
		m.prReviewNotice = ""
		m.prReviewBody = textarea.Model{}
		m.prReviewBodyErr = ""
		// Resize the page we're returning to: if `?` is open its panel height
		// differs from the diff's (Graph/Sync vs Diff columns), so the graph
		// box must be recomputed or it overflows and clips the top rows.
		m.applyPaneSizes()
		return m, nil
	case "?":
		// Inline help panel, same toggle every page uses. The diff bottom
		// line expands into the Diff (+ PR Review) key columns.
		return m.toggleHelp()
	case "tab":
		// Global page cycle, like every other page — close the diff and
		// advance to the next page from the one it sits on.
		return m.cycleDiffPage(false)
	case "shift+tab":
		return m.cycleDiffPage(true)
	case "ctrl+c":
		return m.handleCtrlC()
	case "j", "k", "down", "up", "pgdown", "pgup":
		return m, m.diff.ScrollPatch(msg)
	case "]":
		m.diff.MoveHunk(1)
		return m, nil
	case "[":
		m.diff.MoveHunk(-1)
		return m, nil
	case "}":
		m.diff.JumpToNextFile()
		return m, nil
	case "{":
		m.diff.JumpToPrevFile()
		return m, nil
	}
	return m, nil
}

// cycleDiffPage closes the diff and lands on the next (or previous) top-level
// page, so tab/shift+tab work from inside the diff the same as from any page.
// The diff's own page index (commit → graph, PR review → worktree) is the
// cycle origin.
func (m Model) cycleDiffPage(back bool) (tea.Model, tea.Cmd) {
	idx := m.currentPageIndex()
	m.diff.ClosePatch()
	m.reviewReturnMode = viewModeNormal
	m.reviewPRNumber = 0
	m.prAction = prActionNone
	m.prReviewNotice = ""
	m.prReviewBody = textarea.Model{}
	m.prReviewBodyErr = ""
	delta := 1
	if back {
		delta = -1
	}
	switch (idx + delta + 3) % 3 {
	case 1:
		return m.beginWorktreesModal()
	case 2:
		return m, m.enterLocalChangesMode()
	default:
		return m.enterGraphPage(), nil
	}
}

func (m Model) handleWorktreeAddInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		// Sub-modals open from the worktrees modal, so every exit path
		// lands back there (the dashboard-era surface continuity).
		m.mode = viewModeWorktreesModal
		m.worktreeAction.addInput = textinput.Model{}
		m.worktreeAction.addInlineErr = ""
		return m, nil
	case "ctrl+c":
		return m.handleCtrlC()
	case "enter":
		if m.worktreeAction.actionInFlight {
			return m, nil
		}
		branch := strings.TrimSpace(m.worktreeAction.addInput.Value())
		if branch == "" {
			m.worktreeAction.addInlineErr = "branch name is empty"
			return m, nil
		}
		path := deriveAddPath(m.workdir, branch)
		m.worktreeAction.actionInFlight = true
		m.worktreeAction.pendingAddPath = path
		m.worktreeAction.addInlineErr = ""
		return m, worktreeAddCmd(m.workdir, path, branch, m.worktreeAction.reqID)
	}
	var cmd tea.Cmd
	m.worktreeAction.addInput, cmd = m.worktreeAction.addInput.Update(msg)
	return m, cmd
}

func (m Model) handleWorktreeRemoveConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.worktreeAction.actionInFlight {
		if msg.String() == "ctrl+c" {
			return m.handleCtrlC()
		}
		return m, nil
	}
	t := m.worktreeAction.removeTarget
	isDirty := m.refs.WorktreeDirty(t.Path)
	isLocked := t.Locked
	needsForce := isDirty || isLocked
	switch msg.String() {
	case "q", "esc":
		m.mode = viewModeWorktreesModal
		m.worktreeAction.removeTarget = git.Worktree{}
		return m, nil
	case "ctrl+c":
		return m.handleCtrlC()
	case "y":
		if needsForce {
			m.mode = viewModeWorktreesModal
			m.worktreeAction.removeTarget = git.Worktree{}
			reason := "dirty"
			if isLocked && !isDirty {
				reason = "locked"
			}
			m.status = "remove: cancelled (" + reason + " — use [Y] to force)"
			m.statusStyle = statusOkS
			return m, nil
		}
		m.worktreeAction.actionInFlight = true
		return m, worktreeRemoveCmd(m.workdir, t.Path, false, m.worktreeAction.reqID)
	case "Y":
		if !needsForce {
			return m, nil
		}
		m.worktreeAction.actionInFlight = true
		return m, worktreeRemoveCmd(m.workdir, t.Path, true, m.worktreeAction.reqID)
	}
	return m, nil
}

func (m Model) handleBranchPickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		if m.branchPicker.cursor < len(m.branchPicker.candidates)-1 {
			m.branchPicker.cursor++
			m.branchPicker.scrollIntoView(branchPickerVisibleRows(m.height, len(m.branchPicker.candidates)))
		}
		return m, nil
	case "k", "up":
		if m.branchPicker.cursor > 0 {
			m.branchPicker.cursor--
			m.branchPicker.scrollIntoView(branchPickerVisibleRows(m.height, len(m.branchPicker.candidates)))
		}
		return m, nil
	case "enter":
		if len(m.branchPicker.candidates) == 0 {
			return m, nil
		}
		branch := m.branchPicker.candidates[m.branchPicker.cursor]
		m.branchPicker = branchPickerState{}
		m.mode = viewModeNormal
		var cmd tea.Cmd
		m, cmd = m.beginCheckout(branch, false)
		return m, cmd
	case "q", "esc":
		m.mode = viewModeNormal
		m.branchPicker = branchPickerState{}
		m.status = "branch select cancelled"
		m.statusStyle = statusOkS
		return m, nil
	case "ctrl+c":
		return m.handleCtrlC()
	}
	return m, nil
}

func (m Model) handleRefDeleteConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.refActionInFlight {
		if msg.String() == "ctrl+c" {
			return m.handleCtrlC()
		}
		return m, nil
	}
	switch msg.String() {
	case "q", "esc":
		m.mode = viewModeNormal
		m.pendingRefDelete = refDeleteState{}
		m.status = "delete: cancelled"
		m.statusStyle = statusOkS
		return m, nil
	case "ctrl+c":
		return m.handleCtrlC()
	case "y":
		return m.dispatchRefDelete(false)
	case "Y":
		return m.dispatchRefDelete(true)
	}
	return m, nil
}

func (m Model) handleBranchesModalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		return m.branchesModalMoveCursor(1), nil
	case "k", "up":
		return m.branchesModalMoveCursor(-1), nil
	case "d":
		return m.beginBranchesModalDelete()
	case "q", "esc":
		m.mode = viewModeNormal
		m.branchesModal = branchesModalState{}
		return m, nil
	case "ctrl+c":
		return m.handleCtrlC()
	}
	return m, nil
}

func (m Model) handleWorktreesModalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		return m.worktreesModalMoveCursor(1), nil
	case "k", "up":
		return m.worktreesModalMoveCursor(-1), nil
	case " ", "space":
		// `space` switches to the cursor worktree — the action `enter`
		// carried before review PR took the enter slot, mirroring the graph
		// page's space/enter split.
		return m.worktreesModalEnter()
	case "enter":
		// `enter` opens the cursor worktree's open PR in the inline review
		// overlay (was `O`), keeping graph and worktree key models aligned.
		return m.worktreesModalReviewPR()
	case "a":
		return m.beginWorktreeAdd()
	case "d":
		return m.worktreesModalRemove()
	case "s":
		return m.worktreesModalToggleSort(), nil
	case "tab":
		// Page cycle: worktree → local changes. State is preserved (not
		// zeroed) so the sort preference carries across the switch.
		cmd := m.enterLocalChangesMode()
		return m, cmd
	case "shift+tab":
		// Page cycle: worktree → graph (home).
		return m.enterGraphPage(), nil
	case "?":
		// Inline help on the worktree page (Global + Worktree categories).
		return m.toggleHelp()
	case "ctrl+c":
		return m.handleCtrlC()
	}
	return m, nil
}

func (m Model) handlePRsModalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		return m.prsModalMoveCursor(1), nil
	case "k", "up":
		return m.prsModalMoveCursor(-1), nil
	case "enter":
		return m.prsModalEnter()
	case "l", "q", "esc":
		// `l` toggles the modal closed, mirroring how it opens.
		m.mode = viewModeNormal
		m.prsModal = prsModalState{}
		return m, nil
	case "ctrl+c":
		return m.handleCtrlC()
	}
	return m, nil
}

func (m Model) handleZombieCleanupConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// While the bulk-delete cmd is in flight, only ctrl+c (quit)
	// is honored so a second y/Y can't fork a parallel sweep.
	if m.zombieInFlight {
		if msg.String() == "ctrl+c" {
			return m.handleCtrlC()
		}
		return m, nil
	}
	switch msg.String() {
	case "q", "esc":
		m.mode = viewModeNormal
		m.zombieCleanup = zombieCleanupState{}
		m.status = "zombie cleanup: aborted"
		m.statusStyle = statusOkS
		return m, nil
	case "ctrl+c":
		return m.handleCtrlC()
	case "y", "Y":
		m.zombieInFlight = true
		m.setBusyStatus(fmt.Sprintf("deleting %d zombie branches…", len(m.zombieCleanup.branches)))
		return m, deleteZombieBranchesCmd(m.workdir, m.zombieCleanup.branches)
	}
	return m, nil
}

func (m Model) handleLocalChangesKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m.handleCtrlC()
	case "tab":
		// Page cycle: local changes → graph (home). Releases the diff body.
		return m.enterGraphPage(), nil
	case "shift+tab":
		// Page cycle: local changes → worktree.
		m.localChanges.ClosePatch()
		return m.beginWorktreesModal()
	case "?":
		// Inline help on the local changes page itself — no longer yanks to
		// the graph. Shows the Global + Local Changes categories.
		return m.toggleHelp()
	case "r":
		return m, loadStatusCmd(m.workdir)
	}
	// Single-pane drill-down. Tree owns cursor movement + stage/unstage;
	// `→` descends into the diff. Diff owns hunk navigation (`[`/`]`),
	// per-hunk staging (`space`), and viewport scroll; `←` climbs back to
	// the tree. Page navigation is the tab cycle — there is no esc/q exit.
	switch m.localChanges.Focused() {
	case paneLCTree:
		switch msg.String() {
		case "right":
			return m.enterLocalChangesDiff()
		}
		return m.handleLocalChangesTreeKey(msg)
	case paneLCDiff:
		switch msg.String() {
		case "left":
			m.localChanges.SetFocus(paneLCTree)
			return m, nil
		case "[":
			m.localChanges.MoveHunk(-1)
			return m, nil
		case "]":
			m.localChanges.MoveHunk(1)
			return m, nil
		case " ", "space":
			return m.dispatchLocalChangesStageHunk()
		}
		return m, m.localChanges.ScrollDiff(msg)
	}
	return m, nil
}

func (m Model) handleCheckoutConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "s":
		p := m.pendingCheckout
		m.mode = viewModeNormal
		// Record the branch the stash is taken on for the status suffix
		// and the return-time pop hint. Detached HEAD has no name — that
		// stash just skips the hints.
		for _, r := range m.refs.LocalRefs() {
			if r.IsHead {
				m.stashNotice = r.ShortName
				break
			}
		}
		// pendingCheckout stays armed — a dirty re-entry from the retry
		// (a write raced the stash) reopens the modal with it intact.
		switch {
		case p.withFF, p.withCheckoutFF:
			m.ffInFlight = true
			m.setBusyStatus("stash & fast-forward: " + p.ref + " …")
		default:
			m.checkoutInFlight = true
			m.setBusyStatus("stash & " + checkoutLabel(p.ref, p.detached) + " …")
		}
		return m, stashThenRetryCmd(m.workdir, p)
	case "a", "q", "esc":
		m.mode = viewModeNormal
		// Abort is where the pull-after chain dies — the needs-clean-tree
		// handlers keep it armed so `s` can carry the remote-chip Enter's
		// "synced with network" promise through the stash retry.
		m.pullAfterAction = false
		p := m.pendingCheckout
		m.pendingCheckout = pendingCheckout{}
		switch {
		case p.withFF, p.withCheckoutFF:
			m.status = "fast-forward: aborted"
		default:
			m.status = "checkout: aborted"
		}
		m.statusStyle = statusOkS
		// A re-entered modal (stash landed, retry hit dirty again) can be
		// aborted with the notice still armed — the changes really are in
		// the stash, so say so here instead of leaking the suffix onto a
		// later unrelated status.
		m = m.consumeStashNotice()
		return m, nil
	case "ctrl+c":
		return m.handleCtrlC()
	}
	return m, nil
}

func (m Model) handleNormalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m.handleCtrlC()
	case "?":
		// Toggle the inline help reference panel. It grows out of the footer
		// (not a modal) — other shortcuts keep working while it is open.
		return m.toggleHelp()
	case "F":
		if m.fetchInFlight {
			return m, nil
		}
		m.fetchInFlight = true
		m.lastFetchAt = time.Now()
		m.refs.SetLastFetchAt(m.lastFetchAt)
		m.setBusyStatus("fetching…")
		return m, fetchCmd(m.workdir)
	case "p":
		if m.pullInFlight {
			return m, nil
		}
		m.pullInFlight = true
		m.setBusyStatus("pulling…")
		return m, pullCmd(m.workdir, m.pullPrefStrategy)
	case "r":
		// Manual reload also refreshes the PR badges — unlike the internal
		// reloadCmd callers (watcher, post-checkout), `r` is the user
		// saying "show me current state". Deliberately status-silent:
		// reloads finish in tens of ms, so any busy/done status just
		// flickers; the in-place graph swap is the feedback.
		return m, tea.Batch(m.reloadCmd(), m.dispatchPRList())
	case "tab":
		// Page cycle: graph → worktree. `w` / `,` were retired in favor of
		// the single tab/shift+tab cycle (see renderPageTabs breadcrumb).
		return m.beginWorktreesModal()
	case "shift+tab":
		// Page cycle: graph → local changes (the reverse neighbor).
		cmd := m.enterLocalChangesMode()
		return m, cmd
	case "y":
		m = m.copyHashFromGraph()
		return m, nil
	case "R":
		// Rebase the current branch onto the cursor commit (confirm-first).
		return m.beginRebase()
	case "c":
		// Cherry-pick the cursor commit onto the current branch (confirm-first).
		return m.beginCherryPick()
	case "v":
		// Revert the cursor commit on the current branch (confirm-first).
		// History-preserving — the "scrap a shared commit" path.
		return m.beginRevert()
	case "x":
		// Reset the current branch to the cursor commit (confirm-first,
		// soft/mixed/hard). Pushed-history resets are refused → revert.
		return m.beginReset()
	case "n":
		// Create a branch at the cursor commit and switch to it.
		return m.beginBranchCreate()
	case "P":
		// Push the current branch (first push auto-sets upstream).
		return m.beginPush()
	case "l":
		// Open the PR list modal — every open PR, including ones whose head
		// branch isn't checked out locally (which the graph cursor can't
		// reach). enter opens the cursor PR in the same review overlay.
		return m.beginPRsModal()
	case "Z":
		// Zombie-branch cleanup is a global action now that the sidebar
		// is gone — the previous paneRefs focus gate had no meaningful
		// successor, and the bulk-delete is the same regardless of
		// which pane the user is on.
		if m.zombieInFlight {
			return m, nil
		}
		m.zombieInFlight = true
		m.setBusyStatus("scanning for zombie branches…")
		return m, detectZombieBranchesCmd(m.workdir)
	case " ", "space":
		// `space` runs the checkout / ff / detach evaluator on the cursor
		// row — the action `enter` carried before PR review took the enter
		// slot. Graph is the only focused pane. The sidebar was retired in
		// PR B2; the bottom tab pane was retired with the subtract-
		// bottom-pane change. Worktree + Local Changes are now sibling
		// pages reached via the tab/shift+tab cycle, not `w` / `,`.
		if m.gitMutationInFlight() {
			return m, nil
		}
		// During a stale-while-revalidate window the visible rows are the
		// old graph — evaluating a checkout/FF against them could act on
		// state the in-flight reload is about to replace. Drop the key for
		// the sub-second window, like the old hard-reset (unloaded graph,
		// Selected() !ok) used to.
		if m.graph.pendingSwap {
			return m, nil
		}
		c, ok := m.graph.Selected()
		if !ok {
			return m, nil
		}
		locals := m.refs.LocalRefs()
		if len(locals) == 0 {
			m.status = "refs not loaded yet"
			m.statusStyle = statusErrS
			return m, nil
		}
		remotes := m.refs.RemoteRefs()
		m.actionInFlight = true
		m.setBusyStatus("→ resolving…")
		log.Printf("graph space: dispatch evaluator (cursor=%s, locals=%d, remotes=%d)",
			shortHash(c.Hash), len(locals), len(remotes))
		return m, evaluateGraphActionCmd(m.workdir, c.Hash, locals, remotes)
	case "enter":
		// `enter` pulls the cursor row's open PR into the inline review
		// overlay (the action `O` carried before). Graph stays the return
		// mode so merging drops back here, not to the worktree page. Rows
		// without a PR-bearing chip report on the status line.
		m.reviewReturnMode = viewModeNormal
		return m.beginPRReview()
	case "right":
		// `→` opens the patch overlay for the focused commit (`←` closes it
		// from inside — see handleDiffWindowKey), mirroring the local-changes
		// tree→diff drill-down. Page paging moved to `[`/`]` to free the arrows.
		c, ok := m.graph.Selected()
		if !ok {
			return m, nil
		}
		m.diffReqID++
		m.diff.BeginPatchLoad(c.Hash, m.diffReqID)
		m.mode = viewModeDiffWindow
		m.applyPaneSizes() // box-inner size now that the diff lives in the page
		return m, loadDiffPatchCmd(m.workdir, c.Hash, m.diffReqID)
	case "b":
		// Branches modal — local-branch list with cursor + `d` delete
		// entry. Global, independent of focused pane.
		return m.beginBranchesModal()
	}
	var cmd tea.Cmd
	m.graph, cmd = m.graph.Update(msg)
	return m, cmd
}
