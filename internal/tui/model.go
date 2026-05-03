// Package tui hosts the Bubble Tea models, panes, and key bindings.
//
// The MVP screen is a Fork-style 3-pane layout: refs on the left, commit
// graph in the middle, diff on the right. This file is a placeholder skeleton
// — panes render their name and a focus indicator. Real data wiring lives in
// follow-up files (refs.go, graph.go, diff.go).
package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type pane int

const (
	paneRefs pane = iota
	paneGraph
	paneDiff
	paneCount
)

func (p pane) title() string {
	return [...]string{"refs", "commit graph", "diff"}[p]
}

// Model is the root Bubble Tea model. Sub-pane models will be composed in
// later as fields here.
type Model struct {
	width, height int
	focused       pane
}

func New() Model {
	return Model{focused: paneGraph}
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "h":
			if m.focused > 0 {
				m.focused--
			}
		case "l":
			if m.focused < paneCount-1 {
				m.focused++
			}
		}
	}
	return m, nil
}

var (
	borderUnfocused = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240"))
	borderFocused = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("205"))
	help = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
)

func (m Model) View() string {
	if m.width == 0 {
		return "starting…"
	}

	// Reserve one row for the help line. Each pane gets a border on all
	// four sides, so subtract 2 from the inner height.
	inner := m.height - 1
	contentH := inner - 2
	if contentH < 1 {
		contentH = 1
	}

	// Naive width split: refs 20%, graph 50%, diff 30%. Borders eat 2 cols
	// per pane, so subtract 6 total before splitting.
	avail := m.width - 6
	if avail < 3 {
		avail = 3
	}
	widths := [paneCount]int{
		avail * 20 / 100,
		avail * 50 / 100,
		0,
	}
	widths[paneDiff] = avail - widths[paneRefs] - widths[paneGraph]

	boxes := make([]string, paneCount)
	for p := paneRefs; p < paneCount; p++ {
		style := borderUnfocused
		if p == m.focused {
			style = borderFocused
		}
		boxes[p] = style.
			Width(widths[p]).
			Height(contentH).
			Render(p.title())
	}

	row := lipgloss.JoinHorizontal(lipgloss.Top, boxes[paneRefs], boxes[paneGraph], boxes[paneDiff])
	return lipgloss.JoinVertical(lipgloss.Left, row, help.Render("h/l move focus · q quit"))
}
