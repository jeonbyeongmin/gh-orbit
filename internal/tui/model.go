// Package tui hosts the Bubble Tea models, panes, and key bindings for the
// Fork-style layout (refs sidebar · commit graph on top · tab area on bottom).
package tui

import (
	"context"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// clipboardWrite is the package-level seam for OS clipboard writes. Tests
// swap it with an in-memory buffer; production code defaults to atotto's
// platform-specific implementation (pbcopy on macOS, xclip/xsel on Linux,
// Win32 on Windows).
var clipboardWrite = clipboard.WriteAll

type pane int

const (
	paneRefs pane = iota
	paneGraph
	paneTab
	paneCount
)

// splitRatioDefault, splitRatioMin, splitRatioMax bound the graph/tab vertical
// split. ctrl+up / ctrl+down step by 5 inside [min, max].
const (
	splitRatioDefault = 60
	splitRatioMin     = 20
	splitRatioMax     = 80
	splitRatioStep    = 5
)

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
	changes       changesModel
	commitDetail  commitDetailModel
	tabs          tabsModel
	// splitRatio is the percentage of the right-column height allocated to the
	// graph; the tab area takes the remainder. Bounded by splitRatioMin/Max.
	splitRatio int
	// diffReqID counts every diff dispatch (cursor change, `d` press). Stale
	// in-flight git show responses compare their reqID against this and drop
	// themselves if they no longer match.
	diffReqID uint64
	// streamReqID counts every LogStream dispatch (initial load + each
	// reloadCmd). Stale stream messages from the previous reload compare
	// reqID against this and drop themselves. Independent of diffReqID:
	// diff debounce and graph reload progress on separate cadences.
	streamReqID uint64
	// streamCancel is the cancel handle of the most recent LogStream. r and
	// q/ctrl+c invoke it so the git process is reaped instead of leaking.
	streamCancel context.CancelFunc
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
		focused:      paneGraph,
		refs:         newRefsModel(),
		graph:        newGraphModel(),
		diff:         newDiffModel(),
		changes:      newChangesModel(),
		commitDetail: newCommitDetailModel(),
		tabs:         newTabsModel(),
		splitRatio:   splitRatioDefault,
		currentRefs:  []string{refsAllSentinel},
		streamReqID:  1,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		loadCommitsCmd("", m.currentRefs, m.streamReqID),
		loadRefsCmd(""),
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Terminals re-emit WindowSizeMsg on focus changes / SIGWINCH bursts.
		// Skip the SetSize cascade when nothing actually changed so the
		// patch viewport doesn't re-wrap a multi-MB diff every burst.
		if msg.Width == m.width && msg.Height == m.height {
			return m, nil
		}
		m.width, m.height = msg.Width, msg.Height
		m.applyPaneSizes()
		if m.mode == viewModeDiffWindow {
			m.diff.SetPatchViewportSize(m.width, m.height-1)
		}
		return m, nil

	case commitsStreamStartedMsg:
		if msg.reqID != m.streamReqID {
			// Stale — its reload was superseded. Cancel the orphaned
			// producer immediately so its git process doesn't leak.
			if msg.cancel != nil {
				msg.cancel()
			}
			return m, nil
		}
		m.streamCancel = msg.cancel
		var cmd tea.Cmd
		m.graph, cmd = m.graph.Update(msg)
		return m, cmd

	case commitsAppendedMsg:
		if msg.reqID != m.streamReqID {
			return m, nil
		}
		var cmd tea.Cmd
		m.graph, cmd = m.graph.Update(msg)
		return m, cmd

	case commitsStreamDoneMsg:
		if msg.reqID != m.streamReqID {
			return m, nil
		}
		m.streamCancel = nil
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
		// reqID and the latest tick wins. Stat (Changes) and metadata
		// (Commit) fan out from the same tick so a fast j-mash spawns one
		// pair of git processes per stop, not one per cursor row.
		if msg.reqID != m.diffReqID {
			return m, nil
		}
		return m, tea.Batch(
			loadDiffStatCmd("", msg.hash, msg.reqID),
			loadCommitDetailCmd("", msg.hash, msg.reqID),
		)

	case diffStatLoadedMsg:
		if msg.reqID != m.diffReqID {
			return m, nil
		}
		return m, m.changes.SetFiles(msg.hash, msg.files)
	case diffStatFailedMsg:
		if msg.reqID != m.diffReqID {
			return m, nil
		}
		m.changes.ApplyStatFailed(msg.hash, msg.err)
		return m, nil
	case filePatchLoadedMsg:
		m.changes.ApplyFilePatchLoaded(msg.reqID, msg.hash, msg.path, msg.text)
		return m, nil
	case filePatchFailedMsg:
		m.changes.ApplyFilePatchFailed(msg.reqID, msg.hash, msg.path, msg.err)
		return m, nil
	case commitDetailLoadedMsg:
		m.commitDetail.ApplyDetailLoaded(msg.reqID, msg.hash, msg.detail)
		return m, nil
	case commitDetailFailedMsg:
		m.commitDetail.ApplyDetailFailed(msg.reqID, msg.hash, msg.err)
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
		// The viewMode guard runs before the global ctrl+c/q quit branch so
		// `q` inside the overlay closes the overlay instead of killing the app.
		if m.mode == viewModeDiffWindow {
			switch msg.String() {
			case "esc", "q":
				m.mode = viewModeNormal
				m.diff.ClosePatch()
				return m, nil
			case "ctrl+c":
				m.cancelStream()
				return m, tea.Quit
			case "j", "k", "down", "up", "pgdown", "pgup":
				return m, m.diff.ScrollPatch(msg)
			}
			return m, nil
		}
		switch msg.String() {
		case "ctrl+c", "q":
			m.cancelStream()
			return m, tea.Quit
		case "tab":
			m.focused = (m.focused + 1) % paneCount
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
		case "ctrl+up":
			m.adjustSplit(-splitRatioStep)
			return m, nil
		case "ctrl+down":
			m.adjustSplit(splitRatioStep)
			return m, nil
		case "y":
			m = m.copyHashFromCommitTab()
			return m, nil
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
		case paneTab:
			switch msg.String() {
			case "h", "left":
				m.tabs.Prev()
				return m, nil
			case "l", "right":
				m.tabs.Next()
				return m, nil
			case "ctrl+d", "ctrl+u":
				// Changes-tab patch viewport only — Commit tab intentionally
				// no-ops on these so the bindings stay unambiguous between
				// the two tabs (viewport's default keymap would otherwise
				// claim them).
				if m.tabs.Active() == tabChanges {
					m.changes.ScrollPatch(msg)
				}
				return m, nil
			case "j", "k", "down", "up", "pgdown", "pgup":
				// Commit tab scrolls its body viewport; Changes tab moves
				// the file-list cursor (which triggers a fresh patch load
				// inside changesModel.Update). pgdown/pgup live here, not
				// with ctrl+d/u above, because the Commit tab is supposed
				// to honor them too.
				if m.tabs.Active() == tabCommit {
					return m, m.commitDetail.ScrollContent(msg)
				}
				var cmd tea.Cmd
				m.changes, cmd = m.changes.Update(msg)
				return m, cmd
			case "g", "G":
				// Commit tab: jump-to-top/bottom of the viewport. Changes
				// tab: jump file-list cursor to first/last entry.
				if m.tabs.Active() == tabCommit {
					return m, m.commitDetail.ScrollContent(msg)
				}
				var cmd tea.Cmd
				m.changes, cmd = m.changes.Update(msg)
				return m, cmd
			}
		}
	}
	return m, nil
}

// applyPaneSizes recomputes the inner content dimensions for every sub-model
// from the current width/height/splitRatio.
func (m *Model) applyPaneSizes() {
	s := m.paneSizes()
	m.refs.SetSize(s.refsW, s.refsH)
	m.graph.SetSize(s.graphW, s.graphH)
	m.diff.SetSize(s.tabW, s.tabH)
	// tabBody renders header + spacer (2 lines) above the tab content.
	tabBodyH := s.tabH - 2
	if tabBodyH < 1 {
		tabBodyH = 1
	}
	m.changes.SetSize(s.tabW, tabBodyH)
	m.commitDetail.SetSize(s.tabW, tabBodyH)
}

// adjustSplit nudges the graph/tab split ratio by delta percent and reflows
// the panes if the value actually moved (clamped to [splitRatioMin, Max]).
func (m *Model) adjustSplit(delta int) {
	next := m.splitRatio + delta
	if next < splitRatioMin {
		next = splitRatioMin
	} else if next > splitRatioMax {
		next = splitRatioMax
	}
	if next == m.splitRatio {
		return
	}
	m.splitRatio = next
	m.applyPaneSizes()
}

// copyHashFromCommitTab handles `y`: only acts when paneTab is focused and
// the Commit tab is the active sub-tab; surfaces success ("copied <short>")
// or the OS error (typical: xclip/xsel missing on Linux) through the status
// line so the user always knows whether the clipboard was actually written.
func (m Model) copyHashFromCommitTab() Model {
	if m.focused != paneTab || m.tabs.Active() != tabCommit {
		return m
	}
	hash := m.commitDetail.CurrentHash()
	if hash == "" {
		return m
	}
	if err := clipboardWrite(hash); err != nil {
		m.status = "clipboard unavailable: " + firstLine(err.Error())
		m.statusStyle = statusErrS
		return m
	}
	m.status = "copied " + shortHash(hash)
	m.statusStyle = statusOkS
	return m
}

// beginDiffStat advances the request id, marks both Changes and Commit
// panes loading for the given hash, and schedules a single debounced tick
// that fans out to the stat (Changes) and metadata (Commit) git calls. Used
// by graph cursor moves (commitSelectedMsg) and ref-tip jumps
// (refSelectedMsg).
func (m *Model) beginDiffStat(hash string) tea.Cmd {
	m.diffReqID++
	m.changes.MarkPending(hash)
	m.commitDetail.MarkLoading(hash, m.diffReqID)
	return scheduleDiffStatCmd(m.diffReqID, hash)
}

// cancelStream invokes the active LogStream's cancel handle (if any) and
// clears the slot. Safe to call when no stream is in flight.
func (m *Model) cancelStream() {
	if m.streamCancel != nil {
		m.streamCancel()
		m.streamCancel = nil
	}
}

// reloadCmd resets both panes to their loading state and dispatches fresh
// log + refs queries. A stale ref in m.currentRefs surfaces via the new
// stream's commitsStreamDoneMsg.err.
func (m *Model) reloadCmd() tea.Cmd {
	m.cancelStream()
	m.streamReqID++
	resetCmd := m.graph.ResetForReload()
	m.refs.ResetForReload()
	return tea.Batch(
		resetCmd,
		loadCommitsCmd("", m.currentRefs, m.streamReqID),
		loadRefsCmd(""),
	)
}

// paneSizes holds the inner content dimensions for each rendered box. The
// outer (bordered) widths/heights are content + 2 along each axis. The new
// layout stacks graph above the tab area in the right column; refs is a
// full-height left sidebar.
type paneSizes struct {
	refsW, refsH   int
	graphW, graphH int
	tabW, tabH     int
}

func (m Model) paneSizes() paneSizes {
	var s paneSizes
	if m.width == 0 || m.height == 0 {
		return s
	}
	// Reserve 1 row for the help line.
	mainH := m.height - 1
	if mainH < 1 {
		mainH = 1
	}
	// refs sidebar gets ~20% of total width, right column the rest. Each box
	// claims 2 cols of border around its content.
	refsOuterW := m.width * 20 / 100
	if refsOuterW < 12 {
		refsOuterW = 12
	}
	if refsOuterW > m.width-12 {
		refsOuterW = m.width - 12
	}
	rightOuterW := m.width - refsOuterW

	s.refsW = refsOuterW - 2
	s.refsH = mainH - 2
	if s.refsW < 1 {
		s.refsW = 1
	}
	if s.refsH < 1 {
		s.refsH = 1
	}

	// Right column: vertical split between graph (top) and tab (bottom). Each
	// has its own bordered box, so subtract 2 rows per box for borders.
	graphOuterH := mainH * m.splitRatio / 100
	if graphOuterH < 3 {
		graphOuterH = 3
	}
	if graphOuterH > mainH-3 {
		graphOuterH = mainH - 3
	}
	tabOuterH := mainH - graphOuterH

	s.graphW = rightOuterW - 2
	s.tabW = rightOuterW - 2
	if s.graphW < 1 {
		s.graphW = 1
	}
	if s.tabW < 1 {
		s.tabW = 1
	}
	s.graphH = graphOuterH - 2
	s.tabH = tabOuterH - 2
	if s.graphH < 1 {
		s.graphH = 1
	}
	if s.tabH < 1 {
		s.tabH = 1
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
	helpTextNormal     = "tab focus · h/l switch tab · j/k navigate · ctrl+↑/↓ resize · enter jump ref · y copy hash · d patch · F fetch · r reload · q quit"
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

	refsBox := boxStyle(m.focused == paneRefs).Width(s.refsW).Height(s.refsH).Render(m.refs.View())
	graphBox := boxStyle(m.focused == paneGraph).Width(s.graphW).Height(s.graphH).Render(m.graph.View())
	tabBox := boxStyle(m.focused == paneTab).Width(s.tabW).Height(s.tabH).Render(m.tabBody())

	rightCol := lipgloss.JoinVertical(lipgloss.Left, graphBox, tabBox)
	main := lipgloss.JoinHorizontal(lipgloss.Top, refsBox, rightCol)
	return lipgloss.JoinVertical(lipgloss.Left, main, m.renderHelpStatus())
}

func boxStyle(focused bool) lipgloss.Style {
	if focused {
		return borderFocused
	}
	return borderUnfocused
}

// tabBody renders the active tab's body underneath the tabsModel header.
func (m Model) tabBody() string {
	header := m.tabs.HeaderView()
	var body string
	switch m.tabs.Active() {
	case tabCommit:
		body = m.commitDetail.View()
	case tabChanges:
		body = m.changes.View()
	}
	return header + "\n\n" + body
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
