// `n` — create a branch at the cursor commit and switch to it. The
// branch-name input reuses the worktree-add textinput modal shape:
// constant row count, inline error line, esc cancels.
package tui

import (
	"context"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

const branchCreateTimeout = 30 * time.Second

type branchCreateSucceededMsg struct {
	reqID uint64
	name  string
}
type branchCreateFailedMsg struct {
	reqID uint64
	err   error
}

// branchCreateExec is the package-level seam over git.CheckoutNewBranch.
var branchCreateExec = git.CheckoutNewBranch

// branchCreateState backs viewModeBranchCreateInput. reqID drops a slow
// reply that raced a re-opened modal; startPoint is the cursor hash the
// branch will be created at.
type branchCreateState struct {
	reqID      uint64
	inFlight   bool
	input      textinput.Model
	inlineErr  string
	startPoint string
}

func branchCreateCmd(dir, name, startPoint string, reqID uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), branchCreateTimeout)
		defer cancel()
		if err := branchCreateExec(ctx, dir, name, startPoint); err != nil {
			return branchCreateFailedMsg{reqID: reqID, err: err}
		}
		return branchCreateSucceededMsg{reqID: reqID, name: name}
	}
}

// beginBranchCreate opens the name-input modal anchored at the cursor
// commit.
func (m Model) beginBranchCreate() (Model, tea.Cmd) {
	if m.gitMutationInFlight() {
		return m, nil
	}
	c, ok := m.graph.Selected()
	if !ok {
		return m, nil
	}
	m.branchCreate.reqID++
	ti := textinput.New()
	ti.Placeholder = "branch name"
	ti.CharLimit = 200
	ti.Width = 40
	ti.Focus()
	m.branchCreate.input = ti
	m.branchCreate.inlineErr = ""
	m.branchCreate.startPoint = c.Hash
	m.mode = viewModeBranchCreateInput
	return m, textinput.Blink
}

const helpTextBranchCreate = "[enter] create + switch · [esc] cancel"

// renderBranchCreateInputInner — header (naming the start commit),
// textinput row, inline error or spacer, action hint. Constant row count
// so the hint never bounces.
func (m Model) renderBranchCreateInputInner() string {
	header := modalHeaderS.Render("[New branch @ " + shortHash(m.branchCreate.startPoint) + "]")
	input := m.branchCreate.input.View()
	errLine := " "
	switch {
	case m.branchCreate.inlineErr != "":
		errLine = statusErrS.Render(m.branchCreate.inlineErr)
	case m.branchCreate.inFlight:
		errLine = statusBusyS.Render("creating…")
	}
	hint := help.Render(helpTextBranchCreate)
	return strings.Join([]string{header, input, errLine, hint}, "\n")
}

func (m Model) handleBranchCreateInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = viewModeNormal
		m.branchCreate.input = textinput.Model{}
		m.branchCreate.inlineErr = ""
		return m, nil
	case "ctrl+c":
		return m.handleCtrlC()
	case "enter":
		if m.branchCreate.inFlight {
			return m, nil
		}
		name := strings.TrimSpace(m.branchCreate.input.Value())
		if name == "" {
			m.branchCreate.inlineErr = "branch name is empty"
			return m, nil
		}
		m.branchCreate.inFlight = true
		m.branchCreate.inlineErr = ""
		return m, branchCreateCmd(m.workdir, name, m.branchCreate.startPoint, m.branchCreate.reqID)
	}
	var cmd tea.Cmd
	m.branchCreate.input, cmd = m.branchCreate.input.Update(msg)
	return m, cmd
}
