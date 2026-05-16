// Package tui hosts the Bubble Tea models, panes, and key bindings for the
// Fork-style layout (refs sidebar · commit graph on top · tab area on bottom).
package tui

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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
	// viewModeHelp expands the bottom area into a multi-line help panel.
	// The 3-pane layout stays visible above; only the bottom shrinks the
	// main area to make room. All other shortcuts keep working while the
	// panel is open — `?` re-toggles, q quits, j/k navigate, etc. — so the
	// expanded panel functions as a reference, not a modal.
	viewModeHelp
	// viewModeBranchPicker gates the screen on a "pick which local branch"
	// modal triggered when the graph Enter evaluator returns multiple
	// chips at the cursor row (graphActionPicker). The 3-pane layout stays
	// visible underneath; only j/k/enter/esc are accepted while open.
	viewModeBranchPicker
	// viewModeRefNameInput hosts the create / rename name-entry modal. The
	// 3-pane layout stays visible above; only the bottom panel takes typed
	// keys via bubbles/textinput. Enter validates with check-ref-format and
	// dispatches the create or rename cmd; esc cancels.
	viewModeRefNameInput
	// viewModeRefDeleteConfirm hosts the 4-axis delete confirm modal. The
	// hint row's available keys depend on whether a matching local +
	// remote pair exists for the cursor — y/Y/f/F covers the local+remote
	// matrix; remote-only cursors show just y. esc cancels.
	viewModeRefDeleteConfirm
	// viewModeStashActionPicker gates the screen on the graph-Enter stash
	// modal: `[p] pop / [a] apply / [esc] cancel`. Drop is intentionally
	// excluded here — destructive remove goes through the refs-pane `d`
	// drop confirm so the user always sees a "drop?" prompt before losing
	// an entry. Every key outside p/a/esc/ctrl+c is swallowed.
	viewModeStashActionPicker
	// viewModeStashDropConfirm gates the refs-pane `d` stash modal:
	// `[y] drop / [esc] cancel`. Single key by design — stash drop has no
	// force / multi-axis variants, so a 4-axis matrix would be noise.
	viewModeStashDropConfirm
	// viewModeLocalChanges replaces the right column (graph + tab) with a
	// file-tree + diff layout for working-tree work. refs sidebar stays
	// put so the branch context is unchanged across the toggle. Entered
	// via the `,` keybind or by `enter` on the sticky `● Local Changes`
	// row in the refs pane.
	viewModeLocalChanges
)

// helpExpandedHeight is the row count reserved for the bottom area when
// the `?` help panel is open. Each helpData category renders as a 1-line
// header + 1-line entries row, so 5 categories × 2 rows = 10. paneSizes
// clamps this on small terminals.
const helpExpandedHeight = 10

// pendingCheckout remembers what the user was trying to check out so the
// "[s]tash & checkout" branch in the confirm modal can re-issue the same
// request after stashing. detached=true means graph Enter resolved to
// detach (CheckoutDetached); detached=false means a refs-pane Enter
// (named ref) or a graph-Enter checkout to a chip-bearing branch.
//
// withPull and skipReason carry the refs-pane `p` chain's state across
// the dirty-tree confirm modal. Zero values mean "plain checkout" —
// refCheckoutRequestedMsg (Enter) leaves them off, so the existing
// behavior is unchanged. refCheckoutWithPullRequestedMsg (`p`) sets
// withPull=true and stamps a non-empty skipReason for tag / detached /
// no-upstream local refs (where pull will be elided); the modal's `s`
// branch consults withPull to pick stashThenCheckoutThenPullThenPopCmd
// vs. the legacy stashThenCheckoutCmd. skipReason being non-empty is
// the single signal of "pull will be skipped" — the chain commands
// derive their skip flag from that, no parallel boolean needed.
//
// withFF / withCheckoutFF / ffHash flag the graph-Enter FF paths so the
// dirty-tree confirm modal can route `s` to the right chain. ref carries
// the local-branch name; ffHash carries the cursor commit MergeFFOnly
// should advance to.
//
//   - withFF: HEAD is already on ref. Modal `s` → stashThenFFCmd
//     (no checkout step).
//   - withCheckoutFF: HEAD is on a different branch (cross-branch case
//     from a remote chip). Modal `s` → stashThenCheckoutThenFFCmd
//     (checkout ref then FF).
//
// Both are mutually exclusive with withPull (different keys originate
// the chains).
type pendingCheckout struct {
	ref            string
	detached       bool
	withPull       bool
	skipReason     string
	withFF         bool
	withCheckoutFF bool
	ffHash         string
}

type Model struct {
	// workdir is the absolute path of the git working tree every wrapper
	// invocation runs against. Seeded from os.Getwd() in New(); the worktree
	// modal rewrites it on switch so all subsequent loadRefs / loadCommits /
	// fetch / status / checkout / branch-write cmds re-target the new tree
	// without per-call site changes. Empty string falls back to the process
	// cwd (git's default `cmd.Dir == ""` behavior) — the New() Getwd
	// fallback path leans on this.
	workdir       string
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
	// checkoutInFlight gates Enter on the refs pane and Enter on the graph
	// while a background checkout is running. fetch/pull have their own
	// gates; git's .git/index.lock is the real serialization point.
	checkoutInFlight bool
	// pendingCheckout is set the moment beginCheckout fires so the dirty-
	// tree confirm modal can re-issue the same request (with stashing) on
	// 's', or drop the slot on 'a'/esc.
	pendingCheckout pendingCheckout
	// actionInFlight gates graph-pane Enter while evaluateGraphActionCmd
	// is resolving the cursor's chip / ancestry state. Released by the
	// graphActionMsg handler before any follow-up cmd is dispatched —
	// downstream gates (checkoutInFlight / ffInFlight) take over from
	// there. A second Enter while resolving is swallowed.
	actionInFlight bool
	// ffInFlight gates graph Enter while ffOnlyCmd / stashThenFFCmd is
	// running. Tracked separately from checkoutInFlight so the FF and
	// checkout chains can't collide on a status overwrite. Cleared by
	// ffSucceededMsg / ffFailedMsg / ffNeedsCleanTreeMsg / stashThenFFMsg.
	ffInFlight bool
	// branchPicker backs viewModeBranchPicker. Reset to the zero value on
	// esc / enter; the picker reads candidates+cursor while open and
	// dispatches a graph-Enter checkout on enter.
	branchPicker branchPickerState
	// refNameInput backs viewModeRefNameInput (create / rename modal).
	// Reset to the zero value on esc / success; while open the textinput
	// owns key routing for typed characters and the model handles
	// enter / esc / validation.
	refNameInput refNameInputState
	// pendingRefDelete backs viewModeRefDeleteConfirm. Stamped on `d`
	// keypress with the cursor's local/remote matching state so the modal
	// renderer + key router can branch off the flags without re-deriving
	// from refs.
	pendingRefDelete refDeleteState
	// pendingRefCursorName carries the new ref name across a refs reload
	// after create / rename success so the post-reload refsLoadedMsg can
	// move the cursor onto the new row. Empty string means no jump.
	pendingRefCursorName string
	// pendingRefCursorAfterDelete carries the deleted ref name + section
	// across a refs reload after delete success so the cursor lands on the
	// next (or previous, if last) ref in the same section. Zero name means
	// no adjustment.
	pendingRefCursorAfterDelete deletedRefHandle
	// pendingRefCursorPersist is the "previous cursor position snapshot"
	// reloadCmd writes just before every reload, so the post-reload
	// refsLoadedMsg can restore the cursor onto the same ref after the
	// refModel's cursor=0 reset. Only consumed when the higher-priority
	// pendingRefCursorName / pendingRefCursorAfterDelete are absent. Zero
	// value = no persist (detached HEAD / empty ref set).
	pendingRefCursorPersist persistedRefHandle
	// refActionInFlight gates the n / d / m keys while a branch-write cmd
	// is running. Distinct from checkoutInFlight so a stuck refs write
	// can't deadlock checkout / pull / FF chains.
	refActionInFlight bool
	// currentStashHashes / currentStashByHash snapshot the stash set the
	// running stream was started with. refsLoadedMsg diffs against these
	// and triggers reloadCmd only when the set differs — the reloadCmd's
	// own refsLoadedMsg sees the same set, diff is empty, no loop.
	currentStashHashes []string
	currentStashByHash map[string]string
	pendingStashAction pendingStashAction
	pendingStashDrop   pendingStashDrop
	// localChanges hosts the file-tree + diff viewport that the right
	// column renders when mode == viewModeLocalChanges. The graph / tab
	// models are left untouched across the toggle so exiting the mode
	// snaps back to the exact previous state.
	localChanges localChangesModel
	// localChangesReqID counts every diff dispatch inside the Local
	// Changes mode. ApplyDiffLoaded compares against this + (path,
	// staged) to drop stale responses when the user keeps moving the
	// cursor mid-load.
	localChangesReqID uint64
}

// pendingStashAction backs viewModeStashActionPicker. subject is the
// stash commit's git log subject, best-effort from the graph window —
// empty when the stash hash isn't yet streamed in.
type pendingStashAction struct {
	label   string
	hash    string
	subject string
}

type pendingStashDrop struct {
	label   string
	subject string
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
		localChanges: newLocalChangesModel(),
		splitRatio:   splitRatioDefault,
		currentRefs:  []string{refsAllSentinel},
		streamReqID:  1,
	}
	if wd, err := os.Getwd(); err == nil {
		m.workdir = wd
	} else {
		// Non-fatal — every wrapper still accepts "" and git falls back to
		// the process cwd. Surface so the user understands why the worktree
		// header / modal might show a confusing path.
		m.status = "workdir resolve failed: " + firstLine(err.Error())
		m.statusStyle = statusErrS
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
		loadCommitsCmd(m.workdir, m.currentRefs, nil, nil, m.streamReqID),
		loadRefsCmd(m.workdir),
		loadHeadAncestorsCmd(m.workdir, m.streamReqID),
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

	case headAncestorsLoadedMsg:
		if msg.reqID != m.streamReqID {
			// A reload superseded this dispatch; the new reqID's ancestry
			// is already in flight (or already landed).
			return m, nil
		}
		if msg.err != nil {
			// Fall back to position-based dim — the boundary still reads,
			// just without ancestor protection. Surface in the log; don't
			// noise the status bar with a niche error.
			log.Printf("git rev-list HEAD: %v", msg.err)
			return m, nil
		}
		m.graph.SetHeadAncestors(msg.ancestors)
		return m, nil

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
		// Apply pending refs cursor jumps after the model has the new ref
		// list. Priority: name (create / rename) > delete > persist
		// (reload-cursor snapshot). The switch fires at most one branch and
		// drops the persist handle whenever a higher-priority branch wins,
		// so a name/delete-armed reload doesn't leak persist state to the
		// next cycle. SelectByName / SelectAfterDeleted / SelectByNameKind
		// are all no-ops on a load failure (refs.loaded stays false).
		switch {
		case m.pendingRefCursorName != "":
			m.refs.SelectByName(m.pendingRefCursorName)
			m.pendingRefCursorName = ""
			m.pendingRefCursorPersist = persistedRefHandle{}
		case m.pendingRefCursorAfterDelete.name != "":
			m.refs.SelectAfterDeleted(m.pendingRefCursorAfterDelete.name, m.pendingRefCursorAfterDelete.kind)
			m.pendingRefCursorAfterDelete = deletedRefHandle{}
			m.pendingRefCursorPersist = persistedRefHandle{}
		case m.pendingRefCursorPersist.name != "" || m.pendingRefCursorPersist.stashHash != "":
			h := m.pendingRefCursorPersist
			if h.kind == git.RefKindStash && h.stashHash != "" {
				if !m.refs.SelectStashByHash(h.stashHash) {
					m.refs.SelectAfterDeleted(h.name, git.RefKindStash)
				}
			} else {
				m.refs.SelectByNameKindOrNeighbor(h.name, h.kind)
			}
			m.pendingRefCursorPersist = persistedRefHandle{}
		}
		// Sync stash tips into the commit stream when the set has changed.
		// reloadCmd is idempotent against an unchanged stash set — the next
		// refsLoadedMsg will see the same hashes and skip this branch.
		if _, ok := msg.(refsLoadedMsg); ok {
			if hashes, byHash, changed := diffStashRefs(m.refs.StashRefs(), m.currentStashHashes); changed {
				m.currentStashHashes = hashes
				m.currentStashByHash = byHash
				return m, tea.Batch(cmd, m.reloadCmd())
			}
		}
		return m, cmd

	case refCheckoutRequestedMsg:
		var cmd tea.Cmd
		m, cmd = m.beginCheckout(git.CheckoutTarget(msg.ref), false)
		return m, cmd

	case refCheckoutWithPullRequestedMsg:
		// Pull eligibility is decided here, at keypress time, while we still
		// have the full git.Ref (Kind + Upstream). The chain command itself
		// stays dir-only; passing the decision in skipReason avoids a
		// follow-up `git rev-parse @{upstream}` mid-chain.
		var cmd tea.Cmd
		m, cmd = m.beginCheckoutWithPull(git.CheckoutTarget(msg.ref), false, resolvePullEligibility(msg.ref))
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

	case checkoutThenPullSucceededMsg:
		m.checkoutInFlight = false
		m.pendingCheckout = pendingCheckout{}
		if msg.pullSkipped {
			m.status = checkoutLabel(msg.ref, msg.detached) +
				" (pull skipped: " + msg.skipReason + ")"
		} else {
			m.status = checkoutLabel(msg.ref, msg.detached) + "; pull: done"
		}
		m.statusStyle = statusOkS
		m.pendingHEADHash = pendingHEADSentinel
		return m, m.reloadCmd()

	case checkoutThenPullConflictMsg:
		// checkout landed; pull tripped on a merge/rebase conflict. HEAD now
		// sits on a half-merged commit so we still reload refs+log, but we
		// deliberately do NOT arm pendingHEADHash — the user is mid-conflict
		// and should resolve in their terminal before the cursor jumps.
		m.checkoutInFlight = false
		m.pendingCheckout = pendingCheckout{}
		m.status = checkoutLabel(msg.ref, msg.detached) +
			"; pull: CONFLICT — resolve in your terminal"
		m.statusStyle = statusErrS
		return m, m.reloadCmd()

	case stashThenCheckoutThenPullThenPopSucceededMsg:
		m.checkoutInFlight = false
		m.pendingCheckout = pendingCheckout{}
		if msg.pullSkipped {
			m.status = checkoutLabel(msg.ref, msg.detached) +
				" (pull skipped: " + msg.skipReason + "); stashed → popped " + msg.stashLabel
		} else {
			m.status = checkoutLabel(msg.ref, msg.detached) +
				"; stashed → pull → popped " + msg.stashLabel
		}
		m.statusStyle = statusOkS
		m.pendingHEADHash = pendingHEADSentinel
		return m, m.reloadCmd()

	case stashThenCheckoutThenPullThenPopConflictMsg:
		// Three sub-cases collapse into one msg type:
		//   phase=Pull, ErrPullConflict → pop already ran; show conflict + label
		//   phase=Pull, generic err     → pop never ran; stash preserved
		//   phase=StashPop              → pop conflict; markers + stash preserved
		m.checkoutInFlight = false
		m.pendingCheckout = pendingCheckout{}
		m.statusStyle = statusErrS
		switch {
		case msg.phase == chainPhasePull && errors.Is(msg.err, git.ErrPullConflict):
			m.status = checkoutLabel(msg.ref, msg.detached) +
				"; pull: CONFLICT — resolve in your terminal; stash preserved at " + msg.stashLabel
			// HEAD is mid-conflict — no jump.
			return m, m.reloadCmd()
		case msg.phase == chainPhasePull:
			m.status = checkoutLabel(msg.ref, msg.detached) +
				"; pull failed: " + firstLine(msg.err.Error()) + "; stash preserved at " + msg.stashLabel
			// HEAD did move (checkout landed). Jump to it on reload so the
			// graph shows the user where they are.
			m.pendingHEADHash = pendingHEADSentinel
			return m, m.reloadCmd()
		default: // chainPhaseStashPop
			m.status = checkoutLabel(msg.ref, msg.detached) +
				"; pull: done; pop conflict — resolve markers and run `git stash drop` (" + msg.stashLabel + ")"
			m.pendingHEADHash = pendingHEADSentinel
			return m, m.reloadCmd()
		}

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
			loadDiffStatCmd(m.workdir, msg.hash, msg.reqID),
			loadCommitDetailCmd(m.workdir, msg.hash, msg.reqID),
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

	case graphActionMsg:
		m.actionInFlight = false
		// Stale-drop: cursor moved between Enter dispatch and this reply.
		// Drop silently — the user can re-press Enter on the new row.
		if c, ok := m.graph.Selected(); !ok || c.Hash != msg.hash {
			m.status = ""
			return m, nil
		}
		switch msg.kind {
		case graphActionNoOp:
			m.status = "already on " + msg.branch
			m.statusStyle = statusOkS
			return m, nil
		case graphActionCheckout:
			var cmd tea.Cmd
			m, cmd = m.beginCheckout(msg.branch, false)
			return m, cmd
		case graphActionPicker:
			m.mode = viewModeBranchPicker
			m.branchPicker = branchPickerState{
				candidates: msg.candidates,
				hash:       msg.hash,
			}
			m.status = fmt.Sprintf("branch select: %d candidates", len(msg.candidates))
			m.statusStyle = statusBusyS
			return m, nil
		case graphActionFF:
			m.ffInFlight = true
			m.status = ffLabel(msg.branch, msg.advance) + " …"
			m.statusStyle = statusBusyS
			return m, ffOnlyCmd(m.workdir, msg.branch, msg.hash)
		case graphActionCheckoutAndFF:
			m.ffInFlight = true
			m.status = "fast-forward: " + msg.branch + " (checkout + ff) …"
			m.statusStyle = statusBusyS
			return m, checkoutThenFFCmd(m.workdir, msg.branch, msg.hash)
		case graphActionDetach:
			var cmd tea.Cmd
			m, cmd = m.beginCheckout(msg.hash, true)
			return m, cmd
		case graphActionStashAction:
			subject := ""
			if c, ok := m.graph.Selected(); ok {
				subject = c.Subject
			}
			m.pendingStashAction = pendingStashAction{
				label:   msg.stashLabel,
				hash:    msg.hash,
				subject: subject,
			}
			m.mode = viewModeStashActionPicker
			m.status = "stash: " + msg.stashLabel
			m.statusStyle = statusBusyS
			return m, nil
		}
		return m, nil

	case ffSucceededMsg:
		m.ffInFlight = false
		m.status = ffLabel(msg.branch, msg.advance)
		m.statusStyle = statusOkS
		m.pendingHEADHash = pendingHEADSentinel
		return m, m.reloadCmd()

	case ffFailedMsg:
		m.ffInFlight = false
		log.Printf("graph enter: ff failed: %v", msg.err)
		m.status = "fast-forward failed: " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil

	case ffNeedsCleanTreeMsg:
		// Modal owns the next decision; release the in-flight gate so
		// 's' → stashThenFFCmd can re-arm it without colliding.
		m.ffInFlight = false
		m.pendingCheckout = pendingCheckout{
			ref:    msg.branch,
			withFF: true,
			ffHash: msg.hash,
		}
		m.mode = viewModeCheckoutConfirm
		m.status = "fast-forward: " + msg.branch + " — uncommitted changes"
		m.statusStyle = statusErrS
		return m, nil

	case stashThenFFMsg:
		m.ffInFlight = false
		m.pendingCheckout = pendingCheckout{}
		m.status = ffLabel(msg.branch, msg.advance) +
			" (stashed before fast-forward: " + msg.stashLabel + ")"
		m.statusStyle = statusOkS
		m.pendingHEADHash = pendingHEADSentinel
		return m, m.reloadCmd()

	case checkoutThenFFSucceededMsg:
		m.ffInFlight = false
		m.status = ffLabel(msg.branch, msg.advance) + " (after checkout)"
		m.statusStyle = statusOkS
		m.pendingHEADHash = pendingHEADSentinel
		return m, m.reloadCmd()

	case ffCheckoutNeedsCleanTreeMsg:
		// Modal owns the next decision; release the in-flight gate so
		// 's' → stashThenCheckoutThenFFCmd can re-arm it without colliding.
		m.ffInFlight = false
		m.pendingCheckout = pendingCheckout{
			ref:            msg.branch,
			withCheckoutFF: true,
			ffHash:         msg.hash,
		}
		m.mode = viewModeCheckoutConfirm
		m.status = "checkout+fast-forward: " + msg.branch + " — uncommitted changes"
		m.statusStyle = statusErrS
		return m, nil

	case stashThenCheckoutThenFFMsg:
		m.ffInFlight = false
		m.pendingCheckout = pendingCheckout{}
		m.status = ffLabel(msg.branch, msg.advance) +
			" (stashed before checkout+fast-forward: " + msg.stashLabel + ")"
		m.statusStyle = statusOkS
		m.pendingHEADHash = pendingHEADSentinel
		return m, m.reloadCmd()

	case stashPopSucceededMsg:
		m.checkoutInFlight = false
		m.pendingStashAction = pendingStashAction{}
		m.status = "stash: popped " + msg.label
		m.statusStyle = statusOkS
		return m, m.reloadCmd()

	case stashPopConflictMsg:
		m.checkoutInFlight = false
		m.pendingStashAction = pendingStashAction{}
		m.status = "stash " + msg.label + ": CONFLICT — resolve markers; stash preserved"
		m.statusStyle = statusErrS
		return m, m.reloadCmd()

	case stashPopFailedMsg:
		m.checkoutInFlight = false
		m.pendingStashAction = pendingStashAction{}
		m.status = "stash pop failed: " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil

	case stashApplySucceededMsg:
		m.checkoutInFlight = false
		m.pendingStashAction = pendingStashAction{}
		m.status = "stash: applied " + msg.label
		m.statusStyle = statusOkS
		return m, m.reloadCmd()

	case stashApplyConflictMsg:
		m.checkoutInFlight = false
		m.pendingStashAction = pendingStashAction{}
		m.status = "stash " + msg.label + ": CONFLICT — resolve markers; stash preserved"
		m.statusStyle = statusErrS
		return m, m.reloadCmd()

	case stashApplyFailedMsg:
		m.checkoutInFlight = false
		m.pendingStashAction = pendingStashAction{}
		m.status = "stash apply failed: " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil

	case stashDropSucceededMsg:
		m.refActionInFlight = false
		m.pendingStashDrop = pendingStashDrop{}
		m.status = "stash: dropped " + msg.label
		m.statusStyle = statusOkS
		// Cursor follow-up: surviving stash slots shift down by one
		// (stash@{1} → stash@{0}); SelectAfterDeleted lands the cursor
		// on the next entry in the same section (or previous when last).
		m.pendingRefCursorAfterDelete = deletedRefHandle{
			name: msg.label,
			kind: git.RefKindStash,
		}
		return m, m.reloadCmd()

	case stashDropFailedMsg:
		m.refActionInFlight = false
		m.pendingStashDrop = pendingStashDrop{}
		m.status = "stash drop failed: " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil

	case refCreateRequestedMsg:
		var cmd tea.Cmd
		m, cmd = m.beginRefCreate(msg.cursorRef, msg.hasCursor)
		return m, cmd

	case refRenameRequestedMsg:
		var cmd tea.Cmd
		m, cmd = m.beginRefRename(msg.ref)
		return m, cmd

	case refRenameRejectedMsg:
		m.status = msg.reason
		m.statusStyle = statusErrS
		return m, nil

	case refNameValidatedMsg:
		m.refNameInput.validating = false
		if msg.err != nil {
			m.refNameInput.inlineErr = firstLine(msg.err.Error())
			return m, nil
		}
		name := strings.TrimSpace(msg.name)
		switch m.refNameInput.mode {
		case refNameInputCreate:
			var cmd tea.Cmd
			m, cmd = m.dispatchRefCreate(name)
			return m, cmd
		case refNameInputRename:
			var cmd tea.Cmd
			m, cmd = m.dispatchRefRename(name)
			return m, cmd
		}
		return m, nil

	case branchCreateSucceededMsg:
		m.refActionInFlight = false
		base := m.refNameInput.baseLabel
		if base == "" {
			base = "HEAD"
		}
		m.mode = viewModeNormal
		m.refNameInput = refNameInputState{}
		m.status = "created '" + msg.name + "' (from " + base + ")"
		m.statusStyle = statusOkS
		m.pendingRefCursorName = msg.name
		return m, m.reloadCmd()

	case branchCreateFailedMsg:
		m.refActionInFlight = false
		// Keep the modal open so the user can fix the typed name and retry.
		if errors.Is(msg.err, git.ErrBranchAlreadyExists) ||
			errors.Is(msg.err, git.ErrInvalidRefName) {
			m.refNameInput.inlineErr = firstLine(msg.err.Error())
			return m, nil
		}
		m.mode = viewModeNormal
		m.refNameInput = refNameInputState{}
		m.status = "create failed: " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil

	case branchRenameSucceededMsg:
		m.refActionInFlight = false
		m.mode = viewModeNormal
		m.refNameInput = refNameInputState{}
		m.status = "renamed '" + msg.oldName + "' → '" + msg.newName + "'"
		m.statusStyle = statusOkS
		m.pendingRefCursorName = msg.newName
		if msg.headWasOld {
			// HEAD now points at newName; sentinel survives reload and is
			// resolved to the post-rename HEAD hash by refsLoadedMsg.
			m.pendingHEADHash = pendingHEADSentinel
		}
		return m, m.reloadCmd()

	case branchRenameFailedMsg:
		m.refActionInFlight = false
		if errors.Is(msg.err, git.ErrBranchAlreadyExists) ||
			errors.Is(msg.err, git.ErrInvalidRefName) {
			m.refNameInput.inlineErr = firstLine(msg.err.Error())
			return m, nil
		}
		m.mode = viewModeNormal
		m.refNameInput = refNameInputState{}
		m.status = "rename failed: " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil

	case branchDeleteSucceededMsg:
		m.refActionInFlight = false
		m.mode = viewModeNormal
		m.pendingRefDelete = refDeleteState{}
		m.status = formatDeleteSuccess(msg.target, msg.scope, msg.localDeleted, msg.remoteDeleted)
		m.statusStyle = statusOkS
		// Cursor follow-up: prefer the local name when local was deleted,
		// else the remote shortname so the cursor lands somewhere sensible
		// in the remote section.
		if msg.localDeleted {
			m.pendingRefCursorAfterDelete = deletedRefHandle{
				name: msg.target.localName,
				kind: git.RefKindLocal,
			}
		} else if msg.remoteDeleted {
			m.pendingRefCursorAfterDelete = deletedRefHandle{
				name: msg.target.remote + "/" + msg.target.remoteBranch,
				kind: git.RefKindRemote,
			}
		}
		return m, m.reloadCmd()

	case branchDeletePartialMsg:
		m.refActionInFlight = false
		m.mode = viewModeNormal
		m.pendingRefDelete = refDeleteState{}
		m.status = "deleted '" + msg.target.localName + "'; remote push failed: " +
			firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		// Local was deleted — move the cursor off it. Remote is still there.
		if msg.localDeleted {
			m.pendingRefCursorAfterDelete = deletedRefHandle{
				name: msg.target.localName,
				kind: git.RefKindLocal,
			}
		}
		return m, m.reloadCmd()

	case branchDeleteFailedMsg:
		m.refActionInFlight = false
		m.mode = viewModeNormal
		m.pendingRefDelete = refDeleteState{}
		m.status = "delete failed: " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil

	case branchDeleteNotMergedMsg:
		m.refActionInFlight = false
		m.mode = viewModeNormal
		m.pendingRefDelete = refDeleteState{}
		m.status = "delete: '" + msg.target.localName +
			"' not fully merged — press [f] or [F] to force"
		m.statusStyle = statusErrS
		return m, nil

	case localChangesEnterRequestedMsg:
		cmd := m.enterLocalChangesMode()
		m.status = "local changes"
		m.statusStyle = statusOkS
		return m, cmd

	case localChangesStatusLoadedMsg:
		m.localChanges.ApplyStatusLoaded(msg.entries)
		// After reload, dispatch a diff for whatever the cursor now points
		// at so the right pane doesn't lag behind the tree.
		return m.dispatchLocalChangesDiff()

	case localChangesStatusFailedMsg:
		m.localChanges.ApplyStatusFailed(msg.err)
		m.status = "status: " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil

	case localChangesDiffLoadedMsg:
		m.localChanges.ApplyDiffLoaded(msg.reqID, msg.text)
		return m, nil

	case localChangesDiffFailedMsg:
		m.localChanges.ApplyDiffFailed(msg.reqID, msg.err)
		return m, nil

	case localChangesAddSucceededMsg:
		m.status = "staged " + msg.path
		m.statusStyle = statusOkS
		return m, loadStatusCmd(m.workdir)

	case localChangesAddFailedMsg:
		m.status = "stage " + msg.path + ": " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil

	case localChangesRestoreSucceededMsg:
		m.status = "unstaged " + msg.path
		m.statusStyle = statusOkS
		return m, loadStatusCmd(m.workdir)

	case localChangesRestoreFailedMsg:
		m.status = "unstage " + msg.path + ": " + firstLine(msg.err.Error())
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
		if m.mode == viewModeBranchPicker {
			switch msg.String() {
			case "j", "down":
				if m.branchPicker.cursor < len(m.branchPicker.candidates)-1 {
					m.branchPicker.cursor++
					m.branchPicker.scrollIntoView(branchPickerVisibleRows(m.height, len(m.branchPicker.candidates)))
				}
				return m, nil
			case "k", "up":
				if m.branchPicker.cursor > 0 {
					m.branchPicker.cursor--
					m.branchPicker.scrollIntoView(branchPickerVisibleRows(m.height, len(m.branchPicker.candidates)))
				}
				return m, nil
			case "enter":
				if len(m.branchPicker.candidates) == 0 {
					return m, nil
				}
				branch := m.branchPicker.candidates[m.branchPicker.cursor]
				m.branchPicker = branchPickerState{}
				m.mode = viewModeNormal
				var cmd tea.Cmd
				m, cmd = m.beginCheckout(branch, false)
				return m, cmd
			case "esc":
				m.mode = viewModeNormal
				m.branchPicker = branchPickerState{}
				m.status = "branch select cancelled"
				m.statusStyle = statusOkS
				return m, nil
			case "ctrl+c":
				m.cancelStream()
				return m, tea.Quit
			}
			return m, nil
		}
		if m.mode == viewModeRefNameInput {
			switch msg.String() {
			case "esc":
				m.mode = viewModeNormal
				m.refNameInput = refNameInputState{}
				m.status = "ref input cancelled"
				m.statusStyle = statusOkS
				return m, nil
			case "ctrl+c":
				m.cancelStream()
				return m, tea.Quit
			case "enter":
				if m.refNameInput.validating {
					return m, nil
				}
				name := strings.TrimSpace(m.refNameInput.input.Value())
				if name == "" {
					m.refNameInput.inlineErr = "name required"
					return m, nil
				}
				m.refNameInput.inlineErr = ""
				m.refNameInput.validating = true
				return m, checkRefFormatCmd(m.workdir, name)
			}
			var cmd tea.Cmd
			m.refNameInput.input, cmd = m.refNameInput.input.Update(msg)
			return m, cmd
		}
		if m.mode == viewModeRefDeleteConfirm {
			switch msg.String() {
			case "esc":
				m.mode = viewModeNormal
				m.pendingRefDelete = refDeleteState{}
				m.status = "delete: cancelled"
				m.statusStyle = statusOkS
				return m, nil
			case "ctrl+c":
				m.cancelStream()
				return m, tea.Quit
			case "y", "Y", "f", "F":
				scope, ok := resolveDeleteScope(
					msg.String(), m.pendingRefDelete.hasLocal, m.pendingRefDelete.hasRemote,
				)
				if !ok {
					// Key swallowed — out-of-matrix for this target.
					return m, nil
				}
				return m.dispatchRefDelete(scope)
			}
			return m, nil
		}
		if m.mode == viewModeStashActionPicker {
			switch msg.String() {
			case "esc":
				m.mode = viewModeNormal
				m.pendingStashAction = pendingStashAction{}
				m.status = "stash: cancelled"
				m.statusStyle = statusOkS
				return m, nil
			case "ctrl+c":
				m.cancelStream()
				return m, tea.Quit
			case "p":
				p := m.pendingStashAction
				m.mode = viewModeNormal
				m.checkoutInFlight = true
				m.status = "stash: popping " + p.label + "…"
				m.statusStyle = statusBusyS
				return m, stashPopCmd(m.workdir, p.label)
			case "a":
				p := m.pendingStashAction
				m.mode = viewModeNormal
				m.checkoutInFlight = true
				m.status = "stash: applying " + p.label + "…"
				m.statusStyle = statusBusyS
				return m, stashApplyCmd(m.workdir, p.label)
			}
			return m, nil
		}
		if m.mode == viewModeStashDropConfirm {
			switch msg.String() {
			case "esc":
				m.mode = viewModeNormal
				m.pendingStashDrop = pendingStashDrop{}
				m.status = "drop: cancelled"
				m.statusStyle = statusOkS
				return m, nil
			case "ctrl+c":
				m.cancelStream()
				return m, tea.Quit
			case "y":
				p := m.pendingStashDrop
				m.mode = viewModeNormal
				m.refActionInFlight = true
				m.status = "stash: dropping " + p.label + "…"
				m.statusStyle = statusBusyS
				return m, stashDropCmd(m.workdir, p.label)
			}
			return m, nil
		}
		if m.mode == viewModeLocalChanges {
			switch msg.String() {
			case "ctrl+c", "q":
				m.cancelStream()
				return m, tea.Quit
			case ",", "esc":
				m.exitLocalChangesMode()
				m.status = "local changes: exit"
				m.statusStyle = statusOkS
				return m, nil
			case "?":
				m.mode = viewModeHelp
				m.applyPaneSizes()
				return m, nil
			case "tab":
				return m.cycleLocalChangesFocus(), nil
			case "r":
				return m, loadStatusCmd(m.workdir)
			}
			// Tree sub-focus owns cursor movement + stage/unstage.
			// Diff sub-focus owns viewport scroll. paneRefs focus inside the
			// mode forwards to refs.Update so j/k still navigates the sidebar
			// (sticky row + ref rows).
			if m.focused == paneRefs {
				var cmd tea.Cmd
				m.refs, cmd = m.refs.Update(msg)
				return m, cmd
			}
			switch m.localChanges.Focused() {
			case paneLCTree:
				return m.handleLocalChangesTreeKey(msg)
			case paneLCDiff:
				return m, m.localChanges.ScrollDiff(msg)
			}
			return m, nil
		}
		if m.mode == viewModeCheckoutConfirm {
			switch msg.String() {
			case "s":
				p := m.pendingCheckout
				m.mode = viewModeNormal
				m.statusStyle = statusBusyS
				if p.withFF {
					m.ffInFlight = true
					m.status = "fast-forward: " + p.ref + " (stashing…)"
					return m, stashThenFFCmd(m.workdir, p.ref, p.ffHash)
				}
				if p.withCheckoutFF {
					m.ffInFlight = true
					m.status = "fast-forward: " + p.ref + " (checkout + ff, stashing…)"
					return m, stashThenCheckoutThenFFCmd(m.workdir, p.ref, p.ffHash)
				}
				m.checkoutInFlight = true
				if p.withPull {
					if p.skipReason != "" {
						m.status = "checkout: " + p.ref + " (pull skipped: " + p.skipReason + ", stashing…)"
					} else {
						m.status = "checkout: " + p.ref + " + pull (stashing…)"
					}
					return m, stashThenCheckoutThenPullThenPopCmd(
						"", p.ref, p.detached, m.pullPrefStrategy, p.skipReason,
					)
				}
				m.status = "checkout: " + p.ref + " (stashing…)"
				return m, stashThenCheckoutCmd(m.workdir, p.ref, p.detached)
			case "a", "esc":
				m.mode = viewModeNormal
				p := m.pendingCheckout
				m.pendingCheckout = pendingCheckout{}
				switch {
				case p.withFF, p.withCheckoutFF:
					m.status = "fast-forward: aborted"
				default:
					m.status = "checkout: aborted"
				}
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
		case "?":
			if m.mode == viewModeHelp {
				m.mode = viewModeNormal
			} else {
				m.mode = viewModeHelp
			}
			m.applyPaneSizes()
			return m, nil
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
			return m, fetchCmd(m.workdir)
		case "P":
			if m.pullInFlight {
				return m, nil
			}
			m.pullInFlight = true
			m.status = "pulling…"
			m.statusStyle = statusBusyS
			return m, pullCmd(m.workdir, m.pullPrefStrategy)
		case "r":
			return m, m.reloadCmd()
		case ",":
			cmd := m.enterLocalChangesMode()
			m.status = "local changes"
			m.statusStyle = statusOkS
			return m, cmd
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
		case "enter":
			// Graph focus only — refs pane has its own enter handler
			// (refs.go: refCheckoutRequestedMsg). On other panes, fall
			// through to the focused-sub-model dispatch below.
			if m.focused != paneGraph {
				break
			}
			if m.actionInFlight || m.checkoutInFlight || m.ffInFlight {
				return m, nil
			}
			c, ok := m.graph.Selected()
			if !ok {
				return m, nil
			}
			locals := m.refs.LocalRefs()
			if len(locals) == 0 {
				m.status = "refs not loaded yet"
				m.statusStyle = statusErrS
				return m, nil
			}
			remotes := m.refs.RemoteRefs()
			stashes := m.refs.StashRefs()
			m.actionInFlight = true
			m.status = "→ resolving…"
			m.statusStyle = statusBusyS
			log.Printf("graph enter: dispatch evaluator (cursor=%s, locals=%d, remotes=%d, stashes=%d)",
				shortHash(c.Hash), len(locals), len(remotes), len(stashes))
			return m, evaluateGraphActionCmd(m.workdir, c.Hash, locals, remotes, stashes)
		case "d":
			// Refs focus reinterprets `d` as the delete intent so the
			// destructive ref-write key doesn't collide with the patch
			// overlay. Graph / tab focus falls through to the patch
			// overlay (the original behavior).
			if m.focused == paneRefs {
				return m.beginRefDelete()
			}
			c, ok := m.graph.Selected()
			if !ok {
				return m, nil
			}
			m.diffReqID++
			m.diff.BeginPatchLoad(c.Hash, m.diffReqID)
			m.mode = viewModeDiffWindow
			m.diff.SetPatchViewportSize(m.width, m.height-1)
			return m, loadDiffPatchCmd(m.workdir, c.Hash, m.diffReqID)
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
	if m.mode == viewModeLocalChanges {
		m.localChanges.SetSize(s.lcTreeW, s.lcTreeH, s.lcDiffW, s.lcDiffH)
	}
}

// enterLocalChangesMode flips into the working-tree view and kicks off the
// first status load. refs / graph / tab models are left untouched so exit
// returns to the exact prior state. focused is parked on paneGraph (= "right
// column has focus") and the sub-focus inside that column starts on the tree.
func (m *Model) enterLocalChangesMode() tea.Cmd {
	m.mode = viewModeLocalChanges
	m.focused = paneGraph
	m.localChanges.SetFocus(paneLCTree)
	m.applyPaneSizes()
	return loadStatusCmd(m.workdir)
}

// exitLocalChangesMode flips back to the normal layout. Entries / cursor
// state are kept so re-entry restores them; the diff body is released so
// a large untracked-file diff doesn't sit resident between sessions.
func (m *Model) exitLocalChangesMode() {
	m.mode = viewModeNormal
	m.localChanges.ClosePatch()
	m.applyPaneSizes()
}

// cycleLocalChangesFocus implements the 3-way tab cycle inside the mode:
// refs → tree → diff → refs. Sub-focus inside the right column lives on
// m.localChanges; outer focus only distinguishes refs vs. right column.
func (m Model) cycleLocalChangesFocus() Model {
	switch {
	case m.focused == paneRefs:
		m.focused = paneGraph
		m.localChanges.SetFocus(paneLCTree)
	case m.localChanges.Focused() == paneLCTree:
		m.localChanges.SetFocus(paneLCDiff)
	default:
		m.focused = paneRefs
		m.localChanges.SetFocus(paneLCTree)
	}
	return m
}

// handleLocalChangesTreeKey routes j/k/g/G/space inside the tree pane. The
// cursor-move keys are followed by a diff dispatch for the new entry so the
// diff viewport keeps step. space toggles the entry between Staged and
// Unstaged via Add / RestoreStaged.
func (m Model) handleLocalChangesTreeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		if _, ok := m.localChanges.MoveCursor(1); ok {
			return m.dispatchLocalChangesDiff()
		}
		return m, nil
	case "k", "up":
		if _, ok := m.localChanges.MoveCursor(-1); ok {
			return m.dispatchLocalChangesDiff()
		}
		return m, nil
	case "g":
		if _, ok := m.localChanges.JumpCursor(false); ok {
			return m.dispatchLocalChangesDiff()
		}
		return m, nil
	case "G":
		if _, ok := m.localChanges.JumpCursor(true); ok {
			return m.dispatchLocalChangesDiff()
		}
		return m, nil
	case " ", "space":
		return m.dispatchLocalChangesStage()
	}
	return m, nil
}

// dispatchLocalChangesDiff issues a diff load for whatever entry the cursor
// is currently on. Bumps the reqID so any in-flight stale response is
// dropped by ApplyDiffLoaded's match check.
func (m Model) dispatchLocalChangesDiff() (tea.Model, tea.Cmd) {
	e, ok := m.localChanges.CurrentEntry()
	if !ok {
		return m, nil
	}
	m.localChangesReqID++
	m.localChanges.BeginDiffLoad(m.localChangesReqID)
	return m, loadDiffCmd(m.workdir, e.Path, e.Staged(), e.Untracked, m.localChangesReqID)
}

// dispatchLocalChangesStage picks Add vs. RestoreStaged based on which
// section the cursor entry sits in. The pending-select hint preserves the
// cursor on the same path after the post-action status reload.
func (m Model) dispatchLocalChangesStage() (tea.Model, tea.Cmd) {
	e, ok := m.localChanges.CurrentEntry()
	if !ok {
		return m, nil
	}
	if e.Section == sectionStaged {
		m.localChanges.ScheduleSelectAfterReload(e.Path, false)
		m.status = "unstage " + e.Path + "…"
		m.statusStyle = statusBusyS
		return m, restoreStagedCmd(m.workdir, e.Path)
	}
	m.localChanges.ScheduleSelectAfterReload(e.Path, true)
	m.status = "stage " + e.Path + "…"
	m.statusStyle = statusBusyS
	return m, addCmd(m.workdir, e.Path)
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
	return m, checkoutCmd(m.workdir, ref, detached)
}

// resolvePullEligibility decides whether `p` should follow the checkout
// with a pull. Returns a non-empty reason string when pull should be
// skipped, "" when pull is eligible. Tags resolve to detached HEADs with
// no upstream; local branches without an upstream have nowhere to pull
// from; remote-tracking refs always become local tracking branches via
// dwim and are pull-eligible. The decision happens at keypress time so
// the chain command itself stays dir-only and doesn't need to re-resolve
// refs across async steps.
func resolvePullEligibility(ref git.Ref) (skipReason string) {
	switch ref.Kind {
	case git.RefKindTag:
		return "tag has no upstream"
	case git.RefKindLocal:
		if ref.Upstream == "" {
			return "local branch has no upstream"
		}
	}
	return ""
}

// beginCheckoutWithPull dispatches the refs-pane `p` chain. Same gating
// as beginCheckout (one in-flight at a time) plus pendingCheckout fields
// that survive the dirty-tree confirm modal so its `s` branch can re-issue
// the chain instead of falling back to the plain checkout-only flow.
// A non-empty skipReason means pull will be skipped — that single value
// drives both the busy-status text and the chain command's skip flag.
func (m Model) beginCheckoutWithPull(ref string, detached bool, skipReason string) (Model, tea.Cmd) {
	if m.checkoutInFlight {
		return m, nil
	}
	m.checkoutInFlight = true
	m.pendingCheckout = pendingCheckout{
		ref:        ref,
		detached:   detached,
		withPull:   true,
		skipReason: skipReason,
	}
	if skipReason != "" {
		m.status = checkoutLabel(ref, detached) + " (pull skipped: " + skipReason + ") …"
	} else {
		m.status = checkoutLabel(ref, detached) + " + pull …"
	}
	m.statusStyle = statusBusyS
	return m, checkoutThenPullCmd(m.workdir, ref, detached, m.pullPrefStrategy, skipReason)
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

// beginRefCreate opens the create modal with the base hash + label resolved
// from the focused pane. The modal's textinput is freshly initialized each
// time so a previous typed value doesn't leak into the next session.
func (m Model) beginRefCreate(refsCursorRef git.Ref, refsHasCursor bool) (Model, tea.Cmd) {
	if m.refActionInFlight {
		return m, nil
	}
	graphHash := ""
	if c, ok := m.graph.Selected(); ok {
		graphHash = c.Hash
	}
	base, label := resolveCreateBase(m.focused, graphHash, refsCursorRef, refsHasCursor)

	ti := textinput.New()
	ti.Placeholder = "branch name"
	ti.Focus()
	ti.CharLimit = 200
	ti.Width = 40

	m.refNameInput = refNameInputState{
		mode:      refNameInputCreate,
		base:      base,
		baseLabel: label,
		input:     ti,
	}
	m.mode = viewModeRefNameInput
	m.status = ""
	return m, textinput.Blink
}

// beginRefRename opens the rename modal with the source ref baked in. The
// textinput is pre-populated with the current name so the user can edit
// rather than retype, but the cursor is left at the end so a single Enter
// without edits lands on a no-op (which the validator catches as the same-
// name case via git itself).
func (m Model) beginRefRename(target git.Ref) (Model, tea.Cmd) {
	if m.refActionInFlight {
		return m, nil
	}
	ti := textinput.New()
	ti.SetValue(target.ShortName)
	ti.CursorEnd()
	ti.Focus()
	ti.CharLimit = 200
	ti.Width = 40

	m.refNameInput = refNameInputState{
		mode:   refNameInputRename,
		target: target,
		input:  ti,
	}
	m.mode = viewModeRefNameInput
	m.status = ""
	return m, textinput.Blink
}

// beginRefDelete reads the cursor ref from refs and opens the delete modal
// with the resolved 4-axis state. HEAD branches and tags are filtered with a
// status-bar message instead of opening the modal — destructive intent
// against those targets is almost always a misclick. Stash cursors route to
// beginStashDrop so the single-key `[y] drop` modal opens instead of the
// branch-delete matrix.
func (m Model) beginRefDelete() (Model, tea.Cmd) {
	if m.refActionInFlight {
		return m, nil
	}
	ref, ok := m.refs.Selected()
	if !ok {
		m.status = "delete: no ref selected"
		m.statusStyle = statusErrS
		return m, nil
	}
	if ref.Kind == git.RefKindStash {
		return m.beginStashDrop(ref), nil
	}
	if ref.Kind == git.RefKindLocal && ref.IsHead {
		m.status = "cannot delete current branch"
		m.statusStyle = statusErrS
		return m, nil
	}
	if ref.Kind == git.RefKindTag {
		m.status = "delete: branches only (tags not supported)"
		m.statusStyle = statusErrS
		return m, nil
	}
	st, ok := resolveDeleteState(ref, m.refs.LocalRefs(), m.refs.RemoteRefs())
	if !ok {
		m.status = "delete: nothing to delete on this ref"
		m.statusStyle = statusErrS
		return m, nil
	}
	m.pendingRefDelete = st
	m.mode = viewModeRefDeleteConfirm
	m.status = ""
	return m, nil
}

// beginStashDrop opens the refs-pane `d` stash drop modal. subject is
// resolved best-effort from the cached stashByHash → ObjectName lookup
// against the current graph; if absent (stash hash not yet in graph
// stream), the modal falls back to showing just the label.
func (m Model) beginStashDrop(ref git.Ref) Model {
	subject := ""
	if c, ok := m.graph.CommitByHash(ref.ObjectName); ok {
		subject = c.Subject
	}
	m.pendingStashDrop = pendingStashDrop{label: ref.ShortName, subject: subject}
	m.mode = viewModeStashDropConfirm
	m.status = ""
	return m
}

// dispatchRefDelete fires branchDeleteCmd for the resolved scope and arms
// the in-flight gate. The modal stays open while the cmd runs — the caller
// closes it on the success / failed / partial msg.
func (m Model) dispatchRefDelete(scope deleteScope) (Model, tea.Cmd) {
	d := m.pendingRefDelete
	target := deleteTarget{
		localName:    d.localName,
		remote:       d.remote,
		remoteBranch: d.remoteBranch,
	}

	m.refActionInFlight = true
	m.mode = viewModeNormal

	switch scope {
	case scopeLocalSafe, scopeLocalForce:
		m.status = "deleting '" + d.localName + "'…"
	case scopeBothSafe, scopeBothForce:
		m.status = "deleting '" + d.localName + "' + remote '" + d.remote + "/" + d.remoteBranch + "'…"
	case scopeRemoteOnly:
		m.status = "deleting remote '" + d.remote + "/" + d.remoteBranch + "'…"
	}
	m.statusStyle = statusBusyS
	return m, branchDeleteCmd(m.workdir, target, scope)
}

// dispatchRefCreate fires branchCreateCmd from the validated modal state
// and arms the in-flight gate. The modal stays open while the cmd runs;
// success / failure handlers close it.
func (m Model) dispatchRefCreate(name string) (Model, tea.Cmd) {
	if m.refActionInFlight {
		return m, nil
	}
	m.refActionInFlight = true
	m.refNameInput.validating = false
	m.status = "creating '" + name + "'…"
	m.statusStyle = statusBusyS
	return m, branchCreateCmd(m.workdir, name, m.refNameInput.base)
}

// dispatchRefRename fires branchRenameCmd from the validated modal state.
// headWasOld is observed before the cmd fires so the post-reload graph cursor
// jump can follow the rename when HEAD was the source.
func (m Model) dispatchRefRename(name string) (Model, tea.Cmd) {
	if m.refActionInFlight {
		return m, nil
	}
	m.refActionInFlight = true
	m.refNameInput.validating = false
	src := m.refNameInput.target
	headWasOld := src.IsHead
	m.status = "renaming '" + src.ShortName + "' → '" + name + "'…"
	m.statusStyle = statusBusyS
	return m, branchRenameCmd(m.workdir, src.ShortName, name, headWasOld)
}

// ffLabel renders the user-facing "fast-forward: <branch> +<N>" status
// prefix shared by busy / success / stash-then-success lines. Mirrors
// checkoutLabel's role for the FF path.
func ffLabel(branch string, advance int) string {
	return fmt.Sprintf("fast-forward: %s +%d", branch, advance)
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
// stream's commitsStreamDoneMsg.err. The stash tips are passed alongside
// currentRefs so the graph keeps showing stash entries across reloads.
func (m *Model) reloadCmd() tea.Cmd {
	m.cancelStream()
	m.streamReqID++
	// Snapshot the currently focused ref so refsLoadedMsg can restore the
	// cursor across the upcoming cursor=0 reset. Selected() returns false on
	// detached HEAD / empty ref set / pre-load, which falls through to a
	// zero handle and a no-op restore.
	if ref, ok := m.refs.Selected(); ok {
		h := persistedRefHandle{name: ref.ShortName, kind: ref.Kind}
		if ref.Kind == git.RefKindStash {
			h.stashHash = ref.ObjectName
		}
		m.pendingRefCursorPersist = h
	} else {
		m.pendingRefCursorPersist = persistedRefHandle{}
	}
	resetCmd := m.graph.ResetForReload()
	m.refs.ResetForReload()
	return tea.Batch(
		resetCmd,
		loadCommitsCmd(m.workdir, m.currentRefs, m.currentStashHashes, m.currentStashByHash, m.streamReqID),
		loadRefsCmd(m.workdir),
		loadHeadAncestorsCmd(m.workdir, m.streamReqID),
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
	// lcTreeW/H, lcDiffW/H carry the right-column split when mode ==
	// viewModeLocalChanges. Zero in any other mode — graphW/H and tabW/H
	// stay authoritative there.
	lcTreeW, lcTreeH int
	lcDiffW, lcDiffH int
}

func (m Model) paneSizes() paneSizes {
	var s paneSizes
	if m.width == 0 || m.height == 0 {
		return s
	}
	// Reserve 1 row for the bottom help/status line, or the full panel
	// height when `?` is open. The four centered overlay modal modes
	// (branchPicker / refNameInput / refDeleteConfirm / checkoutConfirm)
	// don't reserve extra rows here — composeOverlay paints them on top
	// of the unchanged 3-pane base, so paneSizes is mode-agnostic outside
	// viewModeHelp.
	helpReserved := 1
	if m.mode == viewModeHelp {
		helpReserved = m.helpReservedRows()
	}
	mainH := m.height - helpReserved
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

	// Local Changes mode subdivides the right column horizontally
	// (tree | diff) instead of vertically (graph / tab). Reuse the
	// Changes-tab ratio (35% to the file list) for layout consistency.
	if m.mode == viewModeLocalChanges {
		treeOuterW := rightOuterW * changesFileListRatio / 100
		if treeOuterW < 12 {
			treeOuterW = 12
		}
		if treeOuterW > rightOuterW-12 {
			treeOuterW = rightOuterW - 12
		}
		diffOuterW := rightOuterW - treeOuterW
		s.lcTreeW = treeOuterW - 2
		s.lcTreeH = mainH - 2
		s.lcDiffW = diffOuterW - 2
		s.lcDiffH = mainH - 2
		if s.lcTreeW < 1 {
			s.lcTreeW = 1
		}
		if s.lcTreeH < 1 {
			s.lcTreeH = 1
		}
		if s.lcDiffW < 1 {
			s.lcDiffW = 1
		}
		if s.lcDiffH < 1 {
			s.lcDiffH = 1
		}
	}
	return s
}

// helpReservedRows returns how many bottom rows the `?` help panel
// claims. Defaults to helpExpandedHeight; small terminals halve it.
// The main area is given priority — if leaving 3 rows for it would push
// the panel below 3 rows, the panel shrinks further (down to 1 row) so
// the user can still see the graph. Floor: 1.
func (m Model) helpReservedRows() int {
	want := max(min(helpExpandedHeight, m.height/2), 3)
	if upper := m.height - 3; upper > 0 {
		want = min(want, upper)
	}
	return max(want, 1)
}

// modalHeaderS is the bold style applied to the header row of the create /
// rename / picker modals. Delete confirm reuses confirmPromptS (busy-color
// + bold), which is its existing visual; checkout confirm uses
// confirmPromptS too. modalHeaderS is plain-bold so create/rename/picker
// don't read as a "warning" alongside the textinput cursor.
var modalHeaderS = lipgloss.NewStyle().Bold(true)

// renderBranchPickerInner returns the multi-line picker content. Built
// for composeOverlay — no border / size / hint chrome here, just rows.
//
// Scroll: candidate rows are sliced to a window of branchPickerVisibleRows
// starting at viewportTop. When the list overflows the window, the slice
// is sandwiched between "↑ N more" / "↓ N more" lines so the user knows
// there are off-screen candidates.
func (m Model) renderBranchPickerInner() string {
	visibleRows := branchPickerVisibleRows(m.height, len(m.branchPicker.candidates))
	if visibleRows < 1 {
		visibleRows = 1
	}
	top := m.branchPicker.viewportTop
	if top < 0 {
		top = 0
	}
	end := top + visibleRows
	if end > len(m.branchPicker.candidates) {
		end = len(m.branchPicker.candidates)
		top = end - visibleRows
		if top < 0 {
			top = 0
		}
	}

	lines := []string{modalHeaderS.Render("[Branch select]")}
	if top > 0 {
		lines = append(lines, help.Render(fmt.Sprintf("↑ %d more", top)))
	}
	for i := top; i < end; i++ {
		if i == m.branchPicker.cursor {
			lines = append(lines, selectedStyle.Render("> "+m.branchPicker.candidates[i]))
		} else {
			lines = append(lines, "  "+m.branchPicker.candidates[i])
		}
	}
	if rest := len(m.branchPicker.candidates) - end; rest > 0 {
		lines = append(lines, help.Render(fmt.Sprintf("↓ %d more", rest)))
	}
	lines = append(lines, help.Render(helpTextBranchPicker))

	return strings.Join(lines, "\n")
}

// renderRefNameInputInner returns the multi-line content for the create /
// rename name-entry modal. Four rows: header (bold), textinput view,
// inline-error or spacer (constant row count so the hint never bounces),
// and a trailing hint.
func (m Model) renderRefNameInputInner() string {
	var header string
	switch m.refNameInput.mode {
	case refNameInputCreate:
		base := m.refNameInput.baseLabel
		if base == "" {
			base = "HEAD"
		}
		header = "[Create branch from '" + base + "']"
	case refNameInputRename:
		header = "[Rename '" + m.refNameInput.target.ShortName + "' →]"
	}

	inputView := m.refNameInput.input.View()

	errLine := " "
	if m.refNameInput.inlineErr != "" {
		errLine = statusErrS.Render(m.refNameInput.inlineErr)
	} else if m.refNameInput.validating {
		errLine = statusBusyS.Render("validating…")
	}

	hint := help.Render("[enter] confirm · [esc] cancel")

	return strings.Join([]string{
		modalHeaderS.Render(header),
		inputView,
		errLine,
		hint,
	}, "\n")
}

// renderRefDeleteConfirmInner returns the multi-line content for the
// delete confirm modal. Header (confirmPromptS — bold + busy color) plus
// optional sub-line plus hint matrix derived from hasLocal × hasRemote.
func (m Model) renderRefDeleteConfirmInner() string {
	d := m.pendingRefDelete

	var header, sub string
	switch {
	case d.hasLocal && d.hasRemote:
		header = "Delete branch '" + d.localName + "'?"
		sub = "(matched remote: '" + d.remote + "/" + d.remoteBranch + "')"
	case d.hasLocal:
		header = "Delete branch '" + d.localName + "'? (no upstream)"
	default: // remote only
		header = "Delete remote-tracking '" + d.remote + "/" + d.remoteBranch + "'?"
		sub = "(no matching local — remote ref will be deleted on '" + d.remote + "')"
	}

	var hint string
	switch {
	case d.hasLocal && d.hasRemote:
		hint = "[y] local · [Y] local+remote · [f] force local · [F] force local+remote · [esc] cancel"
	case d.hasLocal:
		hint = "[y] delete · [f] force delete · [esc] cancel"
	default:
		hint = "[y] delete remote · [esc] cancel"
	}

	lines := []string{confirmPromptS.Render(header)}
	if sub != "" {
		lines = append(lines, statusOkS.Render(sub))
	}
	lines = append(lines, help.Render(hint))
	return strings.Join(lines, "\n")
}

// renderStashModalInner is the shared 3-row skeleton for the stash modals:
// styled header, subject body (or fallback), and the hint matrix. Caller
// picks the header style (modalHeaderS for the picker, confirmPromptS for
// the drop confirm — same convention as the create/rename vs delete-confirm
// pair).
func renderStashModalInner(headerStyle lipgloss.Style, header, body, hint string) string {
	if body == "" {
		body = "(no subject)"
	}
	return strings.Join([]string{
		headerStyle.Render(header),
		statusOkS.Render(body),
		help.Render(hint),
	}, "\n")
}

func (m Model) renderStashActionPickerInner() string {
	p := m.pendingStashAction
	return renderStashModalInner(modalHeaderS, "[Stash "+p.label+"]", p.subject,
		"[p] pop · [a] apply · [esc] cancel")
}

func (m Model) renderStashDropConfirmInner() string {
	p := m.pendingStashDrop
	body := p.label
	if p.subject != "" {
		body = p.label + ": " + p.subject
	}
	return renderStashModalInner(confirmPromptS, "Drop stash?", body,
		"[y] drop · [esc] cancel")
}

// renderCheckoutConfirmInner returns the 3-row content for the dirty-tree
// confirm modal: bold "Uncommitted changes" header, a variant body line,
// and a variant hint line. The withPull case sits above the skip-reason
// case so a no-skip pull advertises "and pull"; the skip variant falls
// through to "stash & checkout" instead, since the chain elides the pull.
func (m Model) renderCheckoutConfirmInner() string {
	p := m.pendingCheckout

	var body, hint string
	switch {
	case p.withFF:
		body = "fast-forward '" + p.ref + "'?"
		hint = "[s] stash & fast-forward · [a] abort · [esc] cancel"
	case p.withCheckoutFF:
		body = "checkout '" + p.ref + "' and fast-forward?"
		hint = "[s] stash & checkout & fast-forward · [a] abort · [esc] cancel"
	case p.withPull && p.skipReason == "":
		body = "checkout '" + p.ref + "' and pull?"
		hint = "[s] stash & checkout & pull · [a] abort · [esc] cancel"
	case p.withPull:
		body = "checkout '" + p.ref + "' (pull skipped: " + p.skipReason + ")?"
		hint = "[s] stash & checkout · [a] abort · [esc] cancel"
	default:
		body = "checkout '" + p.ref + "'?"
		hint = "[s] stash & checkout · [a] abort · [esc] cancel"
	}

	return strings.Join([]string{
		confirmPromptS.Render("Uncommitted changes"),
		statusBusyS.Render(body),
		help.Render(hint),
	}, "\n")
}

var (
	borderUnfocused = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240"))
	borderFocused = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("205"))
	// modalBoxStyle frames the centered overlay modals (create / rename
	// name input, delete confirm, branch picker, dirty-tree confirm). It
	// reuses the focused pane's border color/shape so the modal reads as
	// "the new active surface" — same visual vocabulary, just centered.
	modalBoxStyle = borderFocused.Padding(0, 1)
	help          = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	statusBusyS   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	statusOkS     = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	statusErrS    = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
)

// renderModalBox wraps inner content in modalBoxStyle.
func renderModalBox(inner string) string {
	return modalBoxStyle.Render(inner)
}

const helpTextDiffWindow = "j/k scroll · pgup/pgdn page · esc/q close"

var helpRenderedDiffWindow = help.Render(helpTextDiffWindow)

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
	var rightCol string
	if m.mode == viewModeLocalChanges {
		treeFocused := m.focused != paneRefs && m.localChanges.Focused() == paneLCTree
		diffFocused := m.focused != paneRefs && m.localChanges.Focused() == paneLCDiff
		treeBox := boxStyle(treeFocused).Width(s.lcTreeW).Height(s.lcTreeH).Render(m.localChanges.TreeView())
		diffBox := boxStyle(diffFocused).Width(s.lcDiffW).Height(s.lcDiffH).Render(m.localChanges.DiffView())
		rightCol = lipgloss.JoinHorizontal(lipgloss.Top, treeBox, diffBox)
	} else {
		graphBox := boxStyle(m.focused == paneGraph).Width(s.graphW).Height(s.graphH).Render(m.graph.View())
		tabBox := boxStyle(m.focused == paneTab).Width(s.tabW).Height(s.tabH).Render(m.tabBody())
		rightCol = lipgloss.JoinVertical(lipgloss.Left, graphBox, tabBox)
	}
	main := lipgloss.JoinHorizontal(lipgloss.Top, refsBox, rightCol)
	base := lipgloss.JoinVertical(lipgloss.Left, main, m.renderHelpStatus())

	switch m.mode {
	case viewModeBranchPicker:
		return composeOverlay(base, renderModalBox(m.renderBranchPickerInner()), m.width, m.height)
	case viewModeRefNameInput:
		return composeOverlay(base, renderModalBox(m.renderRefNameInputInner()), m.width, m.height)
	case viewModeRefDeleteConfirm:
		return composeOverlay(base, renderModalBox(m.renderRefDeleteConfirmInner()), m.width, m.height)
	case viewModeCheckoutConfirm:
		return composeOverlay(base, renderModalBox(m.renderCheckoutConfirmInner()), m.width, m.height)
	case viewModeStashActionPicker:
		return composeOverlay(base, renderModalBox(m.renderStashActionPickerInner()), m.width, m.height)
	case viewModeStashDropConfirm:
		return composeOverlay(base, renderModalBox(m.renderStashDropConfirmInner()), m.width, m.height)
	}
	return base
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
//
// Centered modal modes (branch picker / ref name input / ref delete /
// dirty-tree checkout confirm) drop their hint here: the modal box owns
// its own [esc] hint row, so duplicating it on the bottom line would just
// double the prompt. A blank space keeps the row count stable across the
// modal toggle so View()'s base frame doesn't jump in height.
//
// viewModeHelp expands the bottom line into a multi-row panel so the
// shortcut reference can fit the full key matrix.
func (m Model) renderHelpStatus() string {
	switch m.mode {
	case viewModeBranchPicker, viewModeRefNameInput, viewModeRefDeleteConfirm, viewModeCheckoutConfirm,
		viewModeStashActionPicker, viewModeStashDropConfirm:
		return " "
	case viewModeHelp:
		return renderHelpPanel(m.width, m.helpReservedRows())
	}
	hint := paneHintTexts[m.focused]
	hintRendered := paneHintsRendered[m.focused]
	if m.mode == viewModeLocalChanges {
		hint = localChangesHintText
		hintRendered = localChangesHintRendered
	}
	if m.status == "" {
		return hintRendered
	}
	statusRendered := m.statusStyle.Render(m.status)

	avail := m.width - lipgloss.Width(statusRendered) - 1 // 1 for the spacer
	if avail < 1 {
		return statusRendered
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, fitHelpLine(hint, avail), " ", statusRendered)
}
