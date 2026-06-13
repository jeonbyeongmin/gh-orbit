// PR review pane: `O` on a PR-badged graph row pulls that PR's diff into the
// same full-screen patch overlay the commit diff uses (viewModeDiffWindow),
// then approve / merge run inline without leaving the cockpit. The cockpit is
// a gh extension, so the gh CLI is guaranteed present; non-GitHub remotes /
// logged-out gh surface gh's own error on the overlay's hint line.
//
// The approve and merge confirms are NOT separate centered modals — the diff
// overlay is full-screen, so a composeOverlay box would paint over the graph
// base (View() early-returns for viewModeDiffWindow). Instead they live as a
// sub-state of the overlay (prAction) and only swap the bottom hint line,
// mirroring the viewModeCheckoutConfirm idiom: the diff stays on screen while
// the reviewer confirms — exactly what reading-then-merging wants.
package tui

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// prAction is the inline confirm sub-state inside the PR diff overlay. None is
// the plain "reading the diff" state; approve / merge arm the matching confirm
// hint and gate the overlay keymap until the reviewer commits or escapes.
type prAction int

const (
	prActionNone prAction = iota
	prActionApprove
	prActionMerge
)

// prDiffID is the synthetic identity token handed to diffModel in place of a
// commit hash. The overlay's stale-drop guard (diffModel.accepts) only needs a
// stable string per dispatch; a PR has no hash, so "pr/<n>" stands in and lets
// the existing diffPatchLoadedMsg path apply the PR diff with zero changes to
// diffModel.
func prDiffID(number int) string { return "pr/" + strconv.Itoa(number) }

type prApproveDoneMsg struct{ number int }
type prApproveFailedMsg struct{ err error }
type prMergeDoneMsg struct {
	number   int
	strategy string
}
type prMergeFailedMsg struct{ err error }

// prDiffExec is the package-level seam over `gh pr diff`. --color always keeps
// git's green/red palette (parseFileBoundaries strips the ANSI before matching
// headers, so file navigation is unaffected).
var prDiffExec = func(ctx context.Context, dir string, number int) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", "pr", "diff", strconv.Itoa(number), "--color", "always")
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("gh pr diff: %s", firstLine(msg))
		}
		return "", fmt.Errorf("gh pr diff: %w", err)
	}
	return stdout.String(), nil
}

// prReviewExec is the package-level seam over `gh pr review --approve`.
var prReviewExec = func(ctx context.Context, dir string, number int) error {
	cmd := exec.CommandContext(ctx, "gh", "pr", "review", strconv.Itoa(number), "--approve")
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("gh pr review: %s", firstLine(msg))
		}
		return fmt.Errorf("gh pr review: %w", err)
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

// loadPRDiffCmd fetches a PR's unified diff and routes it through the existing
// diffPatchLoadedMsg / diffPatchFailedMsg handlers (update_msgs.go), reusing
// diffModel.ApplyPatchLoaded verbatim. The synthetic prDiffID stands in for
// the commit hash the commit-diff path passes.
func loadPRDiffCmd(dir string, number int, reqID uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), diffPatchTimeout)
		defer cancel()
		text, err := prDiffExec(ctx, dir, number)
		if err != nil {
			return diffPatchFailedMsg{reqID: reqID, hash: prDiffID(number), err: err}
		}
		return diffPatchLoadedMsg{reqID: reqID, hash: prDiffID(number), text: text}
	}
}

func approvePRCmd(dir string, number int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), browseTimeout)
		defer cancel()
		if err := prReviewExec(ctx, dir, number); err != nil {
			return prApproveFailedMsg{err: err}
		}
		return prApproveDoneMsg{number: number}
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

// beginPRReview opens the PR diff overlay for the cursor row's open PR — the
// same PR the row's `#N` badge names, resolved by the shared prForCursorRow.
// Mirrors the `d` commit-overlay handler (update_keys.go): bump diffReqID,
// BeginPatchLoad with the synthetic id, size the viewport, dispatch the load.
// Rows without a PR-bearing chip report on the status line instead of guessing.
func (m Model) beginPRReview() (Model, tea.Cmd) {
	pr, ok := m.prForCursorRow()
	if !ok {
		m.status = "no open PR on this commit"
		m.statusStyle = statusErrS
		return m, nil
	}
	return m.beginPRReviewFor(pr.Number)
}

// beginPRReviewFor opens the PR diff overlay for an explicit PR number — the
// shared core behind both `O` (cursor row, via beginPRReview) and the `l` PR
// list modal (prsModalEnter). Bumps diffReqID, arms the synthetic patch load,
// sizes the viewport, dispatches the diff fetch.
func (m Model) beginPRReviewFor(number int) (Model, tea.Cmd) {
	m.diffReqID++
	m.diff.BeginPatchLoad(prDiffID(number), m.diffReqID)
	m.mode = viewModeDiffWindow
	m.reviewPRNumber = number
	m.prAction = prActionNone
	m.prReviewNotice = ""
	m.prReviewNoticeErr = false
	m.diff.SetPatchViewportSize(m.width, m.height-1)
	return m, loadPRDiffCmd(m.workdir, number, m.diffReqID)
}

// dispatchPRApprove / dispatchPRMerge arm prReviewInFlight (which gates the
// overlay keymap to ctrl+c only) and a busy status. The busy status drives the
// spinner via statusIsBusy — spinnerVisible checks it before the mode switch,
// so the overlay animates even though diffModel itself isn't loading.
func (m Model) dispatchPRApprove() (tea.Model, tea.Cmd) {
	m.prReviewInFlight = true
	m.prAction = prActionNone
	m.setBusyStatus(fmt.Sprintf("approving #%d…", m.reviewPRNumber))
	return m, approvePRCmd(m.workdir, m.reviewPRNumber)
}

func (m Model) dispatchPRMerge(strategy string) (tea.Model, tea.Cmd) {
	m.prReviewInFlight = true
	m.prAction = prActionNone
	m.setBusyStatus(fmt.Sprintf("merging #%d (%s)…", m.reviewPRNumber, strategy))
	return m, mergePRCmd(m.workdir, m.reviewPRNumber, strategy)
}

// clearBusy drops the in-flight status so statusIsBusy stops the spinner. The
// overlay's hint line reports the outcome via prReviewNotice instead — the
// diff View() never renders m.status, so leaving the busy text on would just
// spin forever.
func (m *Model) clearBusy() {
	m.status = ""
	m.statusBusyText = ""
}

// updatePRReviewMsg folds the approve / merge replies back into the overlay.
// approve keeps the overlay open (read on, or chain a merge) and reports in
// the hint; merge closes back to the graph and reports on the normal status
// line. Both refresh the PR list so the chip badge tracks the new state.
func (m Model) updatePRReviewMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case prApproveDoneMsg:
		m.prReviewInFlight = false
		m.prAction = prActionNone
		m.clearBusy()
		m.prReviewNotice = fmt.Sprintf("approved #%d", msg.number)
		m.prReviewNoticeErr = false
		return m, m.dispatchPRList()
	case prApproveFailedMsg:
		m.prReviewInFlight = false
		m.prAction = prActionNone
		m.clearBusy()
		m.prReviewNotice = firstLine(msg.err.Error())
		m.prReviewNoticeErr = true
		return m, nil
	case prMergeDoneMsg:
		m.prReviewInFlight = false
		m.prAction = prActionNone
		m.reviewPRNumber = 0
		m.prReviewNotice = ""
		m.mode = viewModeNormal
		m.diff.ClosePatch()
		m.clearBusy()
		m.status = fmt.Sprintf("merged #%d (%s)", msg.number, msg.strategy)
		m.statusStyle = statusOkS
		return m, m.dispatchPRList()
	case prMergeFailedMsg:
		m.prReviewInFlight = false
		m.prAction = prActionNone
		m.clearBusy()
		m.prReviewNotice = firstLine(msg.err.Error())
		m.prReviewNoticeErr = true
		return m, nil
	}
	return m, nil
}

// renderPRReviewHint is the bottom line of the overlay while it shows a PR
// diff (reviewPRNumber != 0). It carries three states: in-flight (spinner +
// busy text), a one-shot result notice (approve ok / action failed, dismissed
// by the next keypress), and the default browse line. The armed approve /
// merge confirms are NOT here — they render as a centered dialog over the
// diff (renderPRActionConfirmInner); while one is armed this line shows the
// dimmed browse hint behind the box.
func (m Model) renderPRReviewHint() string {
	n := m.reviewPRNumber
	if m.prReviewInFlight {
		return statusBusyS.Render(spinnerGlyph(m.spinnerFrame) + " " + m.status)
	}
	if m.prReviewNotice != "" {
		style := statusOkS
		if m.prReviewNoticeErr {
			style = statusErrS
		}
		return style.Render(m.prReviewNotice) + help.Render(" · esc close")
	}
	base := fmt.Sprintf("PR #%d · a approve · m merge · esc close", n)
	path, idx, total := m.diff.CurrentFile()
	if total == 0 || path == "" {
		return fitHelpLine(base, m.width)
	}
	full := fmt.Sprintf("%s [%d/%d] · %s", path, idx, total, base)
	if lipgloss.Width(full) <= m.width {
		return help.Render(full)
	}
	// The file prefix overflows — drop it so the PR actions always survive.
	return fitHelpLine(base, m.width)
}

// renderPRActionConfirmInner is the centered confirm dialog for the armed
// approve / merge action. View() composes it over the dimmed PR diff (not the
// graph base) so the diff stays in view behind the box — the same
// renderModalBox vocabulary the rebase / revert / reset confirms use. Empty
// string for prActionNone (View() only calls it while one is armed).
func (m Model) renderPRActionConfirmInner() string {
	switch m.prAction {
	case prActionApprove:
		return strings.Join([]string{
			confirmPromptS.Render(fmt.Sprintf("approve PR #%d?", m.reviewPRNumber)),
			help.Render("[y] yes · [esc] cancel"),
		}, "\n")
	case prActionMerge:
		return strings.Join([]string{
			confirmPromptS.Render(fmt.Sprintf("merge PR #%d?", m.reviewPRNumber)),
			help.Render("[s] squash · [m] merge · [r] rebase · [esc] cancel"),
		}, "\n")
	}
	return ""
}
