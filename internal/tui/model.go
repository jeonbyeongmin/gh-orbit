// Package tui hosts the Bubble Tea models, panes, and key bindings for the
// Fork-style 3-pane layout (refs · commit graph · diff).
package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
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

// refsAllSentinel is the git revision spec that means "every ref". Used as the
// default base for the unified graph: Init seeds currentRefs with this so the
// commit list shows every local/remote/tag from the start, Fork-style.
const refsAllSentinel = "--all"

type Model struct {
	width, height int
	focused       pane
	refs          refModel
	graph         graphModel
	// currentRefs is the last commit-query argument dispatched to
	// loadCommitsCmd. New() seeds it with [refsAllSentinel] so the unified
	// graph is the default base. Reload (r) replays git.Log with this exact
	// value, so every dispatch site that changes the visible commit set must
	// update it.
	currentRefs []string
	// fetchInFlight gates the F key while a background fetch is running so a
	// second F doesn't spawn a parallel git invocation.
	fetchInFlight bool
	// status is the one-line message rendered next to the help line:
	// "fetching…", "fetch: done", "fetch failed: …". Empty hides it.
	// statusStyle decides the color; zero value renders without color.
	status      string
	statusStyle lipgloss.Style
}

func New() Model {
	return Model{
		focused:     paneGraph,
		refs:        newRefsModel(),
		graph:       newGraphModel(),
		currentRefs: []string{refsAllSentinel},
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		loadCommitsCmd("", m.currentRefs, defaultLogMaxCount),
		loadRefsCmd(""),
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		s := m.paneSizes()
		m.refs.SetSize(s.refsW, s.contentH)
		m.graph.SetSize(s.graphW, s.contentH)
		return m, nil

	case commitsLoadedMsg, commitsLoadFailedMsg:
		var cmd tea.Cmd
		m.graph, cmd = m.graph.Update(msg)
		return m, cmd

	case refsLoadedMsg, refsLoadFailedMsg:
		var cmd tea.Cmd
		m.refs, cmd = m.refs.Update(msg)
		return m, cmd

	case refSelectedMsg:
		// Unified graph: Enter no longer reloads; it jumps the graph cursor
		// to the row whose hash equals the ref tip. currentRefs stays at
		// [refsAllSentinel] so reload(r) keeps the unified base.
		m.graph.JumpToHash(msg.ref.ObjectName)
		return m, nil

	case fetchSucceededMsg:
		m.fetchInFlight = false
		m.status = "fetch: done"
		m.statusStyle = statusOkS
		return m, m.reloadCmd()

	case fetchFailedMsg:
		m.fetchInFlight = false
		m.status = "fetch failed: " + msg.err.Error()
		m.statusStyle = statusErrS
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "h":
			if m.focused > 0 {
				m.focused--
			}
			return m, nil
		case "l":
			if m.focused < paneCount-1 {
				m.focused++
			}
			return m, nil
		case "F":
			if m.fetchInFlight {
				return m, nil
			}
			m.fetchInFlight = true
			m.status = "fetching…"
			m.statusStyle = statusBusyS
			return m, fetchCmd("")
		case "r":
			return m, m.reloadCmd()
		case "R":
			// Swallow so capital R doesn't fall through to the focused
			// sub-model. Reserved for a future Rebase action.
			return m, nil
		}
		switch m.focused {
		case paneRefs:
			if msg.String() == "a" {
				resetCmd := m.graph.ResetForReload()
				m.currentRefs = []string{refsAllSentinel}
				return m, tea.Batch(resetCmd, loadCommitsCmd("", m.currentRefs, defaultLogMaxCount))
			}
			var cmd tea.Cmd
			m.refs, cmd = m.refs.Update(msg)
			return m, cmd
		case paneGraph:
			var cmd tea.Cmd
			m.graph, cmd = m.graph.Update(msg)
			return m, cmd
		}
	}
	return m, nil
}

// reloadCmd resets both panes to their loading state and dispatches fresh
// log + refs queries. If the ref stored in m.currentRefs was deleted by
// another tool, git.Log surfaces that through the existing commitsLoadFailedMsg
// path.
func (m *Model) reloadCmd() tea.Cmd {
	resetCmd := m.graph.ResetForReload()
	m.refs.ResetForReload()
	return tea.Batch(
		resetCmd,
		loadCommitsCmd("", m.currentRefs, defaultLogMaxCount),
		loadRefsCmd(""),
	)
}

type paneSizes struct {
	refsW, graphW, diffW int
	contentH             int
}

func (m Model) paneSizes() paneSizes {
	var s paneSizes
	if m.width == 0 || m.height == 0 {
		return s
	}
	// 3 panes × 2 border cols = 6 frame cols total.
	avail := m.width - 6
	if avail < 3 {
		avail = 3
	}
	s.refsW = avail * 20 / 100
	s.graphW = avail * 50 / 100
	s.diffW = avail - s.refsW - s.graphW
	// Reserve 1 row for the help line; subtract 2 for top/bottom border.
	s.contentH = m.height - 1 - 2
	if s.contentH < 1 {
		s.contentH = 1
	}
	return s
}

var (
	borderUnfocused = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240"))
	borderFocused = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("205"))
	help        = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	statusBusyS = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	statusOkS   = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	statusErrS  = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
)

const helpText = "h/l move focus · j/k navigate · enter select ref · a all · F fetch · r reload · q quit"

// helpRendered is the styled help line. helpText is const, so we render once
// at package init instead of every View() frame.
var helpRendered = help.Render(helpText)

func (m Model) View() string {
	if m.width == 0 {
		return "starting…"
	}
	s := m.paneSizes()
	widths := [paneCount]int{s.refsW, s.graphW, s.diffW}

	contents := [paneCount]string{
		m.refs.View(),
		m.graph.View(),
		paneDiff.title(),
	}

	boxes := make([]string, paneCount)
	for p := paneRefs; p < paneCount; p++ {
		style := borderUnfocused
		if p == m.focused {
			style = borderFocused
		}
		boxes[p] = style.
			Width(widths[p]).
			Height(s.contentH).
			Render(contents[p])
	}

	row := lipgloss.JoinHorizontal(lipgloss.Top, boxes[paneRefs], boxes[paneGraph], boxes[paneDiff])
	return lipgloss.JoinVertical(lipgloss.Left, row, m.renderHelpStatus())
}

// renderHelpStatus lays out the bottom line as "help … status". When the
// terminal is too narrow to fit both, status wins — the user just triggered
// an action and seeing its outcome matters more than the help reminder.
func (m Model) renderHelpStatus() string {
	if m.status == "" {
		return helpRendered
	}
	statusRendered := m.statusStyle.Render(m.status)

	avail := m.width - lipgloss.Width(statusRendered) - 1 // 1 for the spacer
	if avail < 1 {
		return statusRendered
	}
	helpPart := helpRendered
	if lipgloss.Width(helpPart) > avail {
		helpPart = help.Render(runewidth.Truncate(helpText, avail, "…"))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, helpPart, " ", statusRendered)
}
