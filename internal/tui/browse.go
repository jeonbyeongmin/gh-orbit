// `o` — open the cursor commit's PR on GitHub via `gh pr view --web`.
// Only commits whose chips name an open-PR head branch respond; a bare
// commit has no PR to open, and the per-commit GitHub page earned no
// keybind of its own — the review loop reads diffs in the cockpit and
// only leaves for the PR surface. The cockpit is a gh extension, so the
// gh CLI is guaranteed present; non-GitHub remotes surface gh's own
// error on the status line.
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

const browseTimeout = 15 * time.Second

type prBrowseOpenedMsg struct{ number int }
type browseFailedMsg struct{ err error }

// browsePRExec is the package-level seam over the `gh pr view --web`
// subprocess.
var browsePRExec = func(ctx context.Context, dir string, number int) error {
	cmd := exec.CommandContext(ctx, "gh", "pr", "view", strconv.Itoa(number), "--web")
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return fmt.Errorf("gh pr view: %w", err)
		}
		return fmt.Errorf("gh pr view: %s", msg)
	}
	return nil
}

func browsePRCmd(dir string, number int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), browseTimeout)
		defer cancel()
		if err := browsePRExec(ctx, dir, number); err != nil {
			return browseFailedMsg{err: err}
		}
		return prBrowseOpenedMsg{number: number}
	}
}

// beginBrowsePR opens the open PR whose head branch is chip-decorated on
// the cursor row — the same PR the row's `#N` badge names. Rows without
// a PR-bearing chip report on the status line instead of guessing.
func (m Model) beginBrowsePR() (Model, tea.Cmd) {
	pr, ok := m.prForCursorRow()
	if !ok {
		m.status = "no open PR on this commit"
		m.statusStyle = statusErrS
		return m, nil
	}
	m.setBusyStatus(fmt.Sprintf("opening PR #%d on GitHub…", pr.Number))
	return m, browsePRCmd(m.workdir, pr.Number)
}

// prForCursorRow resolves the cursor row's chips against the open-PR map
// — the same matching chipDisplay uses, so `o` works exactly where a
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
