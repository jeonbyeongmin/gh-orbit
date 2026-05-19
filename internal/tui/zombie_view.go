package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// zombieCleanupVisibleRows caps how many branch names the confirm modal
// inlines before sliding the rest under a `+N more` footer. Picked so the
// modal stays single-screen on a typical terminal even when a stale dev
// tree has dozens of merged feature branches.
const zombieCleanupVisibleRows = 8

// renderZombieCleanupConfirmInner returns the modal body: header,
// per-branch rows (capped at zombieCleanupVisibleRows with `+N more`
// fallback), recovery hint, and key matrix. Matches the branch-picker
// rhythm so the visual vocabulary stays consistent across confirm modals.
func (m Model) renderZombieCleanupConfirmInner() string {
	state := m.zombieCleanup
	n := len(state.branches)
	header := confirmPromptS.Render(fmt.Sprintf("Delete %d zombie %s?", n, pluralizeBranches(n)))
	sub := help.Render(fmt.Sprintf("merged into %s · upstream gone · not checked out", state.baseline))

	lines := []string{header, sub}
	visible := n
	if visible > zombieCleanupVisibleRows {
		visible = zombieCleanupVisibleRows
	}
	for i := 0; i < visible; i++ {
		lines = append(lines, "  "+state.branches[i].Name)
	}
	if rest := n - visible; rest > 0 {
		lines = append(lines, help.Render(fmt.Sprintf("  +%d more", rest)))
	}
	recovery := help.Render("recover any deletion with `git reflog`")
	lines = append(lines, recovery)
	hintText := "[y / Y] delete all · [esc] cancel"
	if m.zombieInFlight {
		hintText = "deleting…"
	}
	lines = append(lines, help.Render(hintText))
	return strings.Join(lines, "\n")
}

// formatZombieSummary builds the post-delete status line. The deleted +
// failed split is surfaced explicitly so the user can see when git
// rejected a branch that passed the cockpit's pre-check (e.g. a race
// where the branch became un-merged between detect and execute).
func formatZombieSummary(deleted []string, failed []zombieDeleteFailure) (string, lipgloss.Style) {
	switch {
	case len(deleted) == 0 && len(failed) == 0:
		return "zombie cleanup: nothing deleted", statusOkS
	case len(failed) == 0:
		return fmt.Sprintf("deleted %d %s: %s · recover via `git reflog`",
			len(deleted), pluralizeBranches(len(deleted)), strings.Join(deleted, ", ")), statusOkS
	case len(deleted) == 0:
		return fmt.Sprintf("zombie cleanup failed for %d: %s", len(failed), firstFailureSummary(failed)), statusErrS
	default:
		return fmt.Sprintf("deleted %d, %d failed: %s · recover via `git reflog`",
			len(deleted), len(failed), firstFailureSummary(failed)), statusErrS
	}
}

func firstFailureSummary(failed []zombieDeleteFailure) string {
	parts := make([]string, 0, len(failed))
	for _, f := range failed {
		parts = append(parts, fmt.Sprintf("%s (%s)", f.name, firstLine(f.err.Error())))
	}
	return strings.Join(parts, "; ")
}

func pluralizeBranches(n int) string {
	if n == 1 {
		return "branch"
	}
	return "branches"
}
