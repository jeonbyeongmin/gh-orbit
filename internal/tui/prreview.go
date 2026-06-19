// PR actions from the graph, the Pull Requests page, and the worktree
// dashboard. Reviewing a PR happens on GitHub now: `enter` opens the cursor
// row's open PR in the browser (`gh pr view --web`); the cockpit keeps only the
// two actions worth a keystroke from the terminal — jump to the PR, and land it
// with `m` (a merge confirm dialog → `gh pr merge`). The cockpit is a gh
// extension, so the gh CLI is guaranteed present; non-GitHub remotes / a
// logged-out gh surface gh's own error on the status line.
package tui

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// browseTimeout bounds the gh subprocesses (web open / merge).
const browseTimeout = 15 * time.Second

// mergeConfirmState backs viewModeMergeConfirm: the open PR the dialog will
// merge. The launching page is recorded on Model.mergeReturnMode.
type mergeConfirmState struct {
	number int
}

type prWebOpenedMsg struct{ number int }
type prWebFailedMsg struct{ err error }
type prMergeDoneMsg struct {
	number   int
	strategy string
}
type prMergeFailedMsg struct{ err error }

// prViewWebExec is the package-level seam over `gh pr view --web`, which opens
// the PR in the default browser and returns immediately.
var prViewWebExec = func(ctx context.Context, dir string, number int) error {
	cmd := exec.CommandContext(ctx, "gh", "pr", "view", strconv.Itoa(number), "--web")
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("gh pr view: %s", firstLine(msg))
		}
		return fmt.Errorf("gh pr view: %w", err)
	}
	return nil
}

// prMergeExec is the package-level seam over `gh pr merge --<strategy>`.
// strategy is one of squash/merge/rebase; the caller never passes another
// value. --delete-branch is deliberately omitted — deleting the branch is a
// surprise the reviewer didn't ask for.
var prMergeExec = func(ctx context.Context, dir string, number int, strategy string) error {
	cmd := exec.CommandContext(ctx, "gh", "pr", "merge", strconv.Itoa(number), "--"+strategy)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("gh pr merge: %s", firstLine(msg))
		}
		return fmt.Errorf("gh pr merge: %w", err)
	}
	return nil
}

func openPRWebCmd(dir string, number int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), browseTimeout)
		defer cancel()
		if err := prViewWebExec(ctx, dir, number); err != nil {
			return prWebFailedMsg{err: err}
		}
		return prWebOpenedMsg{number: number}
	}
}

func mergePRCmd(dir string, number int, strategy string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), browseTimeout)
		defer cancel()
		if err := prMergeExec(ctx, dir, number, strategy); err != nil {
			return prMergeFailedMsg{err: err}
		}
		return prMergeDoneMsg{number: number, strategy: strategy}
	}
}

// prForCursorRow resolves the cursor row's chips against the open-PR map
// — the same matching chipDisplay uses, so `enter` / `m` work exactly where a
// badge is visible.
func (m Model) prForCursorRow() (prInfo, bool) {
	c, ok := m.graph.Selected()
	if !ok {
		return prInfo{}, false
	}
	refs, _ := git.ParseDecoration(c.RefNames)
	for _, chip := range git.MergeLocalRemotePairs(refs) {
		if pr, ok := prForChip(chip, m.prs); ok {
			return pr, true
		}
	}
	return prInfo{}, false
}

// openPRWeb dispatches `gh pr view --web` for an explicit PR number behind a
// transient busy status. Shared by the graph cursor, the PR page, and the
// worktree dashboard.
func (m Model) openPRWeb(number int) (Model, tea.Cmd) {
	m.setBusyStatus(fmt.Sprintf("opening #%d…", number))
	return m, openPRWebCmd(m.workdir, number)
}

// openPRWebForCursor is graph `enter`: open the cursor row's open PR on the web.
// Rows without a PR-bearing chip report on the status line instead of guessing.
func (m Model) openPRWebForCursor() (Model, tea.Cmd) {
	pr, ok := m.prForCursorRow()
	if !ok {
		m.status = "no open PR on this commit"
		m.statusStyle = statusErrS
		return m, nil
	}
	return m.openPRWeb(pr.Number)
}

// beginMergeFor opens the merge confirm dialog for an explicit PR number,
// recording the launching page (graph / PR page) on mergeReturnMode so the
// centered dialog composes over — and closes back to — it. Shared by graph `m`
// and the PR page `m`.
func (m Model) beginMergeFor(number int) (Model, tea.Cmd) {
	m.mergeReturnMode = m.mode
	m.mergeConfirm = mergeConfirmState{number: number}
	m.mode = viewModeMergeConfirm
	m.status = ""
	return m, nil
}

// beginMergeForCursor is graph `m`: arm the merge confirm for the cursor row's
// PR, or report when the row carries none.
func (m Model) beginMergeForCursor() (Model, tea.Cmd) {
	pr, ok := m.prForCursorRow()
	if !ok {
		m.status = "no open PR on this commit"
		m.statusStyle = statusErrS
		return m, nil
	}
	return m.beginMergeFor(pr.Number)
}

// dispatchPRMerge runs the merge behind mergeInFlight (which gates the dialog
// to ctrl+c only) and a busy status that drives the spinner via statusIsBusy.
func (m Model) dispatchPRMerge(strategy string) (tea.Model, tea.Cmd) {
	m.mergeInFlight = true
	m.setBusyStatus(fmt.Sprintf("merging #%d (%s)…", m.mergeConfirm.number, strategy))
	return m, mergePRCmd(m.workdir, m.mergeConfirm.number, strategy)
}

// closeMergeConfirm returns to the launching page and clears the dialog state.
func (m *Model) closeMergeConfirm() {
	m.mergeInFlight = false
	m.mode = m.mergeReturnMode
	m.mergeReturnMode = viewModeNormal
	m.mergeConfirm = mergeConfirmState{}
}

// clearBusy drops the in-flight status so statusIsBusy stops the spinner.
func (m *Model) clearBusy() {
	m.status = ""
	m.statusBusyText = ""
}

// updatePRActionMsg folds the web-open and merge replies back into the model.
// Web-open is fire-and-forget (a status line, no mode change). Merge closes the
// dialog back to its launching page either way; on success it fetches so the
// graph picks up the merge commit and the PR list re-pulls (dropping the `#N`
// badge) on the same beat.
func (m Model) updatePRActionMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case prWebOpenedMsg:
		m.clearBusy()
		m.status = fmt.Sprintf("opened #%d in browser", msg.number)
		m.statusStyle = statusOkS
		return m, nil
	case prWebFailedMsg:
		m.clearBusy()
		m.status = firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil
	case prMergeDoneMsg:
		m.closeMergeConfirm()
		m.clearBusy()
		m.status = fmt.Sprintf("merged #%d (%s)", msg.number, msg.strategy)
		m.statusStyle = statusOkS
		// The merge landed on the remote, so the local graph can't show it
		// yet. Kick a fetch (unless one's already running) — its success path
		// reloads the graph and re-pulls the PR list, so the merge commit
		// appears and the `#N` badge drops without a manual `F` / `r`. Mirrors
		// the focus-fetch setup so a concurrent focus event can't double-fetch.
		if !m.fetchInFlight {
			m.fetchInFlight = true
			m.lastFetchAt = time.Now()
			m.refs.SetLastFetchAt(m.lastFetchAt)
			return m, fetchCmd(m.workdir)
		}
		return m, m.dispatchPRList()
	case prMergeFailedMsg:
		m.closeMergeConfirm()
		m.clearBusy()
		m.status = "merge failed: " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil
	case prCheckOpenedMsg:
		// Fire-and-forget like the web open: the checks modal stays up so the
		// reviewer can open another check's log without reopening it.
		m.clearBusy()
		m.status = fmt.Sprintf("opened %s in browser", msg.name)
		m.statusStyle = statusOkS
		return m, nil
	case prCheckOpenFailedMsg:
		m.clearBusy()
		m.status = firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil
	}
	return m, nil
}

// renderMergeConfirmInner is the centered merge dialog, composed over the
// launching page by View(). While the merge runs it swaps to a spinner line;
// the dialog stays up (gated to ctrl+c) so a second key can't fork a parallel
// gh call. Same renderModalBox vocabulary as the reset confirm.
func (m Model) renderMergeConfirmInner() string {
	n := m.mergeConfirm.number
	if m.mergeInFlight {
		return strings.Join([]string{
			confirmPromptS.Render(fmt.Sprintf("merge PR #%d", n)),
			statusBusyS.Render(spinnerGlyph(m.spinnerFrame) + " merging…"),
		}, "\n")
	}
	return strings.Join([]string{
		confirmPromptS.Render(fmt.Sprintf("merge PR #%d?", n)),
		help.Render("[s] squash · [m] merge · [r] rebase · [esc] cancel"),
	}, "\n")
}
