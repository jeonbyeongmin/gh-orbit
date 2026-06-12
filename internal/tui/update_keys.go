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
	if m.mode == viewModeBranchesModal {
		return m.handleBranchesModalKey(msg)
	}
	if m.mode == viewModeWorktreesModal {
		return m.handleWorktreesModalKey(msg)
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
	switch msg.String() {
	case "esc":
		m.mode = viewModeNormal
		m.diff.ClosePatch()
		return m, nil
	case "ctrl+c":
		return m.handleCtrlC()
	case "j", "k", "down", "up", "pgdown", "pgup":
		return m, m.diff.ScrollPatch(msg)
	case "]":
		m.diff.JumpToNextFile()
		return m, nil
	case "[":
		m.diff.JumpToPrevFile()
		return m, nil
	}
	return m, nil
}

func (m Model) handleWorktreeAddInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = viewModeNormal
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
	case "esc":
		m.mode = viewModeNormal
		m.worktreeAction.removeTarget = git.Worktree{}
		return m, nil
	case "ctrl+c":
		return m.handleCtrlC()
	case "y":
		if needsForce {
			m.mode = viewModeNormal
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
	case "esc":
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
	switch msg.String() {
	case "esc":
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
	case "esc":
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
	case "enter":
		return m.worktreesModalEnter()
	case "a":
		return m.beginWorktreeAdd()
	case "d":
		return m.worktreesModalRemove()
	case "s":
		return m.worktreesModalToggleSort(), nil
	case "w", "esc":
		// `w` toggles the modal closed, mirroring how it opens.
		m.mode = viewModeNormal
		m.worktreesModal = worktreesModalState{}
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
	case "esc":
		m.mode = viewModeNormal
		m.zombieCleanup = zombieCleanupState{}
		m.status = "zombie cleanup: aborted"
		m.statusStyle = statusOkS
		return m, nil
	case "ctrl+c":
		return m.handleCtrlC()
	case "y", "Y":
		m.zombieInFlight = true
		m.status = fmt.Sprintf("deleting %d zombie branches…", len(m.zombieCleanup.branches))
		m.statusStyle = statusBusyS
		return m, deleteZombieBranchesCmd(m.workdir, m.zombieCleanup.branches)
	}
	return m, nil
}

func (m Model) handleLocalChangesKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m.handleCtrlC()
	case ",", "esc":
		m.exitLocalChangesMode()
		m.status = "local changes: exit"
		m.statusStyle = statusOkS
		return m, nil
	case "?":
		m.mode = viewModeHelp
		m.applyPaneSizes()
		return m, nil
	case "tab":
		return m.cycleLocalChangesFocus(), nil
	case "r":
		return m, loadStatusCmd(m.workdir)
	}
	// Tree sub-focus owns cursor movement + stage/unstage.
	// Diff sub-focus owns viewport scroll.
	switch m.localChanges.Focused() {
	case paneLCTree:
		return m.handleLocalChangesTreeKey(msg)
	case paneLCDiff:
		return m, m.localChanges.ScrollDiff(msg)
	}
	return m, nil
}

func (m Model) handleCheckoutConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "a", "esc":
		m.mode = viewModeNormal
		p := m.pendingCheckout
		m.pendingCheckout = pendingCheckout{}
		switch {
		case p.withFF, p.withCheckoutFF:
			m.status = "fast-forward: aborted"
		default:
			m.status = "checkout: aborted"
		}
		m.statusStyle = statusOkS
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
		// Toggle the inline help reference panel. It grows out of the
		// footer (not a modal) — other shortcuts keep working while it is
		// open, so applyPaneSizes reflows the graph around the reserved
		// rows on both expand and collapse.
		if m.mode == viewModeHelp {
			m.mode = viewModeNormal
		} else {
			m.mode = viewModeHelp
		}
		m.applyPaneSizes()
		return m, nil
	case "F":
		if m.fetchInFlight {
			return m, nil
		}
		m.fetchInFlight = true
		m.lastFetchAt = time.Now()
		m.refs.SetLastFetchAt(m.lastFetchAt)
		m.status = "fetching…"
		m.statusStyle = statusBusyS
		return m, fetchCmd(m.workdir)
	case "p":
		if m.pullInFlight {
			return m, nil
		}
		m.pullInFlight = true
		m.status = "pulling…"
		m.statusStyle = statusBusyS
		return m, pullCmd(m.workdir, m.pullPrefStrategy)
	case "r":
		return m, m.reloadCmd()
	case ",":
		cmd := m.enterLocalChangesMode()
		m.status = "local changes"
		m.statusStyle = statusOkS
		return m, cmd
	case "y":
		m = m.copyHashFromGraph()
		return m, nil
	case "R":
		// Swallow so capital R doesn't fall through to the focused
		// sub-model. Reserved for a future Rebase action.
		return m, nil
	case "Z":
		// Zombie-branch cleanup is a global action now that the sidebar
		// is gone — the previous paneRefs focus gate had no meaningful
		// successor, and the bulk-delete is the same regardless of
		// which pane the user is on.
		if m.zombieInFlight {
			return m, nil
		}
		m.zombieInFlight = true
		m.status = "scanning for zombie branches…"
		m.statusStyle = statusBusyS
		return m, detectZombieBranchesCmd(m.workdir)
	case "enter":
		// Graph is the only focused pane. The sidebar was retired in
		// PR B2; the bottom tab pane was retired with the subtract-
		// bottom-pane change. Worktree switch + Local Changes enter
		// come from `w` modal and `,` global.
		if m.actionInFlight || m.checkoutInFlight || m.ffInFlight {
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
		m.status = "→ resolving…"
		m.statusStyle = statusBusyS
		log.Printf("graph enter: dispatch evaluator (cursor=%s, locals=%d, remotes=%d)",
			shortHash(c.Hash), len(locals), len(remotes))
		return m, evaluateGraphActionCmd(m.workdir, c.Hash, locals, remotes)
	case "d":
		// `d` opens the patch overlay for the focused commit. Sidebar
		// is gone so the previous paneRefs interpretation (worktree
		// remove via cursor row) moved into the `w` worktree modal.
		c, ok := m.graph.Selected()
		if !ok {
			return m, nil
		}
		m.diffReqID++
		m.diff.BeginPatchLoad(c.Hash, m.diffReqID)
		m.mode = viewModeDiffWindow
		m.diff.SetPatchViewportSize(m.width, m.height-1)
		return m, loadDiffPatchCmd(m.workdir, c.Hash, m.diffReqID)
	case "b":
		// Branches modal — local-branch list with cursor + `d` delete
		// entry. Global, independent of focused pane.
		return m.beginBranchesModal()
	case "w":
		// Worktrees modal — same overlay pattern as `b`. Global,
		// independent of focused pane.
		return m.beginWorktreesModal()
	}
	var cmd tea.Cmd
	m.graph, cmd = m.graph.Update(msg)
	return m, cmd
}
