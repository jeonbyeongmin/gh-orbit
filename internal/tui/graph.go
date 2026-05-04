// Middle-pane commit list backed by bubbles/list. git is read off the Update
// goroutine so the TUI stays responsive on large repos.
package tui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
	"github.com/jeonbyeongmin/gh-orbit/internal/git/lanes"
)

const (
	defaultLogMaxCount = 200
	shortHashLen       = 7
	// 8 covers the widest relativeShort output ("just now").
	timeColWidth = 8

	cursorColWidth = 2
	maxLaneCap     = 8
	minLaneCap     = 2

	colorHash     = "214"
	colorTime     = "245"
	colorSelected = "205"
)

// laneColCap is the visible column budget for the graph segment given the
// commit pane's width. Lower bound (minLaneCap×cellWidth) keeps the graph
// meaningful in narrow terminals; upper bound caps growth in very wide ones.
func laneColCap(paneWidth int) int {
	// reserve room for cursor + graph + space + hash + space + time, leave
	// at least one column for the subject.
	avail := paneWidth - cursorColWidth - shortHashLen - 1 - timeColWidth - 1
	if avail < 0 {
		avail = 0
	}
	c := avail / cellWidth
	switch {
	case c > maxLaneCap:
		c = maxLaneCap
	case c < minLaneCap:
		c = minLaneCap
	}
	return c * cellWidth
}

// commitItem wraps a Commit so it can be stored in bubbles/list. graphPrefix
// is the ANSI-styled graph segment for this row; graphWidth is its visible
// column count (we can't derive it from len() once ANSI escapes are mixed in).
type commitItem struct {
	c           git.Commit
	graphPrefix string
	graphWidth  int
}

func (i commitItem) FilterValue() string { return i.c.Subject }

// graphRow pairs a commit with its rendered graph segment.
type graphRow struct {
	commit      git.Commit
	graphPrefix string
	visualWidth int
}

// commitDelegate renders one commit per line: cursor + graph + short hash +
// relative time + subject (with truncation when the row is too narrow).
// graphWidth is the column width every row should reserve for the graph
// segment so columns stay aligned across the visible window.
type commitDelegate struct {
	graphWidth int
}

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
	_, _ = fmt.Fprint(w, renderCommitLine(ci.c, ci.graphPrefix, ci.graphWidth, d.graphWidth, width, selected))
}

var (
	hashStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color(colorHash))
	timeStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color(colorTime))
	cursorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color(colorSelected))
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colorSelected)).Bold(true)
)

// renderCommitLine builds one commit row.
//
// graphPrefix carries pre-styled ANSI escapes. graphRowWidth is the visible
// column count of *this* row's graph (so we can pad it out without re-parsing
// ANSI), graphColWidth is the column count to reserve so every visible row
// aligns at the same boundary, and width is the overall row width.
//
// Layout: [cursor 2][graph][hash 7][space][rel 6][space][chips][space][subject].
// Subject sits at the right edge so it absorbs the truncation when the row is
// too narrow; chips are between time and subject, dropped wholesale rather
// than partially when there isn't room for both chip and subject.
func renderCommitLine(c git.Commit, graphPrefix string, graphRowWidth, graphColWidth, width int, selected bool) string {
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
	effectiveCol := graphColWidth
	if effectiveCol > hashCap {
		effectiveCol = hashCap
	}

	graphCell, graphCellW := buildGraphCell(graphPrefix, graphRowWidth, effectiveCol)

	// Fixed-position prefix: cursor + graph + hash + space + relative-time.
	fixedUsed := cursorWidth + graphCellW + shortHashLen + 1 + timeColWidth

	// At least one cell for the subject (after a single separator space).
	if width-fixedUsed-1 < 1 {
		// Row is too narrow for the subject — drop it but keep hash visible.
		return cursor + graphCell + hashStyle.Render(hash)
	}

	chipText, chipW := buildChips(c.RefNames, selected)
	chipSegment := ""
	chipSegmentWidth := 0
	if chipW > 0 {
		// Chip segment = leading space (1) + chip cluster. Drop the cluster
		// entirely if including it would push the subject below 1 cell —
		// the interview answer was explicit that subject wins over chips.
		candidate := 1 + chipW
		if width-fixedUsed-candidate-1 >= 1 {
			chipSegment = " " + chipText
			chipSegmentWidth = candidate
		}
	}

	subjectWidth := width - fixedUsed - chipSegmentWidth - 1
	subject := runewidth.Truncate(c.Subject, subjectWidth, "…")
	subject = runewidth.FillRight(subject, subjectWidth)
	if selected {
		subject = selectedStyle.Render(subject)
	}

	return fmt.Sprintf("%s%s%s %s%s %s",
		cursor,
		graphCell,
		hashStyle.Render(hash),
		timeStyle.Render(runewidth.FillLeft(rel, timeColWidth)),
		chipSegment,
		subject,
	)
}

// buildGraphCell returns the styled graph segment for one row plus the actual
// visible column width consumed. When a row's prefix exceeds the column
// budget (cap reached or narrow terminal), the tail is replaced with "…" so
// the truncation is visible rather than silent.
func buildGraphCell(graphPrefix string, graphRowWidth, effectiveCol int) (string, int) {
	if effectiveCol <= 0 {
		return "", 0
	}
	if graphRowWidth <= effectiveCol {
		pad := strings.Repeat(" ", effectiveCol-graphRowWidth)
		return graphPrefix + pad, effectiveCol
	}
	return ansi.Truncate(graphPrefix, effectiveCol, "…"), effectiveCol
}

// graphModel is the middle-pane sub-model.
type graphModel struct {
	list           list.Model
	delegate       commitDelegate
	width          int
	height         int
	err            error
	loaded         bool
	graphWidth     int // graph column width currently in effect (after cap)
	maxVisualWidth int // widest graphPrefix among loaded rows
}

func newGraphModel() graphModel {
	d := commitDelegate{}
	l := list.New(nil, d, 0, 0)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetShowPagination(false)
	l.SetFilteringEnabled(false)
	l.DisableQuitKeybindings()
	l.SetShowFilter(false)
	return graphModel{list: l, delegate: d}
}

type commitsLoadedMsg struct{ rows []graphRow }
type commitsLoadFailedMsg struct{ err error }

// loadCommitsCmd runs git.Log in a tea.Cmd, then feeds the commits through a
// fresh lane allocator to produce one styled graph segment per row.
func loadCommitsCmd(dir string, refs []string, max int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		commits, err := git.Log(ctx, git.LogOptions{Dir: dir, Refs: refs, MaxCount: max})
		if err != nil {
			return commitsLoadFailedMsg{err: err}
		}
		alloc := lanes.New()
		rows := make([]graphRow, len(commits))
		for i, c := range commits {
			row := alloc.Push(c)
			text, w := renderGraphRow(row)
			rows[i] = graphRow{commit: c, graphPrefix: text, visualWidth: w}
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
			items[i] = commitItem{c: r.commit, graphPrefix: r.graphPrefix, graphWidth: r.visualWidth}
			if r.visualWidth > maxW {
				maxW = r.visualWidth
			}
		}
		g.maxVisualWidth = maxW
		g.applyGraphCap()
		cmd := g.list.SetItems(items)
		g.loaded = true
		g.err = nil
		if c, ok := g.Selected(); ok {
			return g, tea.Batch(cmd, emitCommitSelected(c.Hash))
		}
		return g, cmd
	case commitsLoadFailedMsg:
		g.loaded = true
		g.err = m.err
		return g, nil
	case tea.KeyMsg:
		prevHash := ""
		if c, ok := g.Selected(); ok {
			prevHash = c.Hash
		}
		var cmd tea.Cmd
		g.list, cmd = g.list.Update(msg)
		newHash := ""
		if c, ok := g.Selected(); ok {
			newHash = c.Hash
		}
		if newHash != "" && newHash != prevHash {
			return g, tea.Batch(cmd, emitCommitSelected(newHash))
		}
		return g, cmd
	}
	return g, nil
}

func emitCommitSelected(hash string) tea.Cmd {
	return func() tea.Msg { return commitSelectedMsg{hash: hash} }
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
	g.applyGraphCap()
}

// applyGraphCap reconciles graphWidth with both the row data and the current
// pane width: take the smaller of "widest row prefix" and "lane cap for this
// width", then push the value down into the delegate.
func (g *graphModel) applyGraphCap() {
	cap := g.maxVisualWidth
	if g.width > 0 {
		if c := laneColCap(g.width); cap > c {
			cap = c
		}
	}
	if cap == g.graphWidth {
		return
	}
	g.graphWidth = cap
	g.delegate.graphWidth = cap
	g.list.SetDelegate(g.delegate)
}

// ResetForReload clears state so View renders the "loading…" placeholder
// again. Use this before dispatching a fresh loadCommitsCmd so the UI
// reflects that the visible commits no longer match the requested ref. The
// returned cmd is non-nil only when a list filter is active (filter rebuild) —
// callers should batch it with the new load cmd.
func (g *graphModel) ResetForReload() tea.Cmd {
	g.loaded = false
	g.err = nil
	g.maxVisualWidth = 0
	g.graphWidth = 0
	g.delegate.graphWidth = 0
	g.list.SetDelegate(g.delegate)
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

// JumpToHash moves the cursor to the row whose commit hash equals the given
// hash. It returns true if a matching row was found. The first match wins, so
// when multiple refs point at the same commit (e.g. main ≡ origin/main) the
// cursor lands on the same row regardless of which ref was selected.
func (g *graphModel) JumpToHash(hash string) bool {
	for i, it := range g.list.Items() {
		ci, ok := it.(commitItem)
		if !ok {
			continue
		}
		if ci.c.Hash == hash {
			g.list.Select(i)
			return true
		}
	}
	return false
}
