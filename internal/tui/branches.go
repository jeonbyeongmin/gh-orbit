// branches modal: viewModeBranchesModal hosts a centered overlay listing
// every local branch. `b` opens it from viewModeNormal; `d` arms the
// existing delete confirm dialog (viewModeRefDeleteConfirm) against the
// modal's cursor row, mirroring the refs-pane `d` flow so the delete-
// branch chain stays single-codepath.
//
// The modal reuses m.refs.LocalRefs() as its source of truth — refs.go's
// loadRefsCmd still drives the data; this surface only owns its own
// cursor.
package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// branchesModalState backs viewModeBranchesModal. cursor is an index into
// the m.refs.LocalRefs() slice as observed at modal-open time. A delete
// inside the modal exits to viewModeRefDeleteConfirm; on succeed the
// post-reload handler may shrink the local list, so any re-entry into
// the modal recomputes cursor afresh via beginBranchesModal.
type branchesModalState struct {
	cursor int
}

const helpTextBranchesModal = "[j/k] navigate · [d] delete · [esc] close"

// beginBranchesModal opens viewModeBranchesModal. cursor lands on HEAD if
// found, else 0. Empty local list surfaces an inline error and stays in
// viewModeNormal so the user never enters a modal with nothing to act on.
func (m Model) beginBranchesModal() (Model, tea.Cmd) {
	locals := m.refs.LocalRefs()
	if len(locals) == 0 {
		m.status = "branches: no local branches"
		m.statusStyle = statusErrS
		return m, nil
	}
	cursor := 0
	for i, ref := range locals {
		if ref.IsHead {
			cursor = i
			break
		}
	}
	m.branchesModal.cursor = cursor
	m.mode = viewModeBranchesModal
	m.status = ""
	return m, nil
}

// branchesModalMoveCursor bounds the cursor to [0, len-1]. Called by the
// `j` / `k` keypress handlers.
func (m Model) branchesModalMoveCursor(delta int) Model {
	locals := m.refs.LocalRefs()
	if len(locals) == 0 {
		return m
	}
	c := m.branchesModal.cursor + delta
	if c < 0 {
		c = 0
	}
	if c >= len(locals) {
		c = len(locals) - 1
	}
	m.branchesModal.cursor = c
	return m
}

// beginBranchesModalDelete arms viewModeRefDeleteConfirm for the cursor
// row. Rejects HEAD with a status line (mirrors beginRefDelete). On esc
// from the confirm, viewModeRefDeleteConfirm's handler returns to
// viewModeNormal — the user re-presses `b` to reopen, which is simpler
// than threading a "previous mode" stack.
func (m Model) beginBranchesModalDelete() (Model, tea.Cmd) {
	locals := m.refs.LocalRefs()
	if len(locals) == 0 {
		return m, nil
	}
	if m.branchesModal.cursor < 0 || m.branchesModal.cursor >= len(locals) {
		return m, nil
	}
	ref := locals[m.branchesModal.cursor]
	if ref.IsHead {
		m.status = "cannot delete current branch"
		m.statusStyle = statusErrS
		return m, nil
	}
	m.pendingRefDelete = refDeleteState{localName: ref.ShortName}
	m.mode = viewModeRefDeleteConfirm
	m.status = ""
	return m, nil
}

// renderBranchesModalInner returns the centered overlay content: bold
// header, scroll-windowed list with `>` cursor and `←` HEAD marker, and
// the action hint. Window math is shared with renderBranchPickerInner via
// renderScrollWindow so the scrolling feel stays uniform across modals.
func (m Model) renderBranchesModalInner() string {
	locals := m.refs.LocalRefs()
	header := modalHeaderS.Render("[Branches]")
	if len(locals) == 0 {
		return strings.Join([]string{
			header,
			help.Render("(no local branches)"),
			help.Render(helpTextBranchesModal),
		}, "\n")
	}

	const visibleBudget = 16

	lines := []string{header}
	lines = append(lines, renderScrollWindow(
		m.branchesModal.cursor-visibleBudget/2, visibleBudget, len(locals),
		func(i int) string {
			ref := locals[i]
			label := ref.ShortName
			if ref.IsHead {
				label = label + " ←"
			}
			if i == m.branchesModal.cursor {
				return selectedStyle.Render("> " + label)
			}
			return "  " + label
		})...)
	lines = append(lines, help.Render(helpTextBranchesModal))
	return strings.Join(lines, "\n")
}
