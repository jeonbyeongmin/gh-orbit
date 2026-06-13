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
	streamBatchSize     = 200
	streamBatchInterval = 50 * time.Millisecond
	shortHashLen        = 7
	// 8 covers the widest relativeShort output ("just now").
	timeColWidth = 8
	// authorColWidth is the visible budget for the author column. Names
	// wider than this truncate with "…"; shorter ones right-pad so the
	// time column to their right stays aligned across rows.
	authorColWidth = 14

	cursorColWidth = 2
	maxLaneCap     = 16
	minLaneCap     = 2

	colorTime     = "245"
	colorAuthor   = "248"
	colorSelected = "205"
)

// laneColCap is the visible column budget for the graph segment given the
// commit pane's width. Lower bound (minLaneCap×cellWidth) keeps the graph
// meaningful in narrow terminals; upper bound caps growth in very wide ones.
func laneColCap(paneWidth int) int {
	// reserve room for cursor + separator + time; when a cap-width row
	// still leaves no room for the subject, renderCommitLine drops the
	// message column.
	avail := paneWidth - cursorColWidth - timeColWidth - 1
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
//	[commit row]      ← cursor + graph + chips + subject + author + time
//
// graphWidth is the hard cap (= laneColCap of the pane width); rows
// whose own prefix is wider get truncated with "…". For the very first
// item (index 0) the connector is rendered as a blank line — there is
// nothing above the most recent commit to connect to.
//
// headRowIndex == -1 suppresses dim entirely. Otherwise rows above
// headRowIndex whose Hash isn't in headAncestors are dimmed; a nil
// ancestor map is the "loading" fallback that dims everything above.
type commitDelegate struct {
	graphWidth    int
	headRowIndex  int
	headAncestors map[string]struct{}
	// prs (head branch → open PR) feeds the chip PR badge. nil until the
	// first `gh pr list` lands; kept across reloads so the badges don't
	// flicker while a fresh list is in flight.
	prs map[string]prInfo
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

	// Clamp each row's column to the cap; below the cap rows render at
	// their own prefix width so message starts right after the graph.
	capW := d.graphWidth
	commitColW := ci.commitGraphWidth
	if capW > 0 && commitColW > capW {
		commitColW = capW
	}
	connectorColW := connectorWidth
	if capW > 0 && connectorColW > capW {
		connectorColW = capW
	}

	dim := d.shouldDim(index, ci.c.Hash)
	commitPrefix := ci.commitPrefix
	// HEAD row: swap the commit dot to ◉ so "where am I" reads without
	// scanning chips. The replace targets the unique commit-cell glyph
	// (one per row); merge-HEAD swaps the hollow variant instead.
	if index == d.headRowIndex {
		if replaced := strings.Replace(commitPrefix, commitGlyph, headGlyph, 1); replaced != commitPrefix {
			commitPrefix = replaced
		} else {
			commitPrefix = strings.Replace(commitPrefix, mergeGlyph, headGlyph, 1)
		}
	}
	connectorLine := renderConnectorLine(connectorPrefix, connectorWidth, connectorColW, width, dim)
	commitLine := renderCommitLine(ci.c, d.prs, commitPrefix, ci.commitGraphWidth, commitColW, width, selected, dim)

	_, _ = fmt.Fprint(w, connectorLine+"\n"+commitLine)
}

// colorDim is xterm 240 — also reused by chips.go (chipDimStyle) and
// chipMoreStyle so the muted palette stays in one place.
const colorDim = "240"

var dimFGStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colorDim))

// dimGraphCell strips the lane palette ANSI off graphPrefix before
// re-coloring. The strip is necessary because each per-lane SGR ends
// with its own reset, which would otherwise terminate any outer
// foreground midway through the row and leave the dim effect spotty.
func dimGraphCell(graphPrefix string, graphRowWidth, effectiveCol int) (string, int) {
	plain, w := buildGraphCell(ansi.Strip(graphPrefix), graphRowWidth, effectiveCol)
	return dimFGStyle.Render(plain), w
}

// shouldDim is the row-level decision for HEAD-as-dim-boundary. Returns
// false when HEAD is out of the loaded window (-1) or for the HEAD row
// itself / rows below it (older commits). Above HEAD, ancestry membership
// keeps HEAD's reachable history bright; non-ancestors are dimmed. While
// ancestry hasn't arrived yet (nil set) the fallback is "dim everything
// above HEAD" so the boundary reads on first paint.
func (d commitDelegate) shouldDim(index int, hash string) bool {
	if d.headRowIndex < 0 {
		return false
	}
	if index >= d.headRowIndex {
		return false
	}
	if d.headAncestors == nil {
		return true
	}
	_, isAncestor := d.headAncestors[hash]
	return !isAncestor
}

// colorCursorRowBg is xterm 237 — a dark grey one step above colorDim
// (240). Used as the worktrees-modal cursor row's background tint so it stays
// visible against the default body fg without competing with the
// colorSelected (205) accent that paints the `▶` current-row body.
const colorCursorRowBg = "237"

var (
	timeStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color(colorTime))
	authorStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color(colorAuthor))
	cursorStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color(colorSelected))
	selectedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color(colorSelected)).Bold(true)
	cursorRowBgStyle = lipgloss.NewStyle().Background(lipgloss.Color(colorCursorRowBg))
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
//	[cursor 2][graph][chips? + sp][subject][sp + author 14?][sp][rel 8]
//
// The "message" column carries chips (when present) and the subject; chips
// sit just before the subject so a branch tip reads as a label attached to
// the message. Time is right-anchored — it always renders even at narrow
// widths. When the budget is tight the columns drop in this priority:
// chips → author → subject truncates to a single cell → if even that
// won't fit, the message segment disappears and only the time remains to
// the right of the graph.
func renderCommitLine(c git.Commit, prs map[string]prInfo, graphPrefix string, graphRowWidth, graphColWidth, width int, selected, dim bool) string {
	rel := relativeShort(c.AuthorTime)

	// selected wins over dim so a navigated row above HEAD still highlights.
	useDim := dim && !selected

	timeS, authorS := timeStyle, authorStyle
	if useDim {
		timeS, authorS = dimFGStyle, dimFGStyle
	}

	cursor := "  "
	if selected {
		cursor = cursorStyle.Render("›") + " "
	}
	const cursorWidth = 2

	// Cap graph so it never eats into the right-anchored time column.
	graphBudget := width - cursorWidth - timeColWidth
	if graphBudget < 0 {
		graphBudget = 0
	}
	effectiveCol := graphColWidth
	if effectiveCol > graphBudget {
		effectiveCol = graphBudget
	}

	graphCell, graphCellW := buildGraphCell(graphPrefix, graphRowWidth, effectiveCol)
	if useDim {
		graphCell, graphCellW = dimGraphCell(graphPrefix, graphRowWidth, effectiveCol)
	}
	fixedLeft := cursorWidth + graphCellW

	// Need at least 1 cell for the subject + 1 separator before the time.
	// Below that we drop the message column entirely and fall back to the
	// right tail (time alone, when it fits).
	if width-fixedLeft-1-timeColWidth < 1 {
		if width-fixedLeft >= timeColWidth {
			return cursor + graphCell +
				timeS.Render(runewidth.FillLeft(rel, timeColWidth))
		}
		return cursor + graphCell
	}

	// Author column — sits between the message and the right tail. Drops
	// before the subject is allowed below 1 cell, but chips drop first
	// since they're the most expendable label.
	authorSeg := ""
	authorSegW := 0
	if c.AuthorName != "" {
		candidate := 1 + authorColWidth // leading sep + fixed column
		if width-fixedLeft-candidate-1-timeColWidth >= 1 {
			truncated := runewidth.Truncate(c.AuthorName, authorColWidth, "…")
			truncated = runewidth.FillRight(truncated, authorColWidth)
			authorSeg = " " + authorS.Render(truncated)
			authorSegW = candidate
		}
	}

	// Chip cluster, attached to the front of the subject in the message
	// column. Dropped wholesale rather than partially when there isn't
	// room for both chip and subject.
	chipText, chipW := buildChips(c.RefNames, prs, selected, dim)
	chipSeg := ""
	chipSegW := 0
	if chipW > 0 {
		candidate := chipW + 1 // chip + trailing sep before subject
		if width-fixedLeft-candidate-authorSegW-1-timeColWidth >= 1 {
			chipSeg = chipText + " "
			chipSegW = candidate
		}
	}

	subjectWidth := width - fixedLeft - chipSegW - authorSegW - 1 - timeColWidth
	subject := runewidth.Truncate(c.Subject, subjectWidth, "…")
	subject = runewidth.FillRight(subject, subjectWidth)
	switch {
	case selected:
		subject = selectedStyle.Render(subject)
	case useDim:
		subject = dimFGStyle.Render(subject)
	}

	return fmt.Sprintf("%s%s%s%s%s %s",
		cursor,
		graphCell,
		chipSeg,
		subject,
		authorSeg,
		timeS.Render(runewidth.FillLeft(rel, timeColWidth)),
	)
}

// renderConnectorLine builds one connector row: a 2-space cursor gutter,
// the styled connector graph segment padded to graphColWidth, and trailing
// spaces filling out to the row width. Connector lines never carry time /
// subject — those belong on the commit row that follows.
// dim=true recolors the graph segment with the muted grey palette so the
// connector keeps the visual band started by the commit row above it.
func renderConnectorLine(connectorPrefix string, connectorRowWidth, graphColWidth, width int, dim bool) string {
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

	var graphCell string
	var graphCellW int
	if dim {
		graphCell, graphCellW = dimGraphCell(connectorPrefix, connectorRowWidth, effectiveCol)
	} else {
		graphCell, graphCellW = buildGraphCell(connectorPrefix, connectorRowWidth, effectiveCol)
	}
	used := cursorWidth + graphCellW
	if used >= width {
		return cursor + graphCell
	}
	return cursor + graphCell + strings.Repeat(" ", width-used)
}

// buildGraphCell returns the styled graph segment for one row plus the
// actual visible column width consumed. The "…" tail surfaces only when
// the row's prefix exceeds effectiveCol (lane count past the cap, or pane
// too narrow) — i.e. it marks dropped lanes, not silent truncation.
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
	list         list.Model
	delegate     commitDelegate
	width        int
	height       int
	err          error
	loaded       bool
	streaming    bool // a LogStream is in flight; loaded may already be true
	userHasMoved bool // true after the user has intentionally moved the cursor (j/k/g/G/etc)
	graphWidth   int  // hard cap = laneColCap(width); rows render per-row tight up to this cap

	// headRowIndex is the list index of the row carrying HEAD per the
	// streaming `%D` decoration; -1 = HEAD outside the loaded window so
	// the dim pass is suppressed. headAncestors is `git rev-list HEAD`'s
	// reachable set, loaded asynchronously to keep ancestor rows above
	// HEAD bright. headDimDirty is set whenever either field changes so
	// applyHeadDim can skip the list.SetDelegate on streaming batches
	// that don't move the boundary.
	headRowIndex  int
	headAncestors map[string]struct{}
	headDimDirty  bool

	// pendingSwap is the stale-while-revalidate flag: a reload is in flight
	// but the previous graph stays on screen (no blank "loading…" flash).
	// The next stream's first batch replaces the list instead of appending;
	// a batch-less stream end clears the list instead. Set by
	// MarkStaleForReload, never by the hard ResetForReload path.
	pendingSwap bool

	// spinnerFrame is pushed in by Model on every spinnerTickMsg so the
	// loading placeholder animates. Only read while !loaded.
	spinnerFrame int
}

func newGraphModel() graphModel {
	d := commitDelegate{headRowIndex: -1}
	l := list.New(nil, d, 0, 0)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetShowPagination(false)
	l.SetFilteringEnabled(false)
	l.DisableQuitKeybindings()
	l.SetShowFilter(false)
	return graphModel{list: l, delegate: d, headRowIndex: -1}
}

// commitsStreamStartedMsg is the first event of a streaming load. The Model
// stores cancel for r/quit teardown and dispatches next to start collecting
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

// headAncestorsLoadedMsg carries the result of `git rev-list HEAD`.
// reqID is m.streamReqID at dispatch time so a reload's fresh ancestry
// reply doesn't overwrite the new stream's state. err non-nil means the
// dim pass falls back to "all rows above HEAD are dim" — ancestry-aware
// precision is lost but the boundary still reads.
type headAncestorsLoadedMsg struct {
	reqID     uint64
	ancestors map[string]struct{}
	err       error
}

// loadHeadAncestorsCmd dispatches `git rev-list HEAD` so the graph dim
// pass can keep ancestor rows above HEAD bright. Pass the same reqID as
// the matching loadCommitsCmd so a stale reload's response gets dropped
// in the Model.Update reqID guard.
func loadHeadAncestorsCmd(dir string, reqID uint64) tea.Cmd {
	return func() tea.Msg {
		ancestors, err := git.RevListAncestors(context.Background(), dir, "HEAD")
		return headAncestorsLoadedMsg{reqID: reqID, ancestors: ancestors, err: err}
	}
}

// streamState lives across a stream's lifetime. The lane allocator is
// created once per reload so lane numbers stay continuous across batch
// boundaries.
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
// No MaxCount: streaming means git can walk the full history without
// blocking the UI, so we don't artificially cap the visible window.
//
// reqID lets the model drop stale messages after a reload — only the latest
// reqID's batches should mutate the list.
func loadCommitsCmd(dir string, refs []string, reqID uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		ch, err := git.LogStream(ctx, git.LogOptions{Dir: dir, Refs: refs})
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
				connectorText, connectorW := renderGraphRow(pair.Connector, false)
				commitText, commitW := renderGraphRow(pair.Commit, len(ev.Commit.Parents) >= 2)
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
		var cmd tea.Cmd
		if g.pendingSwap {
			// The reload finished without a single batch — the new window
			// is empty, so the kept-on-screen old graph must go now.
			g.pendingSwap = false
			g.headRowIndex = -1
			g.headAncestors = nil
			g.headDimDirty = true
			g.applyHeadDim()
			cmd = g.list.SetItems(nil)
		}
		if m.err != nil && len(g.list.Items()) == 0 {
			g.err = m.err
		}
		return g, cmd
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
		}
		return g, cmd
	}
	return g, nil
}

// appendCommitItems folds graphRows into a list.Item slice. Per-row tight
// rendering means the delegate doesn't need to know the widest prefix —
// each row carries its own commitGraphWidth / connectorWidth.
func appendCommitItems(dst []list.Item, rows []graphRow) []list.Item {
	for _, r := range rows {
		dst = append(dst, commitItem{
			c:                r.commit,
			connectorPrefix:  r.connectorPrefix,
			connectorWidth:   r.connectorWidth,
			commitPrefix:     r.commitPrefix,
			commitGraphWidth: r.commitWidth,
		})
	}
	return dst
}

// handleAppended folds one streaming batch into the list. Tail-follow on
// subsequent batches is gated by userHasMoved (PR #14 회귀 가드).
//
// The first branch covers two cases that both want "replace, don't append":
// a fresh load (nothing on screen yet) and the first batch after
// MarkStaleForReload, where the old graph stayed visible to avoid a blank
// flash and this batch is the moment it gets swapped out.
func (g graphModel) handleAppended(m commitsAppendedMsg) (graphModel, tea.Cmd) {
	if !g.loaded || g.pendingSwap {
		swap := g.pendingSwap
		g.pendingSwap = false
		// Decoration state describes the old window — clear it the same
		// way ResetForReload would have, then let captureHeadRow /
		// applyHeadDim rebuild it from the fresh rows.
		g.headRowIndex = -1
		g.headAncestors = nil
		g.headDimDirty = true
		items := appendCommitItems(make([]list.Item, 0, len(m.rows)), m.rows)
		g.captureHeadRow(m.rows, 0)
		g.applyGraphCap()
		g.applyHeadDim()
		setCmd := g.list.SetItems(items)
		if swap {
			// Same cursor semantics as the hard reset: start at the top;
			// tryHEADJump may still move it afterwards.
			g.list.Select(0)
		}
		g.loaded = true
		g.streaming = !m.done
		g.err = nil
		cmds := []tea.Cmd{setCmd}
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
	items = appendCommitItems(items, m.rows)
	g.captureHeadRow(m.rows, prevLen)
	g.applyHeadDim()
	setCmd := g.list.SetItems(items)
	g.streaming = !m.done

	cmds := []tea.Cmd{setCmd}
	if atTail {
		g.list.Select(len(items) - 1)
	}
	if !m.done && m.next != nil {
		cmds = append(cmds, m.next)
	}
	return g, tea.Batch(cmds...)
}

// captureHeadRow scans an appended batch for the HEAD commit and records
// its absolute list index. baseIndex is where this batch lands in the
// list (0 for the first batch, prevLen for appends). Detached HEAD lands
// as `headDetached`; named HEAD as a `Ref.IsHead` flag from the same
// ParseDecoration call.
func (g *graphModel) captureHeadRow(rows []graphRow, baseIndex int) {
	if g.headRowIndex >= 0 {
		return
	}
	for i, r := range rows {
		refs, headDetached := git.ParseDecoration(r.commit.RefNames)
		if headDetached || hasIsHead(refs) {
			g.headRowIndex = baseIndex + i
			g.headDimDirty = true
			return
		}
	}
}

func hasIsHead(refs []git.DecoratedRef) bool {
	for _, r := range refs {
		if r.IsHead {
			return true
		}
	}
	return false
}

func (g graphModel) View() string {
	if !g.loaded {
		return centerPlaceholder(g.width, g.height, loadingPlaceholder(g.spinnerFrame))
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

// applyGraphCap derives the per-row hard cap from the current pane width
// via laneColCap and pushes it into the delegate. Per-row tight rendering
// means rows whose prefix is narrower than the cap render at their own
// width — the cap only kicks in to truncate prefixes that exceed it.
func (g *graphModel) applyGraphCap() {
	cap := 0
	if g.width > 0 {
		cap = laneColCap(g.width)
	}
	if cap == g.graphWidth {
		return
	}
	g.graphWidth = cap
	g.delegate.graphWidth = cap
	g.list.SetDelegate(g.delegate)
}

// applyHeadDim copies graphModel's dim state into the delegate. The
// caller marks state dirty by setting headDimDirty; otherwise this is a
// cheap no-op so streaming batches that don't move the boundary don't
// re-layout the list.
func (g *graphModel) applyHeadDim() {
	if !g.headDimDirty {
		return
	}
	g.delegate.headRowIndex = g.headRowIndex
	g.delegate.headAncestors = g.headAncestors
	g.list.SetDelegate(g.delegate)
	g.headDimDirty = false
}

// SetPRs pushes the head-branch → open-PR map into the delegate so chip
// rendering can badge branch tips. Called from Model.Update on
// prsLoadedMsg; survives ResetForReload on purpose (see commitDelegate).
func (g *graphModel) SetPRs(prs map[string]prInfo) {
	g.delegate.prs = prs
	g.list.SetDelegate(g.delegate)
}

// SetHeadAncestors stores the HEAD-reachable hash set. Called from
// Model.Update on headAncestorsLoadedMsg.
func (g *graphModel) SetHeadAncestors(ancestors map[string]struct{}) {
	g.headAncestors = ancestors
	g.headDimDirty = true
	g.applyHeadDim()
}

// MarkStaleForReload arms the soft reload path: the current graph keeps
// rendering (no blank flash) and the next stream's first batch swaps it
// out — see pendingSwap. No-op before the first load; the fresh-load
// branch of handleAppended covers that case on its own.
func (g *graphModel) MarkStaleForReload() {
	if !g.loaded {
		return
	}
	g.pendingSwap = true
	g.userHasMoved = false
}

// ResetForReload clears state so View renders the "loading…" placeholder
// again. This is the hard variant — reserved for reloads where showing the
// old graph would mislead (worktree switch: different tree entirely);
// every other reload goes through MarkStaleForReload. The returned cmd is
// non-nil only when a list filter is active (filter rebuild) — callers
// should batch it with the new load cmd.
func (g *graphModel) ResetForReload() tea.Cmd {
	g.loaded = false
	g.pendingSwap = false
	g.streaming = false
	g.userHasMoved = false
	g.err = nil
	g.graphWidth = 0
	g.delegate.graphWidth = 0
	g.headRowIndex = -1
	g.headAncestors = nil
	g.headDimDirty = false
	g.delegate.headRowIndex = -1
	g.delegate.headAncestors = nil
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

// CommitByHash returns the loaded commit matching hash without moving the
// cursor. Returns ok=false when the hash isn't in the loaded window.
func (g graphModel) CommitByHash(hash string) (git.Commit, bool) {
	for _, it := range g.list.Items() {
		ci, ok := it.(commitItem)
		if !ok {
			continue
		}
		if ci.c.Hash == hash {
			return ci.c, true
		}
	}
	return git.Commit{}, false
}
