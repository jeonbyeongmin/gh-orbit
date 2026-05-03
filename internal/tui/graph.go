// Middle-pane commit list backed by bubbles/list. git is read off the Update
// goroutine so the TUI stays responsive on large repos.
package tui

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

const (
	defaultLogMaxCount = 200
	shortHashLen       = 7
	timeColWidth       = 6

	colorHash     = "214"
	colorTime     = "245"
	colorSelected = "205"
)

// commitItem wraps a Commit so it can be stored in bubbles/list.
type commitItem struct{ c git.Commit }

func (i commitItem) FilterValue() string { return i.c.Subject }

// commitDelegate renders one commit per line: prefix + short hash + relative
// time + subject (with truncation when the row is too narrow).
type commitDelegate struct{}

func (commitDelegate) Height() int                              { return 1 }
func (commitDelegate) Spacing() int                             { return 0 }
func (commitDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd  { return nil }

func (d commitDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	ci, ok := item.(commitItem)
	if !ok {
		return
	}
	selected := index == m.Index()
	width := m.Width()
	_, _ = fmt.Fprint(w, renderCommitLine(ci.c, width, selected))
}

var (
	hashStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color(colorHash))
	timeStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color(colorTime))
	cursorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color(colorSelected))
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colorSelected)).Bold(true)
)

func renderCommitLine(c git.Commit, width int, selected bool) string {
	hash := c.Hash
	if len(hash) > shortHashLen {
		hash = hash[:shortHashLen]
	}
	rel := relativeShort(c.AuthorTime)

	prefix := "  "
	if selected {
		prefix = cursorStyle.Render("›") + " "
	}
	const prefixWidth = 2 // "› " or "  "

	// Layout: [prefix][hash] [right-aligned rel in timeColWidth] [subject]
	used := prefixWidth + shortHashLen + 1 + timeColWidth + 1
	remaining := width - used

	if remaining < 1 {
		return prefix + hashStyle.Render(hash)
	}

	subject := runewidth.Truncate(c.Subject, remaining, "…")
	if selected {
		subject = selectedStyle.Render(subject)
	}

	return fmt.Sprintf("%s%s %s %s",
		prefix,
		hashStyle.Render(hash),
		timeStyle.Render(runewidth.FillLeft(rel, timeColWidth)),
		subject,
	)
}

// graphModel is the middle-pane sub-model.
type graphModel struct {
	list   list.Model
	width  int
	height int
	err    error
	loaded bool
}

func newGraphModel() graphModel {
	l := list.New(nil, commitDelegate{}, 0, 0)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetShowPagination(false)
	l.SetFilteringEnabled(false)
	l.DisableQuitKeybindings()
	l.SetShowFilter(false)
	return graphModel{list: l}
}

// Messages emitted by loadCommitsCmd.
type commitsLoadedMsg struct{ commits []git.Commit }
type commitsLoadFailedMsg struct{ err error }

// loadCommitsCmd runs git.Log in a tea.Cmd. dir == "" uses the process cwd.
func loadCommitsCmd(dir string, max int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		commits, err := git.Log(ctx, git.LogOptions{Dir: dir, MaxCount: max})
		if err != nil {
			return commitsLoadFailedMsg{err: err}
		}
		return commitsLoadedMsg{commits: commits}
	}
}

func (g graphModel) Init() tea.Cmd { return nil }

func (g graphModel) Update(msg tea.Msg) (graphModel, tea.Cmd) {
	switch m := msg.(type) {
	case commitsLoadedMsg:
		items := make([]list.Item, len(m.commits))
		for i, c := range m.commits {
			items[i] = commitItem{c: c}
		}
		cmd := g.list.SetItems(items)
		g.loaded = true
		g.err = nil
		return g, cmd
	case commitsLoadFailedMsg:
		g.loaded = true
		g.err = m.err
		return g, nil
	case tea.KeyMsg:
		var cmd tea.Cmd
		g.list, cmd = g.list.Update(msg)
		return g, cmd
	}
	return g, nil
}

func (g graphModel) View() string {
	if !g.loaded {
		return "loading…"
	}
	if g.err != nil {
		return fmt.Sprintf("(load error: %s)", g.err)
	}
	if len(g.list.Items()) == 0 {
		return "(no commits)"
	}
	return g.list.View()
}

// SetSize must be called when the parent pane's inner content area changes.
func (g *graphModel) SetSize(w, h int) {
	g.width = w
	g.height = h
	g.list.SetSize(w, h)
}

// Selected returns the commit currently under the cursor, if any.
func (g graphModel) Selected() (git.Commit, bool) {
	item, ok := g.list.SelectedItem().(commitItem)
	if !ok {
		return git.Commit{}, false
	}
	return item.c, true
}
