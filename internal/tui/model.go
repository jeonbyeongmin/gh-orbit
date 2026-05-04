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

// viewMode toggles between the 3-pane layout and the full-screen patch overlay
// that `d` opens. graph cursor state is preserved across the toggle so esc
// returns the user to exactly where they were.
type viewMode int

const (
	viewModeNormal viewMode = iota
	viewModeDiffWindow
)

type Model struct {
	width, height int
	focused       pane
	mode          viewMode
	refs          refModel
	graph         graphModel
	diff          diffModel
	// diffReqID counts every diff dispatch (cursor change, `d` press). Stale
	// in-flight git show responses compare their reqID against this and drop
	// themselves if they no longer match.
	diffReqID uint64
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
		diff:        newDiffModel(),
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
		m.diff.SetSize(s.diffW, s.contentH)
		if m.mode == viewModeDiffWindow {
			m.diff.SetPatchViewportSize(m.width, m.height-1)
		}
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
		// [refsAllSentinel] so reload(r) keeps the unified base. When the
		// tip is outside the loaded MaxCount window we surface that through
		// the status bar instead of failing silently.
		if m.graph.JumpToHash(msg.ref.ObjectName) {
			m.status = ""
			if c, ok := m.graph.Selected(); ok {
				return m, m.beginDiffStat(c.Hash)
			}
		} else {
			m.status = "ref tip not in loaded window: " + msg.ref.ShortName
			m.statusStyle = statusErrS
		}
		return m, nil

	case commitSelectedMsg:
		return m, m.beginDiffStat(msg.hash)

	case diffDebounceMsg:
		// Drop stale ticks — if the user kept moving the cursor inside the
		// 200ms window, m.diffReqID has already advanced past this tick's
		// reqID and the latest tick wins.
		if msg.reqID != m.diffReqID {
			return m, nil
		}
		return m, loadDiffStatCmd("", msg.hash, msg.reqID)

	case diffStatLoadedMsg:
		m.diff.ApplyStatLoaded(msg.reqID, msg.hash, msg.text)
		return m, nil
	case diffStatFailedMsg:
		m.diff.ApplyStatFailed(msg.reqID, msg.hash, msg.err)
		return m, nil
	case diffPatchLoadedMsg:
		m.diff.ApplyPatchLoaded(msg.reqID, msg.hash, msg.text)
		return m, nil
	case diffPatchFailedMsg:
		m.diff.ApplyPatchFailed(msg.reqID, msg.hash, msg.err)
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
		// d-window guard MUST come before the global ctrl+c/q quit branch:
		// otherwise pressing q to close the overlay would terminate the app.
		if m.mode == viewModeDiffWindow {
			switch msg.String() {
			case "esc", "q":
				m.mode = viewModeNormal
				return m, nil
			case "ctrl+c":
				return m, tea.Quit
			case "j", "k", "down", "up", "pgdown", "pgup":
				m.diff.ScrollViewport(msg)
				return m, nil
			}
			// Swallow everything else inside the overlay so stray keys
			// don't leak to the focused sub-model.
			return m, nil
		}
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
		case "d":
			c, ok := m.graph.Selected()
			if !ok {
				return m, nil
			}
			m.diffReqID++
			m.diff.BeginPatchLoad(c.Hash, m.diffReqID)
			m.mode = viewModeDiffWindow
			m.diff.SetPatchViewportSize(m.width, m.height-1)
			return m, loadDiffPatchCmd("", c.Hash, m.diffReqID)
		}
		switch m.focused {
		case paneRefs:
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

// beginDiffStat advances the diff request id, marks the diff sub-model as
// loading for the given hash, and returns the debounce tick cmd. Used by both
// graph cursor moves (commitSelectedMsg) and ref-tip jumps (refSelectedMsg)
// so the right pane always reflects the currently focused commit.
func (m *Model) beginDiffStat(hash string) tea.Cmd {
	m.diffReqID++
	m.diff.MarkLoadingStat(hash, m.diffReqID)
	return scheduleDiffStatCmd(m.diffReqID, hash)
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

const (
	helpTextNormal     = "h/l move focus · j/k navigate · enter jump to ref · d diff · F fetch · r reload · q quit"
	helpTextDiffWindow = "j/k scroll · pgup/pgdn page · esc/q close"
)

// helpRendered is the styled help line. The two help strings are const, so we
// render once at package init instead of every View() frame.
var (
	helpRenderedNormal     = help.Render(helpTextNormal)
	helpRenderedDiffWindow = help.Render(helpTextDiffWindow)
)

func (m Model) View() string {
	if m.width == 0 {
		return "starting…"
	}
	if m.mode == viewModeDiffWindow {
		return lipgloss.JoinVertical(lipgloss.Left, m.diff.PatchView(), helpRenderedDiffWindow)
	}
	s := m.paneSizes()
	widths := [paneCount]int{s.refsW, s.graphW, s.diffW}

	contents := [paneCount]string{
		m.refs.View(),
		m.graph.View(),
		m.diff.StatView(),
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
		return helpRenderedNormal
	}
	statusRendered := m.statusStyle.Render(m.status)

	avail := m.width - lipgloss.Width(statusRendered) - 1 // 1 for the spacer
	if avail < 1 {
		return statusRendered
	}
	helpPart := helpRenderedNormal
	if lipgloss.Width(helpPart) > avail {
		helpPart = help.Render(runewidth.Truncate(helpTextNormal, avail, "…"))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, helpPart, " ", statusRendered)
}
