// `o` — open the cursor commit on GitHub via `gh browse`. The cockpit is
// a gh extension, so the gh CLI is guaranteed present; non-GitHub remotes
// surface gh's own error on the status line.
package tui

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const browseTimeout = 15 * time.Second

type browseOpenedMsg struct{ hash string }
type browseFailedMsg struct{ err error }

// browseExec is the package-level seam over the `gh browse` subprocess.
var browseExec = func(ctx context.Context, dir, hash string) error {
	cmd := exec.CommandContext(ctx, "gh", "browse", hash)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return fmt.Errorf("gh browse: %w", err)
		}
		return fmt.Errorf("gh browse: %s", msg)
	}
	return nil
}

func browseCmd(dir, hash string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), browseTimeout)
		defer cancel()
		if err := browseExec(ctx, dir, hash); err != nil {
			return browseFailedMsg{err: err}
		}
		return browseOpenedMsg{hash: hash}
	}
}

// beginBrowse opens the cursor commit's GitHub page in the browser.
func (m Model) beginBrowse() (Model, tea.Cmd) {
	c, ok := m.graph.Selected()
	if !ok {
		return m, nil
	}
	m.status = "opening " + shortHash(c.Hash) + " on GitHub…"
	m.statusStyle = statusBusyS
	return m, browseCmd(m.workdir, c.Hash)
}
