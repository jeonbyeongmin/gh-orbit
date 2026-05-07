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
	defaultLogMaxCount  = 200
	streamBatchSize     = 200
	streamBatchInterval = 50 * time.Millisecond
	shortHashLen        = 7
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
	list           list.Model
	delegate       commitDelegate
	width          int
	height         int
	err            error
	loaded         bool
	streaming      bool // a LogStream is in flight; loaded may already be true
	userHasMoved   bool // true after the user has intentionally moved the cursor (j/k/g/G/etc)
	graphWidth     int  // graph column width currently in effect (after cap)
	maxVisualWidth int  // widest graphPrefix among loaded rows
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

// commitsStreamStartedMsg is the first event of a streaming load. The Model
// stashes cancel for r/quit teardown and dispatches next to start collecting
// batches. reqID lets stale streams (after a reload) drop their messages.
type commitsStreamStartedMsg struct {
	reqID  uint64
	cancel context.CancelFunc
	next   tea.Cmd
}

// commitsAppendedMsg carries one batch of rows. done=true means this is the
// final batch (channel closed cleanly). next, when non-nil, is the cmd that
// will collect the next batch.
type commitsAppendedMsg struct {
	reqID uint64
	rows  []graphRow
	done  bool
	next  tea.Cmd
}

// commitsStreamDoneMsg ends a stream. err is non-nil for a real failure;
// nil err means natural end-of-history or quiet ctx cancel.
type commitsStreamDoneMsg struct {
	reqID uint64
	err   error
}

// streamState lives across a stream's lifetime. The lane allocator is created
// once per reload so lane numbers stay continuous across batch boundaries.
type streamState struct {
	reqID          uint64
	ctx            context.Context
	ch             <-chan git.CommitOrErr
	cancel         context.CancelFunc
	alloc          *lanes.Allocator
	firstBatchSent bool
}

// loadCommitsCmd kicks off a streaming `git log`. The first message is
// commitsStreamStartedMsg (carrying cancel + the batch-collector cmd);
// subsequent batches arrive as commitsAppendedMsg, with commitsStreamDoneMsg
// closing the stream.
//
// reqID lets the model drop stale messages after a reload — only the latest
// reqID's batches should mutate the list.
func loadCommitsCmd(dir string, refs []string, max int, reqID uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		ch, err := git.LogStream(ctx, git.LogOptions{Dir: dir, Refs: refs, MaxCount: max})
		if err != nil {
			cancel()
			return commitsStreamDoneMsg{reqID: reqID, err: err}
		}
		state := &streamState{
			reqID:  reqID,
			ctx:    ctx,
			ch:     ch,
			cancel: cancel,
			alloc:  lanes.New(),
		}
		return commitsStreamStartedMsg{
			reqID:  reqID,
			cancel: cancel,
			next:   collectBatchCmd(state),
		}
	}
}

// collectBatchCmd accumulates commits from the LogStream channel until either
// streamBatchSize commits arrive or streamBatchInterval elapses with at least
// one commit pending, then emits a commitsAppendedMsg. The first commit
// always flushes immediately so the initial paint happens within ms.
//
// ctx.Done is on every wait path — this is the firstBatchSent / drain race
// guard called out in the plan.
func collectBatchCmd(state *streamState) tea.Cmd {
	return func() tea.Msg {
		rows := make([]graphRow, 0, streamBatchSize)
		deadline := time.NewTimer(streamBatchInterval)
		defer deadline.Stop()
		for {
			select {
			case <-state.ctx.Done():
				go drainStream(state.ch)
				return commitsStreamDoneMsg{reqID: state.reqID}
			case ev, ok := <-state.ch:
				if !ok {
					if len(rows) > 0 {
						return commitsAppendedMsg{reqID: state.reqID, rows: rows, done: true}
					}
					return commitsStreamDoneMsg{reqID: state.reqID}
				}
				if ev.Err != nil {
					if len(rows) > 0 {
						return commitsAppendedMsg{
							reqID: state.reqID, rows: rows, done: true,
							next: errMsgCmd(state.reqID, ev.Err),
						}
					}
					return commitsStreamDoneMsg{reqID: state.reqID, err: ev.Err}
				}
				pair := state.alloc.Push(ev.Commit)
				connectorText, connectorW := renderGraphRow(pair.Connector)
				commitText, commitW := renderGraphRow(pair.Commit)
				rows = append(rows, graphRow{
					commit:          ev.Commit,
					connectorPrefix: connectorText,
					connectorWidth:  connectorW,
					commitPrefix:    commitText,
					commitWidth:     commitW,
				})
				if !state.firstBatchSent {
					state.firstBatchSent = true
					return commitsAppendedMsg{
						reqID: state.reqID, rows: rows, done: false,
						next: collectBatchCmd(state),
					}
				}
				if len(rows) >= streamBatchSize {
					return commitsAppendedMsg{
						reqID: state.reqID, rows: rows, done: false,
						next: collectBatchCmd(state),
					}
				}
			case <-deadline.C:
				if len(rows) > 0 {
					return commitsAppendedMsg{
						reqID: state.reqID, rows: rows, done: false,
						next: collectBatchCmd(state),
					}
				}
				deadline.Reset(streamBatchInterval)
			}
		}
	}
}

func drainStream(ch <-chan git.CommitOrErr) {
	for range ch {
	}
}

func errMsgCmd(reqID uint64, err error) tea.Cmd {
	return func() tea.Msg { return commitsStreamDoneMsg{reqID: reqID, err: err} }
}

func (g graphModel) Init() tea.Cmd { return nil }

func (g graphModel) Update(msg tea.Msg) (graphModel, tea.Cmd) {
	switch m := msg.(type) {
	case commitsStreamStartedMsg:
		g.streaming = true
		if m.next != nil {
			return g, m.next
		}
		return g, nil
	case commitsAppendedMsg:
		return g.handleAppended(m)
	case commitsStreamDoneMsg:
		g.streaming = false
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
			// PR #14 회귀 가드: tail-follow 는 사용자가 한 번이라도
			// cursor 를 의식적으로 옮긴 뒤에만 작동해야 한다.
			g.userHasMoved = true
			return g, tea.Batch(cmd, emitCommitSelected(newHash))
		}
		return g, cmd
	}
	return g, nil
}

// handleAppended folds one streaming batch into the list. First batch:
// SetItems + commitSelectedMsg for the initial cursor. Subsequent batch:
// append, with tail-follow gated by userHasMoved (PR #14 회귀 가드).
func (g graphModel) handleAppended(m commitsAppendedMsg) (graphModel, tea.Cmd) {
	if !g.loaded {
		items := make([]list.Item, 0, len(m.rows))
		maxW := 0
		for _, r := range m.rows {
			items = append(items, commitItem{
				c:                r.commit,
				connectorPrefix:  r.connectorPrefix,
				connectorWidth:   r.connectorWidth,
				commitPrefix:     r.commitPrefix,
				commitGraphWidth: r.commitWidth,
			})
			if r.commitWidth > maxW {
				maxW = r.commitWidth
			}
			if r.connectorWidth > maxW {
				maxW = r.connectorWidth
			}
		}
		g.maxVisualWidth = maxW
		g.applyGraphCap()
		setCmd := g.list.SetItems(items)
		g.loaded = true
		g.streaming = !m.done
		g.err = nil
		cmds := []tea.Cmd{setCmd}
		if c, ok := g.Selected(); ok {
			cmds = append(cmds, emitCommitSelected(c.Hash))
		}
		if !m.done && m.next != nil {
			cmds = append(cmds, m.next)
		}
		return g, tea.Batch(cmds...)
	}

	prev := g.list.Items()
	prevLen := len(prev)
	// Tail-follow only when the user has actually moved the cursor at least
	// once and is sitting on the last visible row. Without userHasMoved the
	// fresh cursor at index 0 == prevLen-1 (single-row first batch) would
	// trigger spurious tail-follow on the second batch — the PR #14 회귀.
	atTail := g.userHasMoved && prevLen > 0 && g.list.Index() == prevLen-1

	items := make([]list.Item, 0, prevLen+len(m.rows))
	items = append(items, prev...)
	maxW := g.maxVisualWidth
	for _, r := range m.rows {
		items = append(items, commitItem{
			c:                r.commit,
			connectorPrefix:  r.connectorPrefix,
			connectorWidth:   r.connectorWidth,
			commitPrefix:     r.commitPrefix,
			commitGraphWidth: r.commitWidth,
		})
		if r.commitWidth > maxW {
			maxW = r.commitWidth
		}
		if r.connectorWidth > maxW {
			maxW = r.connectorWidth
		}
	}
	if maxW != g.maxVisualWidth {
		g.maxVisualWidth = maxW
		g.applyGraphCap()
	}
	setCmd := g.list.SetItems(items)
	g.streaming = !m.done

	cmds := []tea.Cmd{setCmd}
	if atTail {
		g.list.Select(len(items) - 1)
		if c, ok := g.Selected(); ok {
			// Tail-follow moved the cursor — re-emit so Commit/Changes
			// tabs follow it. model.go's diffReqID debounces fast batches.
			cmds = append(cmds, emitCommitSelected(c.Hash))
		}
	}
	if !m.done && m.next != nil {
		cmds = append(cmds, m.next)
	}
	return g, tea.Batch(cmds...)
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
	g.streaming = false
	g.userHasMoved = false
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
