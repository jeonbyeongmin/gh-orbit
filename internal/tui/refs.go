// Left-pane ref list (Local / Remote / Tags). Like graphModel, git access
// is async via tea.Cmd → tea.Msg so the TUI never blocks on for-each-ref.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

const refLoadTimeout = 30 * time.Second

type refModel struct {
	refs   []git.Ref
	width  int
	height int
	cursor int
	loaded bool
	err    error
}

func newRefsModel() refModel { return refModel{} }

type refsLoadedMsg struct{ refs []git.Ref }
type refsLoadFailedMsg struct{ err error }

// loadRefsCmd runs git.ForEachRef in a tea.Cmd. dir == "" uses the process cwd.
func loadRefsCmd(dir string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), refLoadTimeout)
		defer cancel()
		refs, err := git.ForEachRef(ctx, git.ForEachRefOptions{Dir: dir})
		if err != nil {
			return refsLoadFailedMsg{err: err}
		}
		return refsLoadedMsg{refs: refs}
	}
}

func (r refModel) Init() tea.Cmd { return nil }

func (r refModel) Update(msg tea.Msg) (refModel, tea.Cmd) {
	switch m := msg.(type) {
	case refsLoadedMsg:
		r.refs = m.refs
		r.cursor = 0
		r.loaded = true
		r.err = nil
		return r, nil
	case refsLoadFailedMsg:
		r.loaded = true
		r.err = m.err
		return r, nil
	}
	return r, nil
}

func (r refModel) View() string {
	if !r.loaded {
		return "loading…"
	}
	if r.err != nil {
		return fmt.Sprintf("(load error: %s)", r.err)
	}
	if len(r.refs) == 0 {
		return "(no refs)"
	}
	width := r.width
	if width < 1 {
		width = 1
	}
	var b strings.Builder
	for i, ref := range r.refs {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(runewidth.Truncate(ref.ShortName, width, "…"))
	}
	return b.String()
}

// SetSize must be called when the parent pane's inner content area changes.
func (r *refModel) SetSize(w, h int) {
	r.width = w
	r.height = h
}
