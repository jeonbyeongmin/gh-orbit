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
	// authorColWidth is the visible budget for the author column. Names
	// wider than this truncate with "…"; shorter ones right-pad so the
	// hash/time columns to their right stay aligned across rows.
	authorColWidth = 14

	cursorColWidth = 2
	maxLaneCap     = 8
	minLaneCap     = 2

	colorHash     = "214"
	colorTime     = "245"
	colorAuthor   = "248"
	colorSelected = "205"

	// streamBatchSize / streamBatchTimeout cap how many commits one
	// commitsAppendedMsg carries before flushing. 200 / 50ms keeps tea.Msg
	// dispatch ~20/sec while letting the graph fill in noticeably as
	// commits stream in. The very first commit flushes on its own
	// (single-row batch) so the user never stares at "loading…" while git
	// log is still warming up on a 50k-commit repo.
	streamBatchSize    = 200
	streamBatchTimeout = 50 * time.Millisecond
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

// commitItem wraps a Commit so it can be stored in bubbles/list. Both the
// connector row drawn above this commit (transitions from the previous
// commit) and the commit row itself are pre-rendered and cached here —
// the delegate emits them as a 2-line block so j/k still moves one item
// per press.
type commitItem struct {
	c                git.Commit
	connectorPrefix  string
	connectorWidth   int
	commitPrefix     string
	commitGraphWidth int
}

func (i commitItem) FilterValue() string { return i.c.Subject }

// graphRow pairs a commit with both pre-rendered graph segments (connector
// + commit), so the delegate can emit them on consecutive lines.
type graphRow struct {
	commit          git.Commit
	connectorPrefix string
	connectorWidth  int
	commitPrefix    string
	commitWidth     int
}

// commitDelegate renders one commit as a 2-line block:
//
//	[connector row]   ← lane transitions arriving at this commit
//	[commit row]      ← cursor + graph + hash + time + chips + subject
//
// graphWidth is the column width every row should reserve for the graph
// segment so columns stay aligned across the visible window. For the very
// first item (index 0) the connector is rendered as a blank line — there
// is nothing above the most recent commit to connect to.
type commitDelegate struct {
	graphWidth int
}

func (commitDelegate) Height() int                             { return 2 }
func (commitDelegate) Spacing() int                            { return 0 }
func (commitDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

func (d commitDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	ci, ok := item.(commitItem)
	if !ok {
		return
	}
	selected := index == m.Index()
	width := m.Width()

	connectorPrefix := ci.connectorPrefix
	connectorWidth := ci.connectorWidth
	if index == 0 {
		// Top-of-screen commit has no preceding commit, so its connector
		// row is a blank visual line that aligns with the graph column.
		connectorPrefix = ""
		connectorWidth = 0
	}

	connectorLine := renderConnectorLine(connectorPrefix, connectorWidth, d.graphWidth, width)
	commitLine := renderCommitLine(ci.c, ci.commitPrefix, ci.commitGraphWidth, d.graphWidth, width, selected)

	_, _ = fmt.Fprint(w, connectorLine+"\n"+commitLine)
}

var (
	hashStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color(colorHash))
	timeStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color(colorTime))
	authorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color(colorAuthor))
	cursorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color(colorSelected))
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colorSelected)).Bold(true)
)

// shortHash truncates a 40-char object name to the conventional 7-char abbrev.
// Short hashes shorter than that are returned unchanged.
func shortHash(h string) string {
	if len(h) > shortHashLen {
		return h[:shortHashLen]
	}
	return h
}

// renderCommitLine builds one commit row.
//
// graphPrefix carries pre-styled ANSI escapes. graphRowWidth is the visible
// column count of *this* row's graph (so we can pad it out without re-parsing
// ANSI), graphColWidth is the column count to reserve so every visible row
// aligns at the same boundary, and width is the overall row width.
//
// Layout (left → right):
//
//	[cursor 2][graph][chips? + sp][subject][sp + author 14?][sp][hash 7][sp][rel 8]
//
// The "message" column carries chips (when present) and the subject; chips
// sit just before the subject so a branch tip reads as a label attached to
// the message. Hash and time are right-anchored — they always render even
// at narrow widths. When the budget is tight the columns drop in this
// priority: chips → author → subject truncates to a single cell → if
// even that won't fit, the message segment disappears and only hash (then
// hash + time) remain to the right of the graph.
func renderCommitLine(c git.Commit, graphPrefix string, graphRowWidth, graphColWidth, width int, selected bool) string {
	hash := shortHash(c.Hash)
	rel := relativeShort(c.AuthorTime)

	cursor := "  "
	if selected {
		cursor = cursorStyle.Render("›") + " "
	}
	const cursorWidth = 2
	// rightTail = hash + sep + time (always-anchored right edge).
	const rightTail = shortHashLen + 1 + timeColWidth

	// Cap graph so it never eats into the right-anchored hash/time area.
	graphBudget := width - cursorWidth - rightTail
	if graphBudget < 0 {
		graphBudget = 0
	}
	effectiveCol := graphColWidth
	if effectiveCol > graphBudget {
		effectiveCol = graphBudget
	}

	graphCell, graphCellW := buildGraphCell(graphPrefix, graphRowWidth, effectiveCol)
	fixedLeft := cursorWidth + graphCellW

	// Need at least 1 cell for the subject + 1 separator before the hash.
	// Below that we drop the message column entirely and fall back to the
	// right tail (hash, then hash + time, depending on what fits).
	if width-fixedLeft-1-rightTail < 1 {
		switch {
		case width-fixedLeft >= rightTail:
			return cursor + graphCell +
				hashStyle.Render(hash) + " " +
				timeStyle.Render(runewidth.FillLeft(rel, timeColWidth))
		case width-fixedLeft >= shortHashLen:
			return cursor + graphCell + hashStyle.Render(hash)
		default:
			return cursor + graphCell
		}
	}

	// Author column — sits between the message and the right tail. Drops
	// before the subject is allowed below 1 cell, but chips drop first
	// since they're the most expendable label.
	authorSeg := ""
	authorSegW := 0
	if c.AuthorName != "" {
		candidate := 1 + authorColWidth // leading sep + fixed column
		if width-fixedLeft-candidate-1-rightTail >= 1 {
			truncated := runewidth.Truncate(c.AuthorName, authorColWidth, "…")
			truncated = runewidth.FillRight(truncated, authorColWidth)
			authorSeg = " " + authorStyle.Render(truncated)
			authorSegW = candidate
		}
	}

	// Chip cluster, attached to the front of the subject in the message
	// column. Dropped wholesale rather than partially when there isn't
	// room for both chip and subject.
	chipText, chipW := buildChips(c.RefNames, selected)
	chipSeg := ""
	chipSegW := 0
	if chipW > 0 {
		candidate := chipW + 1 // chip + trailing sep before subject
		if width-fixedLeft-candidate-authorSegW-1-rightTail >= 1 {
			chipSeg = chipText + " "
			chipSegW = candidate
		}
	}

	subjectWidth := width - fixedLeft - chipSegW - authorSegW - 1 - rightTail
	subject := runewidth.Truncate(c.Subject, subjectWidth, "…")
	subject = runewidth.FillRight(subject, subjectWidth)
	if selected {
		subject = selectedStyle.Render(subject)
	}

	return fmt.Sprintf("%s%s%s%s%s %s %s",
		cursor,
		graphCell,
		chipSeg,
		subject,
		authorSeg,
		hashStyle.Render(hash),
		timeStyle.Render(runewidth.FillLeft(rel, timeColWidth)),
	)
}

// renderConnectorLine builds one connector row: a 2-space cursor gutter,
// the styled connector graph segment padded to graphColWidth, and trailing
// spaces filling out to the row width. Connector lines never carry hash /
// time / subject — those belong on the commit row that follows.
func renderConnectorLine(connectorPrefix string, connectorRowWidth, graphColWidth, width int) string {
	const cursorWidth = 2
	cursor := strings.Repeat(" ", cursorWidth)
	if width <= cursorWidth {
		return cursor[:width]
	}

	effectiveCol := graphColWidth
	if effectiveCol > width-cursorWidth {
		effectiveCol = width - cursorWidth
	}
	if effectiveCol < 0 {
		effectiveCol = 0
	}

	graphCell, graphCellW := buildGraphCell(connectorPrefix, connectorRowWidth, effectiveCol)
	used := cursorWidth + graphCellW
	if used >= width {
		return cursor + graphCell
	}
	return cursor + graphCell + strings.Repeat(" ", width-used)
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
	list     list.Model
	delegate commitDelegate
	width    int
	height   int
	err      error
	// loaded flips true after the first commitsAppendedMsg lands or after
	// commitsStreamDoneMsg ends an empty stream — i.e. View() should stop
	// rendering the "loading…" placeholder.
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

// commitsStreamStartedMsg is the first event of every loadCommitsCmd. The
// Model stashes cancel so r/quit can stop the git process without waiting
// for it to finish.
type commitsStreamStartedMsg struct {
	reqID  uint64
	cancel context.CancelFunc
	next   tea.Cmd
}

// commitsAppendedMsg carries one batch of pre-rendered rows plus the cmd
// that pulls the next batch. The Model chains next back into the runtime
// so the producer keeps draining until commitsStreamDoneMsg lands.
type commitsAppendedMsg struct {
	reqID uint64
	rows  []graphRow
	next  tea.Cmd
}

// commitsStreamDoneMsg ends a stream; err is non-nil when LogStream's
// trailing error event was surfaced (or cmd.Start failed early).
type commitsStreamDoneMsg struct {
	reqID uint64
	err   error
}

// streamState carries the state of one in-flight LogStream subscription
// across consecutive batch tea.Cmd invocations. The lane allocator is built
// once per reload so firstPush=true applies to exactly the first commit
// pushed (not the first commit per batch).
type streamState struct {
	reqID          uint64
	ch             <-chan git.CommitOrErr
	alloc          *lanes.Allocator
	firstBatchSent bool
	closed         bool
	err            error
}

func (s *streamState) toRow(c git.Commit) graphRow {
	pair := s.alloc.Push(c)
	connectorText, connectorW := renderGraphRow(pair.Connector)
	commitText, commitW := renderGraphRow(pair.Commit)
	return graphRow{
		commit:          c,
		connectorPrefix: connectorText,
		connectorWidth:  connectorW,
		commitPrefix:    commitText,
		commitWidth:     commitW,
	}
}

// collectBatch returns a tea.Cmd that pulls the next batch from the stream.
// First call: blocks until the very first commit (or close/err) arrives and
// emits a single-row commitsAppendedMsg so the user sees the graph populate
// the moment git log produces its first line. Subsequent calls: collect up
// to streamBatchSize commits or until streamBatchTimeout elapses, whichever
// comes first.
func (s *streamState) collectBatch() tea.Cmd {
	return func() tea.Msg {
		if s.closed {
			return commitsStreamDoneMsg{reqID: s.reqID, err: s.err}
		}

		if !s.firstBatchSent {
			for {
				msg, ok := <-s.ch
				if !ok {
					s.closed = true
					return commitsStreamDoneMsg{reqID: s.reqID, err: s.err}
				}
				if msg.Err != nil {
					// Trailing err arrived before any commit. Record it and
					// keep draining — the close is right behind.
					s.err = msg.Err
					continue
				}
				s.firstBatchSent = true
				return commitsAppendedMsg{
					reqID: s.reqID,
					rows:  []graphRow{s.toRow(msg.Commit)},
					next:  s.collectBatch(),
				}
			}
		}

		var rows []graphRow
		timer := time.NewTimer(streamBatchTimeout)
		defer timer.Stop()
	loop:
		for len(rows) < streamBatchSize {
			select {
			case msg, ok := <-s.ch:
				if !ok {
					s.closed = true
					break loop
				}
				if msg.Err != nil {
					s.err = msg.Err
					continue
				}
				rows = append(rows, s.toRow(msg.Commit))
			case <-timer.C:
				if len(rows) > 0 {
					break loop
				}
				timer.Reset(streamBatchTimeout)
			}
		}

		if s.closed && len(rows) == 0 {
			return commitsStreamDoneMsg{reqID: s.reqID, err: s.err}
		}
		return commitsAppendedMsg{
			reqID: s.reqID,
			rows:  rows,
			next:  s.collectBatch(),
		}
	}
}

// loadCommitsCmd starts a streaming git log and returns a tea.Cmd that emits
// commitsStreamStartedMsg first (carrying the cancel func and the cmd that
// pulls the first batch). The Model is responsible for stashing cancel
// (so r/quit can interrupt the git process) and for chaining the embedded
// next cmd until the producer signals done.
//
// reqID is used by the Model to drop stale batches: when a fresh reload
// supersedes this stream, msg.reqID will not match m.streamReqID and the
// Model discards the batch instead of merging it into a graph that's
// already moved on.
func loadCommitsCmd(dir string, refs []string, max int, reqID uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		ch, err := git.LogStream(ctx, git.LogOptions{Dir: dir, Refs: refs, MaxCount: max})
		if err != nil {
			cancel()
			return commitsStreamDoneMsg{reqID: reqID, err: err}
		}

		state := &streamState{
			reqID: reqID,
			ch:    ch,
			alloc: lanes.New(),
		}
		return commitsStreamStartedMsg{
			reqID:  reqID,
			cancel: cancel,
			next:   state.collectBatch(),
		}
	}
}

func (g graphModel) Init() tea.Cmd { return nil }

func (g graphModel) Update(msg tea.Msg) (graphModel, tea.Cmd) {
	switch m := msg.(type) {
	case commitsAppendedMsg:
		// Empty batches can arrive transiently (timer fired right as the
		// channel closed) — chain next so the producer keeps draining.
		if len(m.rows) == 0 {
			return g, m.next
		}

		// Append in place: list.Items returns the internal slice, and the
		// SetItems below rebinds m.items to the result, so reusing the
		// backing array avoids copying every previously-loaded commit on
		// every batch (was O(N²) across the stream, now amortized O(N)).
		items := g.list.Items()
		atTail := g.list.Index() == len(items)-1
		for _, r := range m.rows {
			items = append(items, commitItem{
				c:                r.commit,
				connectorPrefix:  r.connectorPrefix,
				connectorWidth:   r.connectorWidth,
				commitPrefix:     r.commitPrefix,
				commitGraphWidth: r.commitWidth,
			})
			if r.commitWidth > g.maxVisualWidth {
				g.maxVisualWidth = r.commitWidth
			}
			if r.connectorWidth > g.maxVisualWidth {
				g.maxVisualWidth = r.connectorWidth
			}
		}
		g.applyGraphCap()
		setCmd := g.list.SetItems(items)

		firstBatch := !g.loaded
		g.loaded = true
		g.err = nil

		if atTail {
			g.list.Select(len(items) - 1)
		}
		var cmds []tea.Cmd
		if setCmd != nil {
			cmds = append(cmds, setCmd)
		}
		// Emit commitSelectedMsg only on the first batch. Tail-follow's
		// list.Select doesn't synthesize a KeyMsg, so subsequent batches
		// don't fan out into per-row diff fetches.
		if firstBatch {
			if c, ok := g.Selected(); ok {
				cmds = append(cmds, emitCommitSelected(c.Hash))
			}
		}
		if m.next != nil {
			cmds = append(cmds, m.next)
		}
		return g, tea.Batch(cmds...)
	case commitsStreamDoneMsg:
		// loaded flips true even on a zero-commit stream so View renders
		// "(no commits)" / "(load error: …)" instead of "loading…".
		g.loaded = true
		if m.err != nil && len(g.list.Items()) == 0 {
			g.err = m.err
		}
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
