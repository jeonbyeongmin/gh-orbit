// Background `git fetch` dispatch. Same tea.Cmd → tea.Msg pattern as
// loadCommitsCmd / loadRefsCmd so the TUI never blocks on the network.
package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// fetchTimeout is generous on purpose — large repos over slow networks can
// blow past the 30s budget the read-only loaders use, and a hard cap is still
// nicer than a hung TUI.
const fetchTimeout = 120 * time.Second

type fetchSucceededMsg struct{}
type fetchFailedMsg struct{ err error }

func fetchCmd(dir string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		if err := git.Fetch(ctx, git.FetchOptions{Dir: dir, All: true}); err != nil {
			return fetchFailedMsg{err: err}
		}
		return fetchSucceededMsg{}
	}
}
