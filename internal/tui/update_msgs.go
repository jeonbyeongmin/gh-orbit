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
// loads, fsnotify-driven refreshes, dirty/agent-session polling, and
// add/remove action replies.
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
		// Dashboard height is data-driven on len(worktrees); going from
		// 0 → N (or N → 0) shrinks/grows the graph pane, so resize the
		// graph viewport now to keep the bubbles list in sync.
		m.applyPaneSizes()
		paths := make([]string, 0, len(msg.entries))
		for _, e := range msg.entries {
			paths = append(paths, e.Path)
		}
		// Reconcile the external-change watcher with the fresh inventory.
		// add/remove/switch all funnel through this case, so Sync sees every
		// gitDir set change. nil-safe for the silent-degrade path.
		m.watcher.Sync(msg.entries)
		// Event-driven agent poll alongside the dirty fan-out: every inventory
		// change (startup, add/remove, switch) lights the agent column now instead
		// of waiting up to one poll interval for the next tick. The 30s tick
		// stays as the ongoing refresh + freshness-aging loop.
		return m, tea.Batch(
			worktreeDirtyFanoutCmd(m.sidebarWorktreesReqID, paths),
			agentSessionPollCmd(agentSessionProjectsDir(), paths, time.Now(), m.sidebarWorktreesReqID),
		)

	case worktreeWatchedChangeMsg:
		// fsnotify saw HEAD or index settle on a watched worktree. Always
		// refresh the inventory so the row's branch / dirty marker tracks
		// the new state. If the event hit the current worktree, also fire
		// reloadCmd so graph + refs stay coherent — an external commit on
		// the tree we're viewing must surface as a new graph row, not just
		// a relabeled dashboard line. reloadCmd bumps reqID a second time,
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

	case agentSessionTickMsg:
		// Poll cadence fired. Stat the current worktree set off the main loop
		// and re-arm the next tick in the same batch — the tick handler is the
		// sole re-arm site, so the loop stays single-lineage and survives a
		// dropped (stale) poll reply. Reading Worktrees() scopes the poll to
		// the live inventory; the reqID lets the reply drop if it races a change.
		wts := m.refs.Worktrees()
		paths := make([]string, 0, len(wts))
		for _, wt := range wts {
			paths = append(paths, wt.Path)
		}
		return m, tea.Batch(
			agentSessionPollCmd(agentSessionProjectsDir(), paths, time.Now(), m.sidebarWorktreesReqID),
			agentSessionTickCmd(),
		)

	case agentSessionPollMsg:
		// Drop a poll that raced an inventory change (mirrors the
		// worktreeDirtyResultMsg reqID guard) so it can't re-insert a key for a
		// pruned worktree. No re-arm here — the tick handler owns that.
		if msg.reqID != m.sidebarWorktreesReqID {
			return m, nil
		}
		for path, state := range msg.states {
			m.refs.SetAgentState(path, state)
		}
		// Gated spinner start: if a worktree is now running and the spinner
		// lineage isn't already live, arm it. The tick handler owns stop + re-arm.
		if !m.spinnerTicking && m.refs.AnyAgentRunning() {
			m.spinnerTicking = true
			return m, agentSpinnerTickCmd()
		}
		return m, nil

	case agentSpinnerTickMsg:
		// Advance the frame and re-arm only while something is still running;
		// otherwise drop spinnerTicking and let the lineage die so the cockpit
		// goes idle (zero re-renders). The poll handler re-arms it next time a
		// worktree starts running.
		if !m.refs.AnyAgentRunning() {
			m.spinnerTicking = false
			return m, nil
		}
		m.spinnerFrame++
		return m, agentSpinnerTickCmd()

	case worktreeAddSucceededMsg:
		if msg.reqID != m.worktreeAction.reqID {
			return m, nil
		}
		m.worktreeAction.actionInFlight = false
		m.worktreeAction.addInput = textinput.Model{}
		m.worktreeAction.addInlineErr = ""
		m.mode = viewModeNormal
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
		m.mode = viewModeNormal
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
		m.mode = viewModeNormal
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

// updateCheckoutMsg handles the graph Enter action chain: evaluator
// replies and checkout / fast-forward outcomes (including the
// needs-clean-tree detours into viewModeCheckoutConfirm).
func (m Model) updateCheckoutMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case checkoutSucceededMsg:
		m.checkoutInFlight = false
		m.pendingCheckout = pendingCheckout{}
		m.status = checkoutLabel(msg.ref, msg.detached)
		m.statusStyle = statusOkS
		m.pendingHEADHash = pendingHEADSentinel
		return m, m.reloadCmd()

	case checkoutNeedsCleanTreeMsg:
		// Modal owns the next decision; release the in-flight gate.
		// pendingCheckout stays intact so the modal hint can name the
		// chain that was about to run.
		m.checkoutInFlight = false
		m.mode = viewModeCheckoutConfirm
		m.status = "checkout: " + msg.ref + " — uncommitted changes"
		m.statusStyle = statusErrS
		return m, nil

	case checkoutFailedMsg:
		m.checkoutInFlight = false
		m.pendingCheckout = pendingCheckout{}
		m.status = "checkout failed: " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil

	case graphActionMsg:
		m.actionInFlight = false
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
			m.ffInFlight = true
			m.status = ffLabel(msg.branch, msg.advance) + " …"
			m.statusStyle = statusBusyS
			return m, ffOnlyCmd(m.workdir, msg.branch, msg.hash)
		case graphActionCheckoutAndFF:
			m.ffInFlight = true
			m.status = "fast-forward: " + msg.branch + " (checkout + ff) …"
			m.statusStyle = statusBusyS
			return m, checkoutThenFFCmd(m.workdir, msg.branch, msg.hash)
		case graphActionDetach:
			var cmd tea.Cmd
			m, cmd = m.beginCheckout(msg.hash, true)
			return m, cmd
		}
		return m, nil

	case ffSucceededMsg:
		m.ffInFlight = false
		m.status = ffLabel(msg.branch, msg.advance)
		m.statusStyle = statusOkS
		m.pendingHEADHash = pendingHEADSentinel
		return m, m.reloadCmd()

	case ffFailedMsg:
		m.ffInFlight = false
		log.Printf("graph enter: ff failed: %v", msg.err)
		m.status = "fast-forward failed: " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
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
		m.status = ffLabel(msg.branch, msg.advance) + " (after checkout)"
		m.statusStyle = statusOkS
		m.pendingHEADHash = pendingHEADSentinel
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
		// While a pull is still in flight, its "pulling…" status outranks
		// fetch's outcome and the pending pullSucceededMsg / pullConflictMsg
		// will reload. Skip status overwrite + the redundant reload.
		if m.pullInFlight {
			return m, nil
		}
		m.status = "fetch: done"
		m.statusStyle = statusOkS
		return m, m.reloadCmd()

	case fetchFailedMsg:
		m.fetchInFlight = false
		if m.pullInFlight {
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
		return m, m.reloadCmd()

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
		// Re-arm the inline confirm with force=true so the user can press
		// `Y` to retry with -D. Stay in viewModeRefDeleteConfirm; the
		// renderer swaps the hint from "[y] delete" to "[Y] force delete"
		// off pendingRefDelete (which is still populated).
		m.refActionInFlight = false
		m.status = "'" + msg.localName + "' not fully merged — press [Y] to force"
		m.statusStyle = statusErrS
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
			loadLocalChangesSummaryCmd(m.workdir),
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
			loadLocalChangesSummaryCmd(m.workdir),
			loadWorktreesCmd(m.workdir, m.sidebarWorktreesReqID),
		)

	case localChangesRestoreFailedMsg:
		m.status = "unstage " + msg.path + ": " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil

	case localChangesSummaryLoadedMsg:
		m.refs.SetLocalChangesSummary(msg.summary, msg.loadedAt)
		return m, nil

	case localChangesSummaryFailedMsg:
		// Sidebar inline meta is a nice-to-have — a failed numstat (rare
		// outside detached HEAD without a HEAD ref) should not noise up
		// status; the bare label still renders.
		m.refs.ResetLocalChangesSummary()
		return m, nil
	}
	return m, nil
}
