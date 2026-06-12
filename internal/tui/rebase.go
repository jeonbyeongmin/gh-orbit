// `R` — rebase the current branch onto the graph cursor. The flow is
// confirm-first (an inline bottom-line prompt, same surface as the
// branch-delete confirm) because rebase rewrites history; conflicts are
// delegated to the terminal exactly like pull conflicts — the cockpit
// reports and reloads, it never auto-aborts a mid-rebase state.
package tui

import (
	"context"
	"errors"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// rebaseTimeout mirrors pullTimeout — replaying many commits can be slow,
// but a hard cap beats a hung TUI.
const rebaseTimeout = 300 * time.Second

type rebaseSucceededMsg struct{}
type rebaseConflictMsg struct{ err error }
type rebaseFailedMsg struct{ err error }

// rebaseExec is the package-level seam over git.Rebase for tests.
var rebaseExec = git.Rebase

// pendingRebase backs viewModeRebaseConfirm: the cursor hash the rebase
// will replay onto, the human label the prompt names it by (first chip on
// the row, else the short hash), and the HEAD branch being rebased.
type pendingRebase struct {
	onto   string
	label  string
	branch string
}

func rebaseCmd(dir, onto string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), rebaseTimeout)
		defer cancel()
		if err := rebaseExec(ctx, dir, onto); err != nil {
			if errors.Is(err, git.ErrRebaseConflict) {
				return rebaseConflictMsg{err: err}
			}
			return rebaseFailedMsg{err: err}
		}
		return rebaseSucceededMsg{}
	}
}

// beginRebase arms viewModeRebaseConfirm for the cursor commit. Rejected
// up front when another graph action is mid-flight, when HEAD is detached
// (nothing to replay), or when the cursor is HEAD itself (no-op).
func (m Model) beginRebase() (Model, tea.Cmd) {
	if m.gitMutationInFlight() {
		return m, nil
	}
	c, ok := m.graph.Selected()
	if !ok {
		return m, nil
	}
	var headBranch, headHash string
	for _, r := range m.refs.LocalRefs() {
		if r.IsHead {
			headBranch = r.ShortName
			headHash = r.ObjectName
			break
		}
	}
	if headBranch == "" {
		m.status = "rebase: detached HEAD — checkout a branch first"
		m.statusStyle = statusErrS
		return m, nil
	}
	if c.Hash == headHash {
		m.status = "rebase: cursor is already HEAD"
		m.statusStyle = statusOkS
		return m, nil
	}
	m.pendingRebase = pendingRebase{
		onto:   c.Hash,
		label:  m.rebaseTargetLabel(c.Hash),
		branch: headBranch,
	}
	m.mode = viewModeRebaseConfirm
	m.status = ""
	return m, nil
}

// rebaseTargetLabel names the onto-commit for the confirm prompt: the
// first local chip on the row wins, then the first remote chip, then the
// bare short hash.
func (m Model) rebaseTargetLabel(hash string) string {
	for _, r := range m.refs.LocalRefs() {
		if r.ObjectName == hash {
			return r.ShortName
		}
	}
	for _, r := range m.refs.RemoteRefs() {
		if r.ObjectName == hash {
			return r.ShortName
		}
	}
	return shortHash(hash)
}

// rebaseInlineHint paints the confirm prompt into the bottom hint line —
// no overlay box, so the cursor stays anchored on the onto-row (matching
// the branch-delete inline pattern).
func (m Model) rebaseInlineHint() string {
	p := m.pendingRebase
	return confirmPromptS.Render("rebase "+p.branch+" onto "+p.label+"?") + " " +
		help.Render("[y] rebase · [esc] cancel")
}

func (m Model) handleRebaseConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = viewModeNormal
		m.pendingRebase = pendingRebase{}
		m.status = "rebase: cancelled"
		m.statusStyle = statusOkS
		return m, nil
	case "ctrl+c":
		return m.handleCtrlC()
	case "y":
		p := m.pendingRebase
		m.mode = viewModeNormal
		m.rebaseInFlight = true
		m.status = "rebase: " + p.branch + " onto " + p.label + " …"
		m.statusStyle = statusBusyS
		return m, rebaseCmd(m.workdir, p.onto)
	}
	return m, nil
}
