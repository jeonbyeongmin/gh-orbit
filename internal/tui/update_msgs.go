// Data-message dispatch for Model.Update, extracted out of the giant
// type switch and grouped by domain. Each update*Msg method owns one
// domain's message types; the bodies moved here verbatim. NOTE: a new
// message type added to a group's switch must also be registered in the
// matching dispatch case inside Model.Update, or it will never fire.
package tui

import (
	"fmt"
	"log"
	"path/filepath"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// updateWorktreeMsg handles the sidebar worktree domain: inventory
// loads, fsnotify-driven refreshes, dirty polling, and add/remove
// action replies.
func (m Model) updateWorktreeMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case switchWorktreeMsg:
		next, cmd := m.switchWorktree(msg.path)
		return next, cmd

	case worktreesLoadedMsg:
		// Drop stale loads (a switch or another reload bumped the reqID
		// between dispatch and reply).
		if msg.reqID != m.sidebarWorktreesReqID {
			return m, nil
		}
		m.refs.SetWorktrees(msg.entries, m.workdir)
		// While the modal is open, keep its cursor valid against the fresh
		// inventory: land on a just-added entry (pendingAddPath), then clamp
		// against shrink so the highlight can never point past the list.
		if m.mode == viewModeWorktreesModal {
			wts := m.modalWorktrees()
			if p := m.worktreeAction.pendingAddPath; p != "" {
				for i, wt := range wts {
					if wt.Path == p {
						m.worktreesModal.cursor = i
						break
					}
				}
			}
			if m.worktreesModal.cursor >= len(wts) {
				m.worktreesModal.cursor = len(wts) - 1
			}
			if m.worktreesModal.cursor < 0 {
				m.worktreesModal.cursor = 0
			}
		}
		m.worktreeAction.pendingAddPath = ""
		paths := make([]string, 0, len(msg.entries))
		for _, e := range msg.entries {
			paths = append(paths, e.Path)
		}
		// Reconcile the external-change watcher with the fresh inventory.
		// add/remove/switch all funnel through this case, so Sync sees every
		// gitDir set change. nil-safe for the silent-degrade path.
		m.watcher.Sync(msg.entries)
		return m, worktreeDirtyFanoutCmd(m.sidebarWorktreesReqID, paths)

	case worktreeWatchedChangeMsg:
		// fsnotify saw HEAD or index settle on a watched worktree. Always
		// refresh the inventory so the row's branch / dirty marker tracks
		// the new state. If the event hit the current worktree, also fire
		// reloadCmd so graph + refs stay coherent — an external commit on
		// the tree we're viewing must surface as a new graph row, not just
		// a relabeled modal row. reloadCmd bumps reqID a second time,
		// which only burns one generation (stale-drop logic is reqID-equal,
		// not monotonic).
		m.sidebarWorktreesReqID++
		cmds := []tea.Cmd{loadWorktreesCmd(m.workdir, m.sidebarWorktreesReqID)}
		if msg.path == m.workdir {
			cmds = append(cmds, m.reloadCmd())
		}
		return m, tea.Batch(cmds...)

	case worktreesLoadFailedMsg:
		if msg.reqID != m.sidebarWorktreesReqID {
			return m, nil
		}
		// Soft-fail: the sidebar already shows whatever the previous load
		// produced. Surface the error on the status bar so the user knows
		// the inventory may be stale.
		m.status = "worktrees: " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil

	case worktreeDirtyResultMsg:
		if msg.reqID != m.sidebarWorktreesReqID {
			return m, nil
		}
		m.refs.SetWorktreeDirty(msg.path, msg.dirty, msg.timedOut)
		m.refs.SetWorktreeLastCommit(msg.path, msg.subject, msg.when)
		return m, nil

	case worktreeAddSucceededMsg:
		if msg.reqID != m.worktreeAction.reqID {
			return m, nil
		}
		m.worktreeAction.actionInFlight = false
		m.worktreeAction.addInput = textinput.Model{}
		m.worktreeAction.addInlineErr = ""
		// Return to the worktrees modal — the sub-modal was opened from it,
		// and the surface continuity (pick next action from the same list)
		// is the dashboard-era behavior this modal inherits. The cursor
		// lands on the new entry once the reload below delivers it.
		m.mode = viewModeWorktreesModal
		m.status = "worktree added: " + msg.branch + " → " + filepath.Base(msg.path)
		m.statusStyle = statusOkS
		m.sidebarWorktreesReqID++
		return m, loadWorktreesCmd(m.workdir, m.sidebarWorktreesReqID)

	case worktreeAddFailedMsg:
		if msg.reqID != m.worktreeAction.reqID {
			return m, nil
		}
		m.worktreeAction.actionInFlight = false
		m.worktreeAction.pendingAddPath = ""
		m.worktreeAction.addInlineErr = firstLine(msg.err.Error())
		// Stay in viewModeWorktreeAddInput so the user can fix the input.
		return m, nil

	case worktreeRemoveSucceededMsg:
		if msg.reqID != m.worktreeAction.reqID {
			return m, nil
		}
		m.worktreeAction.actionInFlight = false
		m.worktreeAction.removeTarget = git.Worktree{}
		m.mode = viewModeWorktreesModal
		m.status = "worktree removed: " + filepath.Base(msg.path)
		m.statusStyle = statusOkS
		m.sidebarWorktreesReqID++
		return m, loadWorktreesCmd(m.workdir, m.sidebarWorktreesReqID)

	case worktreeRemoveFailedMsg:
		if msg.reqID != m.worktreeAction.reqID {
			return m, nil
		}
		m.worktreeAction.actionInFlight = false
		m.worktreeAction.removeTarget = git.Worktree{}
		m.mode = viewModeWorktreesModal
		m.status = "remove: " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil
	}
	return m, nil
}

// updateCommitsMsg handles commit-data loads: the streamed git log,
// HEAD ancestry, refs, and the `d` patch overlay body.
func (m Model) updateCommitsMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case commitsStreamStartedMsg:
		if msg.reqID != m.streamReqID {
			// Stale — its reload was superseded. Cancel the orphaned
			// producer immediately so its git process doesn't leak.
			if msg.cancel != nil {
				msg.cancel()
			}
			return m, nil
		}
		m.streamCancel = msg.cancel
		var cmd tea.Cmd
		m.graph, cmd = m.graph.Update(msg)
		return m, cmd

	case commitsAppendedMsg:
		if msg.reqID != m.streamReqID {
			return m, nil
		}
		var cmd tea.Cmd
		m.graph, cmd = m.graph.Update(msg)
		// The jump target may have just streamed in — and on the swap
		// batch this is the first call where the visible rows are the NEW
		// window (tryHEADJump holds while pendingSwap keeps the old ones).
		m = m.tryHEADJump()
		return m, cmd

	case commitsStreamDoneMsg:
		if msg.reqID != m.streamReqID {
			return m, nil
		}
		m.streamCancel = nil
		var cmd tea.Cmd
		m.graph, cmd = m.graph.Update(msg)
		m = m.tryHEADJump()
		return m, cmd

	case headAncestorsLoadedMsg:
		if msg.reqID != m.streamReqID {
			// A reload superseded this dispatch; the new reqID's ancestry
			// is already in flight (or already landed).
			return m, nil
		}
		if msg.err != nil {
			// Fall back to position-based dim — the boundary still reads,
			// just without ancestor protection. Surface in the log; don't
			// noise the status bar with a niche error.
			log.Printf("git rev-list HEAD: %v", msg.err)
			return m, nil
		}
		m.graph.SetHeadAncestors(msg.ancestors)
		return m, nil

	case refsLoadedMsg, refsLoadFailedMsg:
		if loaded, ok := msg.(refsLoadedMsg); ok && m.pendingHEADHash == pendingHEADSentinel {
			for _, r := range loaded.refs {
				if r.IsHead {
					m.pendingHEADHash = r.ObjectName
					break
				}
			}
			m = m.tryHEADJump()
		}
		var cmd tea.Cmd
		m.refs, cmd = m.refs.Update(msg)
		return m, cmd

	case diffPatchLoadedMsg:
		m.diff.ApplyPatchLoaded(msg.reqID, msg.hash, msg.text)
		return m, nil

	case diffPatchFailedMsg:
		m.diff.ApplyPatchFailed(msg.reqID, msg.hash, msg.err)
		return m, nil
	}
	return m, nil
}

// updateCheckoutMsg handles graph-action outcomes: the Enter evaluator
// chain (checkout / fast-forward, including the needs-clean-tree detours
// into viewModeCheckoutConfirm) plus the rebase / cherry-pick /
// branch-create / push / browse replies.
func (m Model) updateCheckoutMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case checkoutSucceededMsg:
		m.checkoutInFlight = false
		m.pendingCheckout = pendingCheckout{}
		m.status = checkoutLabel(msg.ref, msg.detached)
		m.statusStyle = statusOkS
		m = m.consumeStashNotice()
		// Return-time pop hint: landing on a branch stash-and-continue
		// once left changes on. Detached refs are hashes — never in the
		// map, so no detached guard is needed.
		if m.stashedRefs[msg.ref] {
			m.status += " · stashed changes here — git stash pop"
		}
		m.pendingHEADHash = pendingHEADSentinel
		var pull tea.Cmd
		var chained bool
		m, pull, chained = m.chainPullAfterAction(m.status)
		if chained {
			return m, pull
		}
		return m, m.reloadCmd()

	case checkoutNeedsCleanTreeMsg:
		// Modal owns the next decision; release the in-flight gate.
		// pendingCheckout stays intact so the modal hint can name the
		// chain that was about to run. pullAfterAction also stays armed —
		// the modal's `s` continues the chain, abort clears it.
		m.checkoutInFlight = false
		m.mode = viewModeCheckoutConfirm
		m.status = "checkout: " + msg.ref + " — uncommitted changes"
		m.statusStyle = statusErrS
		return m, nil

	case checkoutFailedMsg:
		m.pullAfterAction = false
		m.checkoutInFlight = false
		m.pendingCheckout = pendingCheckout{}
		m.status = "checkout failed: " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		// A stash that landed before the retry failed must still be
		// named — the changes are gone from the tree either way.
		m = m.consumeStashNotice()
		return m, nil

	case stashFailedMsg:
		// The stash step itself failed: nothing was stashed, nothing was
		// retried. Kill the whole chain — including the pull-after arm
		// the modal carried through.
		m.pullAfterAction = false
		m.checkoutInFlight = false
		m.ffInFlight = false
		m.pendingCheckout = pendingCheckout{}
		m.stashNotice = ""
		m.status = "stash failed: " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil

	case graphActionMsg:
		m.actionInFlight = false
		// Every evaluation re-arms or clears the pull-after chain; a stale
		// flag from a dropped earlier dispatch must not leak into this one.
		m.pullAfterAction = false
		// Stale-drop: cursor moved between Enter dispatch and this reply.
		// Drop silently — the user can re-press Enter on the new row.
		if c, ok := m.graph.Selected(); !ok || c.Hash != msg.hash {
			m.status = ""
			return m, nil
		}
		switch msg.kind {
		case graphActionNoOp:
			m.status = "already on " + msg.branch
			m.statusStyle = statusOkS
			return m, nil
		case graphActionCheckout:
			m.pullAfterAction = msg.pullAfter
			var cmd tea.Cmd
			m, cmd = m.beginCheckout(msg.branch, false)
			return m, cmd
		case graphActionPicker:
			m.mode = viewModeBranchPicker
			m.branchPicker = branchPickerState{
				candidates: msg.candidates,
				hash:       msg.hash,
			}
			m.status = fmt.Sprintf("branch select: %d candidates", len(msg.candidates))
			m.statusStyle = statusBusyS
			return m, nil
		case graphActionFF:
			m.pullAfterAction = msg.pullAfter
			m.ffInFlight = true
			m.setBusyStatus(ffLabel(msg.branch, msg.advance) + " …")
			return m, ffOnlyCmd(m.workdir, msg.branch, msg.hash)
		case graphActionCheckoutAndFF:
			m.pullAfterAction = msg.pullAfter
			m.ffInFlight = true
			m.setBusyStatus("fast-forward: " + msg.branch + " (checkout + ff) …")
			return m, checkoutThenFFCmd(m.workdir, msg.branch, msg.hash)
		case graphActionDetach:
			var cmd tea.Cmd
			m, cmd = m.beginCheckout(msg.hash, true)
			return m, cmd
		}
		return m, nil

	case ffSucceededMsg:
		m.ffInFlight = false
		// The stash retry path reaches here with pendingCheckout still
		// armed (the modal's `s` keeps it for dirty re-entries) — drop it.
		m.pendingCheckout = pendingCheckout{}
		m.status = ffLabel(msg.branch, msg.advance)
		m.statusStyle = statusOkS
		m = m.consumeStashNotice()
		m.pendingHEADHash = pendingHEADSentinel
		var pull tea.Cmd
		var chained bool
		m, pull, chained = m.chainPullAfterAction(m.status)
		if chained {
			return m, pull
		}
		return m, m.reloadCmd()

	case ffFailedMsg:
		m.pullAfterAction = false
		m.ffInFlight = false
		m.pendingCheckout = pendingCheckout{}
		log.Printf("graph enter: ff failed: %v", msg.err)
		m.status = "fast-forward failed: " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		m = m.consumeStashNotice()
		return m, nil

	case ffNeedsCleanTreeMsg:
		m.ffInFlight = false
		m.pendingCheckout = pendingCheckout{
			ref:    msg.branch,
			withFF: true,
			ffHash: msg.hash,
		}
		m.mode = viewModeCheckoutConfirm
		m.status = "fast-forward: " + msg.branch + " — uncommitted changes"
		m.statusStyle = statusErrS
		return m, nil

	case checkoutThenFFSucceededMsg:
		m.ffInFlight = false
		m.pendingCheckout = pendingCheckout{}
		m.status = ffLabel(msg.branch, msg.advance) + " (after checkout)"
		m.statusStyle = statusOkS
		m = m.consumeStashNotice()
		if m.stashedRefs[msg.branch] {
			m.status += " · stashed changes here — git stash pop"
		}
		m.pendingHEADHash = pendingHEADSentinel
		var pull tea.Cmd
		var chained bool
		m, pull, chained = m.chainPullAfterAction(m.status)
		if chained {
			return m, pull
		}
		return m, m.reloadCmd()

	case ffCheckoutNeedsCleanTreeMsg:
		m.ffInFlight = false
		m.pendingCheckout = pendingCheckout{
			ref:            msg.branch,
			withCheckoutFF: true,
			ffHash:         msg.hash,
		}
		m.mode = viewModeCheckoutConfirm
		m.status = "checkout+fast-forward: " + msg.branch + " — uncommitted changes"
		m.statusStyle = statusErrS
		return m, nil

	case rebaseSucceededMsg:
		p := m.pendingRebase
		m.rebaseInFlight = false
		m.pendingRebase = pendingRebase{}
		m.status = "rebase: done (" + p.branch + " onto " + p.label + ")"
		m.statusStyle = statusOkS
		m.pendingHEADHash = pendingHEADSentinel
		return m, m.reloadCmd()

	case rebaseConflictMsg:
		m.rebaseInFlight = false
		m.pendingRebase = pendingRebase{}
		m.status = "rebase: CONFLICT — resolve in your terminal"
		m.statusStyle = statusErrS
		// Reload so the graph reflects the mid-rebase state. No HEAD jump —
		// the user is mid-conflict (mirrors the pull-conflict handler).
		return m, m.reloadCmd()

	case rebaseFailedMsg:
		m.rebaseInFlight = false
		m.pendingRebase = pendingRebase{}
		m.status = "rebase failed: " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil

	case cherryPickSucceededMsg:
		p := m.pendingCherryPick
		m.cherryPickInFlight = false
		m.pendingCherryPick = pendingCherryPick{}
		m.status = "cherry-pick: done (" + shortHash(p.hash) + " onto " + p.branch + ")"
		m.statusStyle = statusOkS
		m.pendingHEADHash = pendingHEADSentinel
		return m, m.reloadCmd()

	case cherryPickConflictMsg:
		m.cherryPickInFlight = false
		m.pendingCherryPick = pendingCherryPick{}
		m.status = "cherry-pick: CONFLICT — resolve in your terminal"
		m.statusStyle = statusErrS
		return m, m.reloadCmd()

	case cherryPickFailedMsg:
		m.cherryPickInFlight = false
		m.pendingCherryPick = pendingCherryPick{}
		m.status = "cherry-pick failed: " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil

	case revertSucceededMsg:
		p := m.pendingRevert
		m.revertInFlight = false
		m.pendingRevert = pendingRevert{}
		m.status = "revert: done (" + shortHash(p.hash) + " on " + p.branch + ")"
		m.statusStyle = statusOkS
		m.pendingHEADHash = pendingHEADSentinel
		return m, m.reloadCmd()

	case revertConflictMsg:
		m.revertInFlight = false
		m.pendingRevert = pendingRevert{}
		m.status = "revert: CONFLICT — resolve in your terminal"
		m.statusStyle = statusErrS
		return m, m.reloadCmd()

	case revertFailedMsg:
		m.revertInFlight = false
		m.pendingRevert = pendingRevert{}
		m.status = "revert failed: " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil

	case resetEvalMsg:
		m.resetInFlight = false
		if !msg.ok {
			m.status = msg.err
			m.statusStyle = statusErrS
			return m, nil
		}
		m.pendingReset = pendingReset{target: msg.target, label: msg.label, branch: msg.branch, discard: msg.discard}
		m.mode = viewModeResetConfirm
		m.status = ""
		return m, nil

	case resetSucceededMsg:
		m.resetInFlight = false
		suffix := ""
		if msg.discard > 0 {
			suffix = fmt.Sprintf(" -%d", msg.discard)
		}
		m.status = "reset: " + msg.branch + " → " + msg.label + " (" + msg.mode + suffix + ")"
		m.statusStyle = statusOkS
		m.pendingHEADHash = pendingHEADSentinel
		return m, m.reloadCmd()

	case resetFailedMsg:
		m.resetInFlight = false
		m.status = "reset failed: " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil

	case branchCreateSucceededMsg:
		if msg.reqID != m.branchCreate.reqID {
			return m, nil
		}
		m.branchCreate.inFlight = false
		m.branchCreate.input = textinput.Model{}
		m.branchCreate.inlineErr = ""
		m.mode = viewModeNormal
		m.status = "branch created: " + msg.name
		m.statusStyle = statusOkS
		m.pendingHEADHash = pendingHEADSentinel
		return m, m.reloadCmd()

	case branchCreateFailedMsg:
		if msg.reqID != m.branchCreate.reqID {
			return m, nil
		}
		m.branchCreate.inFlight = false
		m.branchCreate.inlineErr = firstLine(msg.err.Error())
		// Stay in viewModeBranchCreateInput so the user can fix the name.
		return m, nil

	case pushSucceededMsg:
		m.pushInFlight = false
		m.status = "push: done (" + msg.branch + ")"
		m.statusStyle = statusOkS
		return m, m.reloadCmd()

	case pushFailedMsg:
		m.pushInFlight = false
		m.status = "push failed: " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil

	case browseFailedMsg:
		m.status = "browse failed: " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil

	case prBrowseOpenedMsg:
		m.status = fmt.Sprintf("opened PR #%d on GitHub", msg.number)
		m.statusStyle = statusOkS
		return m, nil
	}
	return m, nil
}

// updateFetchPullMsg handles network sync: focus-triggered + manual
// fetch, and pull outcomes.
func (m Model) updateFetchPullMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.FocusMsg:
		// xterm mode 1004 / TTY focus event: refresh the refs view in the
		// background so the cockpit sees PRs / merges that landed while the
		// window was inactive. fetchInFlight gate avoids piling on a manual
		// `F`; the throttle gate keeps an alt-tab burst from saturating the
		// network.
		if m.fetchInFlight {
			return m, nil
		}
		if !m.lastFetchAt.IsZero() && time.Since(m.lastFetchAt) < focusFetchThrottle {
			return m, nil
		}
		m.fetchInFlight = true
		m.lastFetchAt = time.Now()
		m.refs.SetLastFetchAt(m.lastFetchAt)
		return m, fetchCmd(m.workdir)

	case fetchSucceededMsg:
		m.fetchInFlight = false
		// Remote state just refreshed — re-pull the open-PR list on the
		// same beat so chip badges track what fetch saw.
		prCmd := m.dispatchPRList()
		// While a pull is still in flight, its "pulling…" status outranks
		// fetch's outcome and the pending pullSucceededMsg / pullConflictMsg
		// will reload. Skip status overwrite + the redundant reload. But if
		// F's own busy status is still painted (F pressed after p), hand
		// the line back to the pull so the spinner doesn't keep asserting
		// a fetch that just finished.
		if m.pullInFlight {
			if m.status == "fetching…" {
				m.setBusyStatus("pulling…")
			}
			return m, prCmd
		}
		m.status = "fetch: done"
		m.statusStyle = statusOkS
		return m, tea.Batch(m.reloadCmd(), prCmd)

	case fetchFailedMsg:
		m.fetchInFlight = false
		if m.pullInFlight {
			// Same busy-status handback as fetchSucceededMsg.
			if m.status == "fetching…" {
				m.setBusyStatus("pulling…")
			}
			return m, nil
		}
		m.status = "fetch failed: " + msg.err.Error()
		m.statusStyle = statusErrS
		return m, nil

	case pullSucceededMsg:
		m.pullInFlight = false
		m.status = "pull: done"
		m.statusStyle = statusOkS
		m.pendingHEADHash = pendingHEADSentinel
		// Pull's fetch leg refreshed remote state — same badge re-pull as
		// the fetchSucceededMsg path.
		return m, tea.Batch(m.reloadCmd(), m.dispatchPRList())

	case pullConflictMsg:
		m.pullInFlight = false
		m.status = "pull: CONFLICT — resolve in your terminal"
		m.statusStyle = statusErrS
		// Reload so refs (HEAD may now sit on a half-merged commit) and the
		// graph reflect post-pull state. No HEAD jump — user is mid-conflict.
		return m, m.reloadCmd()

	case pullFailedMsg:
		m.pullInFlight = false
		m.status = "pull failed: " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil

	case prsLoadedMsg:
		m.prsInFlight = false
		m.prs = msg.prs
		m.prList = msg.list
		m.graph.SetPRs(msg.prs)
		// A refresh can land while the `l` modal is open (a fetch/pull was
		// in flight when it opened) and return a shorter list — clamp the
		// cursor so it stays on a visible row instead of vanishing past the
		// end with enter dead-no-opping.
		if m.mode == viewModePRsModal && m.prsModal.cursor >= len(m.prList) {
			m.prsModal.cursor = len(m.prList) - 1
			if m.prsModal.cursor < 0 {
				m.prsModal.cursor = 0
			}
		}
		return m, nil

	case prsLoadFailedMsg:
		m.prsInFlight = false
		// Quiet on purpose: the badge is passive enrichment, and a repo
		// without a GitHub remote (or a logged-out gh) would otherwise
		// error-spam the status line on every refresh.
		log.Printf("pr list failed: %v", msg.err)
		return m, nil
	}
	return m, nil
}

// updateBranchOpsMsg handles branch lifecycle replies: single delete
// (refs pane / branches modal `d`) and bulk zombie cleanup (`Z`).
func (m Model) updateBranchOpsMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case branchDeleteSucceededMsg:
		m.refActionInFlight = false
		m.mode = viewModeNormal
		m.pendingRefDelete = refDeleteState{}
		if msg.forced {
			m.status = "deleted '" + msg.localName + "' (forced)"
		} else {
			m.status = "deleted '" + msg.localName + "'"
		}
		m.statusStyle = statusOkS
		return m, m.reloadCmd()

	case branchDeleteFailedMsg:
		m.refActionInFlight = false
		m.mode = viewModeNormal
		m.pendingRefDelete = refDeleteState{}
		m.status = "delete failed: " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil

	case branchDeleteNotMergedMsg:
		// Stay in viewModeRefDeleteConfirm so the user can press `Y` to
		// retry with -D. The dialog renders the not-merged row off
		// pendingRefDelete — not m.status, which mode-blind async
		// handlers could overwrite while the dialog is open.
		m.refActionInFlight = false
		m.pendingRefDelete.notMerged = true
		return m, nil

	case zombieDetectedMsg:
		m.zombieInFlight = false
		if len(msg.branches) == 0 {
			m.status = fmt.Sprintf("no zombie branches (merged into %s, upstream gone, not checked out)", msg.baseline)
			m.statusStyle = statusOkS
			return m, nil
		}
		m.zombieCleanup = zombieCleanupState(msg)
		m.mode = viewModeZombieCleanupConfirm
		m.status = ""
		return m, nil

	case zombieDetectFailedMsg:
		m.zombieInFlight = false
		m.status = "zombie scan: " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil

	case zombieDeletedMsg:
		m.zombieInFlight = false
		m.mode = viewModeNormal
		m.zombieCleanup = zombieCleanupState{}
		m.status, m.statusStyle = formatZombieSummary(msg.deleted, msg.failed)
		return m, m.reloadCmd()
	}
	return m, nil
}

// updateLocalChangesMsg handles the Local Changes view: status/diff
// loads, stage/unstage replies, and the sidebar summary.
func (m Model) updateLocalChangesMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case localChangesEnterRequestedMsg:
		cmd := m.enterLocalChangesMode()
		m.status = "local changes"
		m.statusStyle = statusOkS
		return m, cmd

	case localChangesStatusLoadedMsg:
		m.localChanges.ApplyStatusLoaded(msg.entries)
		// After reload, dispatch a diff for whatever the cursor now points
		// at so the right pane doesn't lag behind the tree.
		return m.dispatchLocalChangesDiff()

	case localChangesStatusFailedMsg:
		m.localChanges.ApplyStatusFailed(msg.err)
		m.status = "status: " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil

	case localChangesDiffLoadedMsg:
		m.localChanges.ApplyDiffLoaded(msg.reqID, msg.text)
		return m, nil

	case localChangesDiffFailedMsg:
		m.localChanges.ApplyDiffFailed(msg.reqID, msg.err)
		return m, nil

	case localChangesAddSucceededMsg:
		m.status = "staged " + msg.path
		m.statusStyle = statusOkS
		m.sidebarWorktreesReqID++
		return m, tea.Batch(
			loadStatusCmd(m.workdir),
			loadWorktreesCmd(m.workdir, m.sidebarWorktreesReqID),
		)

	case localChangesAddFailedMsg:
		m.status = "stage " + msg.path + ": " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil

	case localChangesRestoreSucceededMsg:
		m.status = "unstaged " + msg.path
		m.statusStyle = statusOkS
		m.sidebarWorktreesReqID++
		return m, tea.Batch(
			loadStatusCmd(m.workdir),
			loadWorktreesCmd(m.workdir, m.sidebarWorktreesReqID),
		)

	case localChangesRestoreFailedMsg:
		m.status = "unstage " + msg.path + ": " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil

	}
	return m, nil
}
