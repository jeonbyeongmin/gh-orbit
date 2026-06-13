// `x` — reset the current branch to the cursor commit, discarding the
// commits after it. Two-step like the graph Enter evaluator: `x` first runs
// an async guard (evaluateResetCmd) that confirms the cursor is behind HEAD
// and that the dropped range isn't already pushed, then arms a confirm with
// soft/mixed/hard. A pushed-history reset would need a force-push (forbidden
// here), so it is refused and steered to revert (`v`). Hard also discards the
// working tree, so the confirm flags that on its own row.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

const resetTimeout = 60 * time.Second

// resetEvalMsg is evaluateResetCmd's reply. ok==true means the guards passed
// and the confirm should arm; otherwise err holds the human-facing reason.
type resetEvalMsg struct {
	ok      bool
	err     string
	target  string
	label   string
	branch  string
	discard int
}

type resetSucceededMsg struct {
	branch  string
	label   string
	mode    string
	discard int
}
type resetFailedMsg struct{ err error }

// resetExec is the package-level seam over git.Reset for tests.
var resetExec = git.Reset

// pendingReset backs viewModeResetConfirm: the resolved target hash, its
// display label, the HEAD branch being moved, and how many commits the reset
// drops (for the confirm prompt + done status).
type pendingReset struct {
	target  string
	label   string
	branch  string
	discard int
}

// evaluateResetCmd runs the ancestor + pushed-history guards off the Update
// goroutine (each is a rev-list count). The target must be strictly behind
// HEAD — reset discards "everything after target", so a target ahead of HEAD
// has nothing to drop. When the branch has an upstream that holds commits the
// reset would drop, it refuses and steers to revert.
func evaluateResetCmd(dir, headHash, headBranch, upstream, target, label string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), checkoutTimeout)
		defer cancel()
		// ahead = commits reachable from target but not HEAD. >0 means the
		// cursor is ahead of (or diverged from) HEAD — not a discard.
		ahead, err := countAheadExec(ctx, dir, headHash, target)
		if err != nil {
			return resetEvalMsg{err: "reset: " + firstLine(err.Error())}
		}
		if ahead > 0 {
			return resetEvalMsg{err: "reset: cursor is ahead of HEAD — nothing to discard"}
		}
		// discard = commits on HEAD not reachable from target (the dropped range).
		discard, err := countAheadExec(ctx, dir, target, headHash)
		if err != nil {
			return resetEvalMsg{err: "reset: " + firstLine(err.Error())}
		}
		// Pushed-history guard: if the upstream holds commits the reset would
		// drop (upstream ahead of target), refuse — that reset needs a
		// force-push, which this repo forbids. Revert is the safe path there.
		if upstream != "" {
			if pushedDrop, err := countAheadExec(ctx, dir, target, upstream); err == nil && pushedDrop > 0 {
				return resetEvalMsg{err: "reset: would drop pushed commits — use revert (v) instead"}
			}
		}
		return resetEvalMsg{ok: true, target: target, label: label, branch: headBranch, discard: discard}
	}
}

func resetCmd(dir string, mode git.ResetMode, p pendingReset, modeName string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), resetTimeout)
		defer cancel()
		if err := resetExec(ctx, dir, mode, p.target); err != nil {
			return resetFailedMsg{err: err}
		}
		return resetSucceededMsg{branch: p.branch, label: p.label, mode: modeName, discard: p.discard}
	}
}

// beginReset runs the synchronous rejections (in-flight, detached HEAD,
// cursor==HEAD) then hands the ancestor/push guards to evaluateResetCmd.
// resetInFlight latches here and stays latched through the evaluation; the
// resetEvalMsg handler clears it when arming (or rejecting) the confirm.
func (m Model) beginReset() (Model, tea.Cmd) {
	if m.gitMutationInFlight() {
		return m, nil
	}
	c, ok := m.graph.Selected()
	if !ok {
		return m, nil
	}
	var headBranch, headHash, upstream string
	for _, r := range m.refs.LocalRefs() {
		if r.IsHead {
			headBranch = r.ShortName
			headHash = r.ObjectName
			upstream = r.Upstream
			break
		}
	}
	if headBranch == "" {
		m.status = "reset: detached HEAD — checkout a branch first"
		m.statusStyle = statusErrS
		return m, nil
	}
	if c.Hash == headHash {
		m.status = "reset: cursor is already HEAD"
		m.statusStyle = statusOkS
		return m, nil
	}
	m.resetInFlight = true
	m.setBusyStatus("reset: resolving…")
	return m, evaluateResetCmd(m.workdir, headHash, headBranch, upstream, c.Hash, m.rebaseTargetLabel(c.Hash))
}

// renderResetConfirmInner returns the reset confirm dialog content. Unlike
// the single-y confirms it offers s/m/h for the three modes; the hard row
// carries the working-tree-loss warning in the error color.
func (m Model) renderResetConfirmInner() string {
	p := m.pendingReset
	prompt := fmt.Sprintf("reset %s to %s?", p.branch, p.label)
	if p.discard > 0 {
		plural := ""
		if p.discard != 1 {
			plural = "s"
		}
		prompt += fmt.Sprintf(" (discards %d commit%s)", p.discard, plural)
	}
	return strings.Join([]string{
		confirmPromptS.Render(prompt),
		statusErrS.Render("[h] hard also discards uncommitted changes"),
		help.Render("[s] soft · [m] mixed · [h] hard · [esc] cancel"),
	}, "\n")
}

func (m Model) handleResetConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		m.mode = viewModeNormal
		m.pendingReset = pendingReset{}
		m.status = "reset: cancelled"
		m.statusStyle = statusOkS
		return m, nil
	case "ctrl+c":
		return m.handleCtrlC()
	case "s":
		return m.dispatchReset(git.ResetSoft, "soft")
	case "m":
		return m.dispatchReset(git.ResetMixed, "mixed")
	case "h":
		return m.dispatchReset(git.ResetHard, "hard")
	}
	return m, nil
}

func (m Model) dispatchReset(mode git.ResetMode, modeName string) (tea.Model, tea.Cmd) {
	p := m.pendingReset
	m.mode = viewModeNormal
	m.pendingReset = pendingReset{}
	m.resetInFlight = true
	m.setBusyStatus("reset: " + p.branch + " to " + p.label + " (" + modeName + ") …")
	return m, resetCmd(m.workdir, mode, p, modeName)
}
