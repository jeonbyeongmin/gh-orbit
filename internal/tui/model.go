// Package tui hosts the Bubble Tea models, panes, and key bindings for the
// Fork-style layout (refs sidebar · commit graph on top · tab area on bottom).
package tui

import (
	"context"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"github.com/jeonbyeongmin/gh-orbit/internal/config"
	"github.com/jeonbyeongmin/gh-orbit/internal/git"
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

// pendingHEADSentinel marks pendingHEADHash as "armed but the real hash
// hasn't arrived yet". Replaced with HEAD's commit hash by the next
// refsLoadedMsg. Picks an obviously-not-a-hash value so a misbehaving
// refsLoadedMsg can never accidentally collide.
const pendingHEADSentinel = "<pending>"

// viewMode toggles between the 3-pane layout and the full-screen patch overlay
// that `d` opens. graph cursor state is preserved across the toggle so esc
// returns the user to exactly where they were.
type viewMode int

const (
	viewModeNormal viewMode = iota
	viewModeDiffWindow
	// viewModeCheckoutConfirm gates the screen on a "stash & checkout vs.
	// abort" prompt while pendingCheckout holds the ref the user picked.
	// The 3-pane layout stays visible underneath (so the user keeps their
	// context); only the help/status line below switches to the choice
	// keys, and every key except s/a/esc/ctrl+c is swallowed.
	viewModeCheckoutConfirm
)

// pendingCheckout remembers what the user was trying to check out so the
// "[s]tash & checkout" branch in the confirm modal can re-issue the same
// request after stashing. detached=true means graph 'C' (CheckoutDetached);
// detached=false means a refs-pane Enter (named ref).
type pendingCheckout struct {
	ref      string
	detached bool
}

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
	// pullInFlight gates the P key. Tracked separately from fetchInFlight so
	// F + P can run in parallel; git's own .git/index.lock is the real
	// serialization point.
	pullInFlight bool
	// pullPrefStrategy is the user's preferred pull strategy ("ff-only" /
	// "merge" / "rebase" / ""). Loaded once in New() from ConfigPath; an
	// empty string means "let git config / final fallback decide".
	pullPrefStrategy string
	// pendingHEADHash drives the post-pull cursor jump. pullSucceededMsg
	// arms it with the sentinel pendingHEADSentinel; the post-reload
	// refsLoadedMsg replaces the sentinel with HEAD's actual hash;
	// tryHEADJump (called from both refsLoadedMsg and commitsStreamDoneMsg)
	// clears it once the row lands. Empty string means "no pending jump".
	pendingHEADHash string
	// status is the one-line message rendered next to the help line:
	// "fetching…", "fetch: done", "fetch failed: …". Empty hides it.
	// statusStyle decides the color; zero value renders without color.
	status      string
	statusStyle lipgloss.Style
	// checkoutInFlight gates Enter on the refs pane and 'C' on the graph
	// while a background checkout is running. fetch/pull have their own
	// gates; git's .git/index.lock is the real serialization point.
	checkoutInFlight bool
	// pendingCheckout is set the moment beginCheckout fires so the dirty-
	// tree confirm modal can re-issue the same request (with stashing) on
	// 's', or drop the slot on 'a'/esc.
	pendingCheckout pendingCheckout
}

func New() Model {
	m := Model{
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
	prefs, err := config.LoadPrefs()
	if err != nil {
		// Non-fatal — pull falls back to git config / ff-only. Surface once
		// so the user knows the file isn't being honored.
		m.status = "prefs load failed: " + firstLine(err.Error())
		m.statusStyle = statusErrS
	} else {
		m.pullPrefStrategy = prefs.Pull.Strategy
	}
	return m
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
		m = m.tryHEADJump()
		return m, cmd

	case refsLoadedMsg, refsLoadFailedMsg:
		if loaded, ok := msg.(refsLoadedMsg); ok && m.pendingHEADHash == pendingHEADSentinel {
			for _, r := range loaded.refs {
				if r.IsHead {
					m.pendingHEADHash = r.ObjectName
					break
				}
			}
			m = m.tryHEADJump()
		}
		var cmd tea.Cmd
		m.refs, cmd = m.refs.Update(msg)
		return m, cmd

	case refCheckoutRequestedMsg:
		var cmd tea.Cmd
		m, cmd = m.beginCheckout(git.CheckoutTarget(msg.ref), false)
		return m, cmd

	case checkoutSucceededMsg:
		m.checkoutInFlight = false
		m.pendingCheckout = pendingCheckout{}
		m.status = checkoutLabel(msg.ref, msg.detached)
		m.statusStyle = statusOkS
		m.pendingHEADHash = pendingHEADSentinel
		return m, m.reloadCmd()

	case checkoutNeedsCleanTreeMsg:
		// Modal owns the next decision; release the in-flight gate so
		// 's' → stashThenCheckoutCmd can re-arm it without colliding.
		// pendingCheckout stays intact so 's' can re-issue the same request.
		m.checkoutInFlight = false
		m.mode = viewModeCheckoutConfirm
		m.status = "checkout: " + msg.ref + " — uncommitted changes"
		m.statusStyle = statusErrS
		return m, nil

	case checkoutFailedMsg:
		m.checkoutInFlight = false
		m.pendingCheckout = pendingCheckout{}
		m.status = "checkout failed: " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil

	case stashThenCheckoutMsg:
		m.checkoutInFlight = false
		m.pendingCheckout = pendingCheckout{}
		m.status = checkoutLabel(msg.ref, msg.detached) +
			" (stashed before checkout: " + msg.stashLabel + ")"
		m.statusStyle = statusOkS
		m.pendingHEADHash = pendingHEADSentinel
		return m, m.reloadCmd()

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
		// While a pull is still in flight, its "pulling…" status outranks
		// fetch's outcome and the pending pullSucceededMsg / pullConflictMsg
		// will reload. Skip status overwrite + the redundant reload.
		if m.pullInFlight {
			return m, nil
		}
		m.status = "fetch: done"
		m.statusStyle = statusOkS
		return m, m.reloadCmd()

	case fetchFailedMsg:
		m.fetchInFlight = false
		if m.pullInFlight {
			return m, nil
		}
		m.status = "fetch failed: " + msg.err.Error()
		m.statusStyle = statusErrS
		return m, nil

	case pullSucceededMsg:
		m.pullInFlight = false
		m.status = "pull: done"
		m.statusStyle = statusOkS
		m.pendingHEADHash = pendingHEADSentinel
		return m, m.reloadCmd()

	case pullConflictMsg:
		m.pullInFlight = false
		m.status = "pull: CONFLICT — resolve in your terminal"
		m.statusStyle = statusErrS
		// Reload so refs (HEAD may now sit on a half-merged commit) and the
		// graph reflect post-pull state. No HEAD jump — user is mid-conflict.
		return m, m.reloadCmd()

	case pullFailedMsg:
		m.pullInFlight = false
		m.status = "pull failed: " + firstLine(msg.err.Error())
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
		if m.mode == viewModeCheckoutConfirm {
			switch msg.String() {
			case "s":
				ref, detached := m.pendingCheckout.ref, m.pendingCheckout.detached
				m.mode = viewModeNormal
				m.checkoutInFlight = true
				m.status = "checkout: " + ref + " (stashing…)"
				m.statusStyle = statusBusyS
				return m, stashThenCheckoutCmd("", ref, detached)
			case "a", "esc":
				m.mode = viewModeNormal
				m.pendingCheckout = pendingCheckout{}
				m.status = "checkout: aborted"
				m.statusStyle = statusOkS
				return m, nil
			case "ctrl+c":
				m.cancelStream()
				return m, tea.Quit
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
		case "P":
			if m.pullInFlight {
				return m, nil
			}
			m.pullInFlight = true
			m.status = "pulling…"
			m.statusStyle = statusBusyS
			return m, pullCmd("", m.pullPrefStrategy)
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
		case "C":
			// Gate on graph focus so a stray 'C' on the refs pane doesn't detach.
			if m.focused != paneGraph {
				return m, nil
			}
			c, ok := m.graph.Selected()
			if !ok {
				return m, nil
			}
			var cmd tea.Cmd
			m, cmd = m.beginCheckout(c.Hash, true)
			return m, cmd
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
			case "j", "k", "down", "up", "pgdown", "pgup", "g", "G":
				// Commit tab → body viewport scroll; Changes tab → file-list
				// cursor (which loads a fresh patch inside changesModel.Update).
				// pgdown/pgup live here rather than with ctrl+d/u above so the
				// Commit tab honors them too.
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

// beginCheckout dispatches a checkout for ref and arms checkoutInFlight +
// pendingCheckout. The dirty-tree confirm modal needs pendingCheckout
// later if git rejects, so we set it before the cmd fires. Repeat presses
// while a checkout is in flight are dropped — git holds index.lock and a
// second invocation would just block.
func (m Model) beginCheckout(ref string, detached bool) (Model, tea.Cmd) {
	if m.checkoutInFlight {
		return m, nil
	}
	m.checkoutInFlight = true
	m.pendingCheckout = pendingCheckout{ref: ref, detached: detached}
	m.status = checkoutLabel(ref, detached) + " …"
	m.statusStyle = statusBusyS
	return m, checkoutCmd("", ref, detached)
}

// checkoutLabel renders the user-facing "checkout: …" prefix shared by the
// busy / success / stash-then-success status lines. Detached checkouts
// short-hash the ref since the user picked a commit, not a name.
func checkoutLabel(ref string, detached bool) string {
	if detached {
		return "checkout: detached at " + shortHash(ref)
	}
	return "checkout: " + ref
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

// tryHEADJump attempts to point the graph cursor at HEAD using the hash
// captured from the post-pull refsLoadedMsg. JumpToHash returns false until
// the matching commit has actually streamed in, so the caller invokes this
// from both refsLoadedMsg and commitsStreamDoneMsg — whichever arrives
// second wins. Empty / sentinel pendingHEADHash → no-op.
func (m Model) tryHEADJump() Model {
	if m.pendingHEADHash == "" || m.pendingHEADHash == pendingHEADSentinel {
		return m
	}
	if m.graph.JumpToHash(m.pendingHEADHash) {
		m.pendingHEADHash = ""
	}
	return m
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
	helpTextNormal     = "tab focus · h/l switch tab · j/k nav · ctrl+↑/↓ resize · enter checkout · o jump ref · C detach · y copy · d patch · F fetch · P pull · r reload · q quit"
	helpTextDiffWindow = "j/k scroll · pgup/pgdn page · esc/q close"
)

// helpRendered is the styled help line. The two help strings are const, so we
// render once at package init instead of every View() frame.
var (
	helpRenderedNormal     = help.Render(helpTextNormal)
	helpRenderedDiffWindow = help.Render(helpTextDiffWindow)
)

// confirmPromptS reuses the busy color and adds bold so the modal prompt
// reads as "active dialog" rather than "an error just landed".
var confirmPromptS = statusBusyS.Bold(true)

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
// The dirty-tree checkout modal replaces the whole line with its prompt
// so the available choice keys are unambiguous.
func (m Model) renderHelpStatus() string {
	if m.mode == viewModeCheckoutConfirm {
		return confirmPromptS.Render(
			"Uncommitted changes — checkout '" + m.pendingCheckout.ref +
				"'? · [s] stash & checkout · [a] abort · [esc] cancel",
		)
	}
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
