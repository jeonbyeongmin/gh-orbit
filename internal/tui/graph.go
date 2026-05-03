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
	colorGraph    = "244"
)

// commitItem wraps a Commit so it can be stored in bubbles/list. graphPrefix
// is the ASCII graph segment (e.g. "* ", "|\\ ") rendered to the left of the
// commit row; empty when no graph data is available yet.
type commitItem struct {
	c           git.Commit
	graphPrefix string
}

func (i commitItem) FilterValue() string { return i.c.Subject }

// graphRow pairs a commit with its graph segment. The git wrapper returns
// more general GraphRow values that also include connector-only rows; we keep
// only commit rows here so the list widget's index↔commit mapping stays 1:1.
type graphRow struct {
	commit      git.Commit
	graphPrefix string
}

// commitDelegate renders one commit per line: prefix + short hash + relative
// time + subject (with truncation when the row is too narrow).
type commitDelegate struct{}

func (commitDelegate) Height() int                             { return 1 }
func (commitDelegate) Spacing() int                            { return 0 }
func (commitDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

func (d commitDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	ci, ok := item.(commitItem)
	if !ok {
		return
	}
	selected := index == m.Index()
	width := m.Width()
	// graphWidth is plumbed in via the delegate in step 4; for now graph
	// data is carried on commitItem but rendered with width 0 (invisible).
	_, _ = fmt.Fprint(w, renderCommitLine(ci.c, ci.graphPrefix, 0, width, selected))
}

var (
	hashStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color(colorHash))
	timeStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color(colorTime))
	cursorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color(colorSelected))
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colorSelected)).Bold(true)
	graphStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color(colorGraph))
)

func renderCommitLine(c git.Commit, graphPrefix string, graphWidth, width int, selected bool) string {
	hash := c.Hash
	if len(hash) > shortHashLen {
		hash = hash[:shortHashLen]
	}
	rel := relativeShort(c.AuthorTime)

	cursor := "  "
	if selected {
		cursor = cursorStyle.Render("›") + " "
	}
	const cursorWidth = 2

	// Cap graph so it never eats into the hash column on narrow terminals.
	hashCap := width - cursorWidth - shortHashLen
	if hashCap < 0 {
		hashCap = 0
	}
	effectiveGraphWidth := graphWidth
	if effectiveGraphWidth > hashCap {
		effectiveGraphWidth = hashCap
	}

	graphCell := ""
	if effectiveGraphWidth > 0 {
		gp := runewidth.Truncate(graphPrefix, effectiveGraphWidth, "")
		gp = runewidth.FillRight(gp, effectiveGraphWidth)
		graphCell = graphStyle.Render(gp)
	}

	// Layout: [cursor 2][graph N][hash 7] [rel 6 right-aligned] [subject]
	used := cursorWidth + effectiveGraphWidth + shortHashLen + 1 + timeColWidth + 1
	remaining := width - used

	if remaining < 1 {
		return cursor + graphCell + hashStyle.Render(hash)
	}

	subject := runewidth.Truncate(c.Subject, remaining, "…")
	if selected {
		subject = selectedStyle.Render(subject)
	}

	return fmt.Sprintf("%s%s%s %s %s",
		cursor,
		graphCell,
		hashStyle.Render(hash),
		timeStyle.Render(runewidth.FillLeft(rel, timeColWidth)),
		subject,
	)
}

// graphModel is the middle-pane sub-model.
type graphModel struct {
	list       list.Model
	width      int
	height     int
	err        error
	loaded     bool
	graphWidth int
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
type commitsLoadedMsg struct{ rows []graphRow }
type commitsLoadFailedMsg struct{ err error }

// loadCommitsCmd runs git.Log in a tea.Cmd. dir == "" uses the process cwd.
// refs == nil falls back to HEAD; pass `[]string{"--all"}` for the all-refs view.
func loadCommitsCmd(dir string, refs []string, max int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		commits, err := git.Log(ctx, git.LogOptions{Dir: dir, Refs: refs, MaxCount: max})
		if err != nil {
			return commitsLoadFailedMsg{err: err}
		}
		rows := make([]graphRow, len(commits))
		for i, c := range commits {
			rows[i] = graphRow{commit: c}
		}
		return commitsLoadedMsg{rows: rows}
	}
}

func (g graphModel) Init() tea.Cmd { return nil }

func (g graphModel) Update(msg tea.Msg) (graphModel, tea.Cmd) {
	switch m := msg.(type) {
	case commitsLoadedMsg:
		items := make([]list.Item, len(m.rows))
		maxW := 0
		for i, r := range m.rows {
			items[i] = commitItem{c: r.commit, graphPrefix: r.graphPrefix}
			if w := runewidth.StringWidth(r.graphPrefix); w > maxW {
				maxW = w
			}
		}
		g.graphWidth = maxW
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

// ResetForReload clears state so View renders the "loading…" placeholder
// again. Use this before dispatching a fresh loadCommitsCmd so the UI
// reflects that the visible commits no longer match the requested ref. The
// returned cmd is non-nil only when a list filter is active (filter rebuild) —
// callers should batch it with the new load cmd.
func (g *graphModel) ResetForReload() tea.Cmd {
	g.loaded = false
	g.err = nil
	g.graphWidth = 0
	return g.list.SetItems(nil)
}

// Selected returns the commit currently under the cursor, if any.
func (g graphModel) Selected() (git.Commit, bool) {
	item, ok := g.list.SelectedItem().(commitItem)
	if !ok {
		return git.Commit{}, false
	}
	return item.c, true
}
