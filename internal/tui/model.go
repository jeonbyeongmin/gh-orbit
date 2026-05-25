// Package tui hosts the Bubble Tea models, panes, and key bindings for the
// stacked layout (top dashboard · commit graph filling the rest). The
// per-commit diff lives in the full-screen `d` patch overlay.
package tui

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jeonbyeongmin/gh-orbit/internal/config"
	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// focusFetchThrottle caps how often a `tea.FocusMsg` re-triggers a
// background fetch. Terminals + window managers burst FocusMsg on every
// alt-tab / desktop swap; a 60s floor keeps the cockpit's refs view fresh
// without turning the user's `git fetch` cadence into the WM's cadence.
// Hard-coded per CEO plan D5 (no config surface — HOLD SCOPE).
const focusFetchThrottle = 60 * time.Second

// clipboardWrite is the package-level seam for OS clipboard writes. Tests
// swap it with an in-memory buffer; production code defaults to atotto's
// platform-specific implementation (pbcopy on macOS, xclip/xsel on Linux,
// Win32 on Windows).
var clipboardWrite = clipboard.WriteAll

type pane int

const (
	paneGraph pane = iota
	// paneDashboard is the top worktree band when it grabs the cursor.
	// Toggled by `w` from paneGraph; j/k/enter/a/d/esc route to dashboard
	// handlers while the focus is on, all other keys fall through to the
	// normal-mode switch so global shortcuts (r, F, p, ?, ,) still work.
	paneDashboard
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
	// viewModeCheckoutConfirm gates the screen on an "uncommitted changes
	// — abort" prompt while pendingCheckout holds the ref the user picked.
	// The 3-pane layout stays visible underneath (so the user keeps their
	// context); only the help/status line below switches to the choice
	// keys, and every key except a/esc/ctrl+c is swallowed.
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
	// viewModeRefDeleteConfirm gates the screen on the inline branch-delete
	// confirm. Unlike the centered overlay modes above, this one paints its
	// prompt into the bottom hint line — no overlay box — to match the
	// worktree-remove pattern and keep the cursor anchored on the row being
	// acted upon. Only y/Y/esc/ctrl+c are accepted.
	viewModeRefDeleteConfirm
	// viewModeLocalChanges replaces the graph view with a file-tree + diff
	// layout for working-tree work. The top dashboard stays put so the
	// branch context is unchanged across the toggle. Entered via the `,`
	// keybind.
	viewModeLocalChanges
	// viewModeWorktreeAddInput hosts the branch-name textinput for the
	// add-worktree action, triggered by `a` on a worktree row in the
	// sidebar. The path is auto-derived (sibling directory of the active
	// worktree's parent) so a single input clears the modal.
	viewModeWorktreeAddInput
	// viewModeWorktreeRemoveConfirm hosts the remove-worktree confirm
	// prompt, triggered by `d` on a worktree row in the sidebar. Hint
	// matrix derives from dirty + locked: clean rows offer [y] remove,
	// dirty/locked rows require [Y] for force.
	viewModeWorktreeRemoveConfirm
	// viewModeZombieCleanupConfirm hosts the bulk zombie-branch cleanup
	// confirm. Triggered by `Z` on the refs pane; the modal lists every
	// local branch that satisfies the 3-condition guard (merged + upstream
	// gone + not checked out) and lets the user accept-all (y/Y) or abort
	// (esc). post-delete summary lands on the bottom status line with the
	// `git reflog` recovery hint.
	viewModeZombieCleanupConfirm
	// viewModeBranchesModal hosts the centered overlay listing every local
	// branch. Entered via `b` from viewModeNormal. `d` on the cursor row
	// arms viewModeRefDeleteConfirm against that branch — the delete-branch
	// chain stays single-codepath with the refs-pane inline d.
	viewModeBranchesModal
)

// helpExpandedHeight is the row count reserved for the bottom area when
// the `?` help panel is open. Each helpData category renders as a 1-line
// header + 1-line entries row, so 3 categories × 2 rows = 6. paneSizes
// clamps this on small terminals.
const helpExpandedHeight = 6

// pendingCheckout remembers what the user was trying to check out so the
// confirm modal's hint can name the chain it's aborting. detached=true
// means graph Enter resolved to detach (CheckoutDetached); detached=false
// means a refs-pane Enter (named ref) or a graph-Enter checkout to a
// chip-bearing branch.
//
// withFF / withCheckoutFF / ffHash flag the graph-Enter FF paths so the
// modal hint can name the chain. ref carries the local-branch name;
// ffHash carries the cursor commit MergeFFOnly should advance to.
// zombieCleanupState is the snapshot the confirm modal renders. baseline
// is the default branch that detect ran against (named in the modal so
// the user knows which "merged" was tested); branches is the candidate
// list — non-empty whenever the modal is open.
type zombieCleanupState struct {
	baseline string
	branches []git.ZombieBranch
}

type pendingCheckout struct {
	ref            string
	detached       bool
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
	// spinnerFrame is the Braille frame index for "running" agent markers.
	// Read by the dashboard row; advanced by the gated agentSpinnerTickMsg
	// (which only runs while a worktree is actually running).
	spinnerFrame int
	// statusTickSeq counts every status line that arms a tea.Tick auto-clear
	// (today: the switch-confirmation in switchWorktree). The dispatcher
	// captures the value at send time; on receipt the handler only clears
	// when m.statusTickSeq still matches, so a follow-up action that bumps
	// the counter can't be wiped by a stale tick.
	statusTickSeq uint64
	// diffReqID counts every patch-overlay dispatch (`d` press). Stale
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
	// lastFetchAt is the wall-clock of the most recent fetch *attempt* (F key
	// or focus-fetch). Two roles: (1) drives the 60s focus-fetch throttle so
	// a window manager that bursts FocusMsg on every alt-tab doesn't flood
	// `git fetch`; (2) feeds the sidebar footer's "fetched Xm ago" so the
	// user can tell whether the refs view is stale. Zero value = "never
	// fetched"; the footer stays blank until the first attempt.
	lastFetchAt time.Time
	// pullInFlight gates the p key. Tracked separately from fetchInFlight so
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
	// tree confirm modal can name what the user was attempting to do and
	// drop the slot on 'a'/esc.
	pendingCheckout pendingCheckout
	// actionInFlight gates graph-pane Enter while evaluateGraphActionCmd
	// is resolving the cursor's chip / ancestry state. Released by the
	// graphActionMsg handler before any follow-up cmd is dispatched —
	// downstream gates (checkoutInFlight / ffInFlight) take over from
	// there. A second Enter while resolving is swallowed.
	actionInFlight bool
	// ffInFlight gates graph Enter while ffOnlyCmd / checkoutThenFFCmd is
	// running. Tracked separately from checkoutInFlight so the FF and
	// checkout chains can't collide on a status overwrite. Cleared by
	// ffSucceededMsg / ffFailedMsg / ffNeedsCleanTreeMsg.
	ffInFlight bool
	// branchPicker backs viewModeBranchPicker. Reset to the zero value on
	// esc / enter; the picker reads candidates+cursor while open and
	// dispatches a graph-Enter checkout on enter.
	branchPicker branchPickerState
	// branchesModal backs viewModeBranchesModal. Cursor indexes into
	// m.refs.LocalRefs() at modal-open time. Reset on esc/q.
	branchesModal branchesModalState
	// dashboardFocus backs the top-dashboard focus mode (paneDashboard).
	// Cursor indexes into m.refs.Worktrees() at focus-on time. Reset to
	// zero on focus-off.
	dashboardFocus dashboardFocusState
	// pendingRefDelete backs viewModeRefDeleteConfirm. Stamped on `d`
	// keypress with the cursor's local-branch name; the inline-confirm
	// renderer / key router reads it without re-deriving from refs.
	pendingRefDelete refDeleteState
	// refActionInFlight gates the `d` key while a branch-delete cmd is
	// running. Distinct from checkoutInFlight so a stuck refs write can't
	// deadlock checkout / pull / FF chains.
	refActionInFlight bool
	// zombieCleanup backs viewModeZombieCleanupConfirm. Populated when the
	// detect dispatch returns a non-empty list; the modal renderer + key
	// router both read it without re-running the detection.
	zombieCleanup zombieCleanupState
	// zombieInFlight gates the `Z` key + the confirm's y/Y while a
	// detect-or-delete cmd is running so a second press can't fork a
	// parallel scan or double-delete the same list.
	zombieInFlight bool
	// localChanges hosts the file-tree + diff viewport rendered in place
	// of the graph when mode == viewModeLocalChanges. The graph model is
	// left untouched across the toggle so exiting the mode snaps back to
	// the exact previous state.
	localChanges localChangesModel
	// localChangesReqID counts every diff dispatch inside the Local
	// Changes mode. ApplyDiffLoaded compares against this + (path,
	// staged) to drop stale responses when the user keeps moving the
	// cursor mid-load.
	localChangesReqID uint64
	// sidebarWorktreesReqID counts every load fired by
	// refreshSidebarWorktreesCmd. The post-load worktreesLoadedMsg + each
	// dirty fan-out msg carry the same reqID so a switch issued mid-load
	// drops the stale data instead of letting it overwrite the new tree's
	// sidebar.
	sidebarWorktreesReqID uint64
	// worktreeAction backs the add-input / remove-confirm sub-modals
	// triggered from a worktree row (a / d). Reset to zero on esc /
	// success; while open the textinput owns key routing.
	worktreeAction worktreeActionState
	// watcher is the fsnotify-backed external-change detector. nil when
	// NewWithWatcher was not used (tests, embedded usage) or when
	// fsnotify.NewWatcher itself failed at startup; the worktreesLoadedMsg
	// handler nil-guards every call. Owned by main.go (Bind + Close), the
	// Model only borrows it.
	watcher *worktreeWatcher
	// externalWatchUnavailable is set by NewWithWatcher when fsnotify
	// initialization failed. Init() paints a one-shot status line so the
	// user knows automatic external refresh is off; `r` still works.
	externalWatchUnavailable bool
}

func New() Model {
	m := Model{
		focused:               paneGraph,
		refs:                  newRefsModel(),
		graph:                 newGraphModel(),
		diff:                  newDiffModel(),
		localChanges:          newLocalChangesModel(),
		currentRefs:           []string{refsAllSentinel},
		streamReqID:           1,
		sidebarWorktreesReqID: 1,
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

// NewWithWatcher builds the Model plus an attached external-change
// watcher. main.go uses this path so fsnotify init failure can flip the
// silent-degrade flag without affecting any other call site that uses
// the bare New() (tests, future embedders). The returned *worktreeWatcher
// is nil when fsnotify.NewWatcher failed — callers must nil-check before
// Bind / Close, mirroring the Model's own nil-guards.
func NewWithWatcher() (Model, *worktreeWatcher) {
	m := New()
	w, err := newWorktreeWatcher()
	if err != nil {
		log.Printf("external watch unavailable: %v", err)
		m.externalWatchUnavailable = true
		// Don't stomp a workdir / prefs error from New() — those matter more.
		if m.status == "" {
			m.status = "external watch unavailable — use 'r' to refresh"
			m.statusStyle = statusErrS
		}
		return m, nil
	}
	m.watcher = w
	return m, w
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		loadCommitsCmd(m.workdir, m.currentRefs, m.streamReqID),
		loadRefsCmd(m.workdir),
		loadHeadAncestorsCmd(m.workdir, m.streamReqID),
		loadWorktreesCmd(m.workdir, m.sidebarWorktreesReqID),
		loadLocalChangesSummaryCmd(m.workdir),
		agentSessionTickCmd(),
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

	case switchWorktreeMsg:
		next, cmd := m.switchWorktree(msg.path)
		return next, cmd

	case statusClearTickMsg:
		// Stale-tick gate: a re-switch (or any other path that bumps
		// statusTickSeq) invalidates this tick. Prefix check is a
		// belt-and-suspenders guard so a follow-up status overwrite
		// without a seq bump still survives.
		if msg.seq == m.statusTickSeq && strings.HasPrefix(m.status, "→ switched:") {
			m.status = ""
			m.statusStyle = statusOkS
		}
		return m, nil

	case worktreesLoadedMsg:
		// Drop stale loads (a switch or another reload bumped the reqID
		// between dispatch and reply).
		if msg.reqID != m.sidebarWorktreesReqID {
			return m, nil
		}
		m.refs.SetWorktrees(msg.entries, m.workdir)
		// Dashboard height is data-driven on len(worktrees); going from
		// 0 → N (or N → 0) shrinks/grows the graph pane, so resize the
		// graph viewport now to keep the bubbles list in sync.
		m.applyPaneSizes()
		paths := make([]string, 0, len(msg.entries))
		for _, e := range msg.entries {
			paths = append(paths, e.Path)
		}
		// Reconcile the external-change watcher with the fresh inventory.
		// add/remove/switch all funnel through this case, so Sync sees every
		// gitDir set change. nil-safe for the silent-degrade path.
		m.watcher.Sync(msg.entries)
		// Event-driven agent poll alongside the dirty fan-out: every inventory
		// change (startup, add/remove, switch) lights the 🤖 column now instead
		// of waiting up to one poll interval for the next tick. The 30s tick
		// stays as the ongoing refresh + freshness-aging loop.
		return m, tea.Batch(
			worktreeDirtyFanoutCmd(m.sidebarWorktreesReqID, paths),
			agentSessionPollCmd(agentSessionProjectsDir(), paths, time.Now(), m.sidebarWorktreesReqID),
		)

	case worktreeWatchedChangeMsg:
		// fsnotify saw HEAD or index settle on a watched worktree. Always
		// refresh the inventory so the row's branch / dirty marker tracks
		// the new state. If the event hit the current worktree, also fire
		// reloadCmd so graph + refs stay coherent — an external commit on
		// the tree we're viewing must surface as a new graph row, not just
		// a relabeled dashboard line. reloadCmd bumps reqID a second time,
		// which only burns one generation (stale-drop logic is reqID-equal,
		// not monotonic).
		m.sidebarWorktreesReqID++
		cmds := []tea.Cmd{loadWorktreesCmd(m.workdir, m.sidebarWorktreesReqID)}
		if msg.path == m.workdir {
			cmds = append(cmds, m.reloadCmd())
		}
		return m, tea.Batch(cmds...)

	case worktreesLoadFailedMsg:
		if msg.reqID != m.sidebarWorktreesReqID {
			return m, nil
		}
		// Soft-fail: the sidebar already shows whatever the previous load
		// produced. Surface the error on the status bar so the user knows
		// the inventory may be stale.
		m.status = "worktrees: " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil

	case worktreeDirtyResultMsg:
		if msg.reqID != m.sidebarWorktreesReqID {
			return m, nil
		}
		m.refs.SetWorktreeDirty(msg.path, msg.dirty, msg.timedOut)
		m.refs.SetWorktreeLastCommit(msg.path, msg.subject, msg.when)
		return m, nil

	case agentSessionTickMsg:
		// Poll cadence fired. Stat the current worktree set off the main loop
		// and re-arm the next tick in the same batch — the tick handler is the
		// sole re-arm site, so the loop stays single-lineage and survives a
		// dropped (stale) poll reply. Reading Worktrees() scopes the poll to
		// the live inventory; the reqID lets the reply drop if it races a change.
		wts := m.refs.Worktrees()
		paths := make([]string, 0, len(wts))
		for _, wt := range wts {
			paths = append(paths, wt.Path)
		}
		return m, tea.Batch(
			agentSessionPollCmd(agentSessionProjectsDir(), paths, time.Now(), m.sidebarWorktreesReqID),
			agentSessionTickCmd(),
		)

	case agentSessionPollMsg:
		// Drop a poll that raced an inventory change (mirrors the
		// worktreeDirtyResultMsg reqID guard) so it can't re-insert a key for a
		// pruned worktree. No re-arm here — the tick handler owns that.
		if msg.reqID != m.sidebarWorktreesReqID {
			return m, nil
		}
		for path, state := range msg.states {
			m.refs.SetAgentState(path, state)
		}
		return m, nil

	case worktreeAddSucceededMsg:
		if msg.reqID != m.worktreeAction.reqID {
			return m, nil
		}
		m.worktreeAction.actionInFlight = false
		m.worktreeAction.addInput = textinput.Model{}
		m.worktreeAction.addInlineErr = ""
		m.mode = viewModeNormal
		m.status = "worktree added: " + msg.branch + " → " + filepath.Base(msg.path)
		m.statusStyle = statusOkS
		m.sidebarWorktreesReqID++
		return m, loadWorktreesCmd(m.workdir, m.sidebarWorktreesReqID)

	case worktreeAddFailedMsg:
		if msg.reqID != m.worktreeAction.reqID {
			return m, nil
		}
		m.worktreeAction.actionInFlight = false
		m.worktreeAction.pendingAddPath = ""
		m.worktreeAction.addInlineErr = firstLine(msg.err.Error())
		// Stay in viewModeWorktreeAddInput so the user can fix the input.
		return m, nil

	case worktreeRemoveSucceededMsg:
		if msg.reqID != m.worktreeAction.reqID {
			return m, nil
		}
		m.worktreeAction.actionInFlight = false
		m.worktreeAction.removeTarget = git.Worktree{}
		m.mode = viewModeNormal
		m.status = "worktree removed: " + filepath.Base(msg.path)
		m.statusStyle = statusOkS
		m.sidebarWorktreesReqID++
		return m, loadWorktreesCmd(m.workdir, m.sidebarWorktreesReqID)

	case worktreeRemoveFailedMsg:
		if msg.reqID != m.worktreeAction.reqID {
			return m, nil
		}
		m.worktreeAction.actionInFlight = false
		m.worktreeAction.removeTarget = git.Worktree{}
		m.mode = viewModeNormal
		m.status = "remove: " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
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
		return m, cmd

	case checkoutSucceededMsg:
		m.checkoutInFlight = false
		m.pendingCheckout = pendingCheckout{}
		m.status = checkoutLabel(msg.ref, msg.detached)
		m.statusStyle = statusOkS
		m.pendingHEADHash = pendingHEADSentinel
		return m, m.reloadCmd()

	case checkoutNeedsCleanTreeMsg:
		// Modal owns the next decision; release the in-flight gate.
		// pendingCheckout stays intact so the modal hint can name the
		// chain that was about to run.
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

	case diffPatchLoadedMsg:
		m.diff.ApplyPatchLoaded(msg.reqID, msg.hash, msg.text)
		return m, nil
	case diffPatchFailedMsg:
		m.diff.ApplyPatchFailed(msg.reqID, msg.hash, msg.err)
		return m, nil

	case tea.FocusMsg:
		// xterm mode 1004 / TTY focus event: refresh the refs view in the
		// background so the cockpit sees PRs / merges that landed while the
		// window was inactive. fetchInFlight gate avoids piling on a manual
		// `F`; the throttle gate keeps an alt-tab burst from saturating the
		// network.
		if m.fetchInFlight {
			return m, nil
		}
		if !m.lastFetchAt.IsZero() && time.Since(m.lastFetchAt) < focusFetchThrottle {
			return m, nil
		}
		m.fetchInFlight = true
		m.lastFetchAt = time.Now()
		m.refs.SetLastFetchAt(m.lastFetchAt)
		return m, fetchCmd(m.workdir)

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

	case checkoutThenFFSucceededMsg:
		m.ffInFlight = false
		m.status = ffLabel(msg.branch, msg.advance) + " (after checkout)"
		m.statusStyle = statusOkS
		m.pendingHEADHash = pendingHEADSentinel
		return m, m.reloadCmd()

	case ffCheckoutNeedsCleanTreeMsg:
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

	case branchDeleteSucceededMsg:
		m.refActionInFlight = false
		m.mode = viewModeNormal
		m.pendingRefDelete = refDeleteState{}
		if msg.forced {
			m.status = "deleted '" + msg.localName + "' (forced)"
		} else {
			m.status = "deleted '" + msg.localName + "'"
		}
		m.statusStyle = statusOkS
		return m, m.reloadCmd()

	case branchDeleteFailedMsg:
		m.refActionInFlight = false
		m.mode = viewModeNormal
		m.pendingRefDelete = refDeleteState{}
		m.status = "delete failed: " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil

	case branchDeleteNotMergedMsg:
		// Re-arm the inline confirm with force=true so the user can press
		// `Y` to retry with -D. Stay in viewModeRefDeleteConfirm; the
		// renderer swaps the hint from "[y] delete" to "[Y] force delete"
		// off pendingRefDelete (which is still populated).
		m.refActionInFlight = false
		m.status = "'" + msg.localName + "' not fully merged — press [Y] to force"
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
		m.sidebarWorktreesReqID++
		return m, tea.Batch(
			loadStatusCmd(m.workdir),
			loadLocalChangesSummaryCmd(m.workdir),
			loadWorktreesCmd(m.workdir, m.sidebarWorktreesReqID),
		)

	case localChangesAddFailedMsg:
		m.status = "stage " + msg.path + ": " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil

	case localChangesRestoreSucceededMsg:
		m.status = "unstaged " + msg.path
		m.statusStyle = statusOkS
		m.sidebarWorktreesReqID++
		return m, tea.Batch(
			loadStatusCmd(m.workdir),
			loadLocalChangesSummaryCmd(m.workdir),
			loadWorktreesCmd(m.workdir, m.sidebarWorktreesReqID),
		)

	case localChangesRestoreFailedMsg:
		m.status = "unstage " + msg.path + ": " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil

	case localChangesSummaryLoadedMsg:
		m.refs.SetLocalChangesSummary(msg.summary, msg.loadedAt)
		return m, nil

	case localChangesSummaryFailedMsg:
		// Sidebar inline meta is a nice-to-have — a failed numstat (rare
		// outside detached HEAD without a HEAD ref) should not noise up
		// status; the bare label still renders.
		m.refs.ResetLocalChangesSummary()
		return m, nil

	case zombieDetectedMsg:
		m.zombieInFlight = false
		if len(msg.branches) == 0 {
			m.status = fmt.Sprintf("no zombie branches (merged into %s, upstream gone, not checked out)", msg.baseline)
			m.statusStyle = statusOkS
			return m, nil
		}
		m.zombieCleanup = zombieCleanupState(msg)
		m.mode = viewModeZombieCleanupConfirm
		m.status = ""
		return m, nil

	case zombieDetectFailedMsg:
		m.zombieInFlight = false
		m.status = "zombie scan: " + firstLine(msg.err.Error())
		m.statusStyle = statusErrS
		return m, nil

	case zombieDeletedMsg:
		m.zombieInFlight = false
		m.mode = viewModeNormal
		m.zombieCleanup = zombieCleanupState{}
		m.status, m.statusStyle = formatZombieSummary(msg.deleted, msg.failed)
		return m, m.reloadCmd()

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
			case "]":
				m.diff.JumpToNextFile()
				return m, nil
			case "[":
				m.diff.JumpToPrevFile()
				return m, nil
			}
			return m, nil
		}
		if m.mode == viewModeWorktreeAddInput {
			switch msg.String() {
			case "esc":
				m.mode = viewModeNormal
				m.worktreeAction.addInput = textinput.Model{}
				m.worktreeAction.addInlineErr = ""
				return m, nil
			case "ctrl+c":
				m.cancelStream()
				return m, tea.Quit
			case "enter":
				if m.worktreeAction.actionInFlight {
					return m, nil
				}
				branch := strings.TrimSpace(m.worktreeAction.addInput.Value())
				if branch == "" {
					m.worktreeAction.addInlineErr = "branch name is empty"
					return m, nil
				}
				path := deriveAddPath(m.workdir, branch)
				m.worktreeAction.actionInFlight = true
				m.worktreeAction.pendingAddPath = path
				m.worktreeAction.addInlineErr = ""
				return m, worktreeAddCmd(m.workdir, path, branch, m.worktreeAction.reqID)
			}
			var cmd tea.Cmd
			m.worktreeAction.addInput, cmd = m.worktreeAction.addInput.Update(msg)
			return m, cmd
		}
		if m.mode == viewModeWorktreeRemoveConfirm {
			if m.worktreeAction.actionInFlight {
				if msg.String() == "ctrl+c" {
					m.cancelStream()
					return m, tea.Quit
				}
				return m, nil
			}
			t := m.worktreeAction.removeTarget
			isDirty := m.refs.WorktreeDirty(t.Path)
			isLocked := t.Locked
			needsForce := isDirty || isLocked
			switch msg.String() {
			case "esc":
				m.mode = viewModeNormal
				m.worktreeAction.removeTarget = git.Worktree{}
				return m, nil
			case "ctrl+c":
				m.cancelStream()
				return m, tea.Quit
			case "y":
				if needsForce {
					m.mode = viewModeNormal
					m.worktreeAction.removeTarget = git.Worktree{}
					reason := "dirty"
					if isLocked && !isDirty {
						reason = "locked"
					}
					m.status = "remove: cancelled (" + reason + " — use [Y] to force)"
					m.statusStyle = statusOkS
					return m, nil
				}
				m.worktreeAction.actionInFlight = true
				return m, worktreeRemoveCmd(m.workdir, t.Path, false, m.worktreeAction.reqID)
			case "Y":
				if !needsForce {
					return m, nil
				}
				m.worktreeAction.actionInFlight = true
				return m, worktreeRemoveCmd(m.workdir, t.Path, true, m.worktreeAction.reqID)
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
			case "y":
				return m.dispatchRefDelete(false)
			case "Y":
				return m.dispatchRefDelete(true)
			}
			return m, nil
		}
		if m.mode == viewModeBranchesModal {
			switch msg.String() {
			case "j", "down":
				return m.branchesModalMoveCursor(1), nil
			case "k", "up":
				return m.branchesModalMoveCursor(-1), nil
			case "d":
				return m.beginBranchesModalDelete()
			case "esc", "q":
				m.mode = viewModeNormal
				m.branchesModal = branchesModalState{}
				return m, nil
			case "ctrl+c":
				m.cancelStream()
				return m, tea.Quit
			}
			return m, nil
		}
		if m.focused == paneDashboard && m.mode == viewModeNormal {
			switch msg.String() {
			case "j", "down":
				return m.dashboardMoveCursor(1), nil
			case "k", "up":
				return m.dashboardMoveCursor(-1), nil
			case "enter":
				return m.dashboardEnter()
			case "a":
				return m.dashboardAdd()
			case "d":
				return m.dashboardRemove()
			case "s":
				return m.dashboardToggleSort(), nil
			case "esc":
				m.focused = paneGraph
				m.dashboardFocus = dashboardFocusState{}
				return m, nil
			case "ctrl+c":
				m.cancelStream()
				return m, tea.Quit
			}
			// `w` and any other key fall through to the normal-mode
			// switch so the toggle-off case in `case "w"` fires and
			// global shortcuts (r, F, p, ?, ,) still work.
		}
		if m.mode == viewModeZombieCleanupConfirm {
			// While the bulk-delete cmd is in flight, only ctrl+c (quit)
			// is honored so a second y/Y can't fork a parallel sweep.
			if m.zombieInFlight {
				if msg.String() == "ctrl+c" {
					m.cancelStream()
					return m, tea.Quit
				}
				return m, nil
			}
			switch msg.String() {
			case "esc":
				m.mode = viewModeNormal
				m.zombieCleanup = zombieCleanupState{}
				m.status = "zombie cleanup: aborted"
				m.statusStyle = statusOkS
				return m, nil
			case "ctrl+c":
				m.cancelStream()
				return m, tea.Quit
			case "y", "Y":
				m.zombieInFlight = true
				m.status = fmt.Sprintf("deleting %d zombie branches…", len(m.zombieCleanup.branches))
				m.statusStyle = statusBusyS
				return m, deleteZombieBranchesCmd(m.workdir, m.zombieCleanup.branches)
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
			// Diff sub-focus owns viewport scroll.
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
		case "F":
			if m.fetchInFlight {
				return m, nil
			}
			m.fetchInFlight = true
			m.lastFetchAt = time.Now()
			m.refs.SetLastFetchAt(m.lastFetchAt)
			m.status = "fetching…"
			m.statusStyle = statusBusyS
			return m, fetchCmd(m.workdir)
		case "p":
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
		case "y":
			m = m.copyHashFromGraph()
			return m, nil
		case "R":
			// Swallow so capital R doesn't fall through to the focused
			// sub-model. Reserved for a future Rebase action.
			return m, nil
		case "Z":
			// Zombie-branch cleanup is a global action now that the sidebar
			// is gone — the previous paneRefs focus gate had no meaningful
			// successor, and the bulk-delete is the same regardless of
			// which pane the user is on.
			if m.zombieInFlight {
				return m, nil
			}
			m.zombieInFlight = true
			m.status = "scanning for zombie branches…"
			m.statusStyle = statusBusyS
			return m, detectZombieBranchesCmd(m.workdir)
		case "enter":
			// Graph is the only focused pane. The sidebar was retired in
			// PR B2; the bottom tab pane was retired with the subtract-
			// bottom-pane change. Worktree switch + Local Changes enter
			// come from `w` modal and `,` global.
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
			m.actionInFlight = true
			m.status = "→ resolving…"
			m.statusStyle = statusBusyS
			log.Printf("graph enter: dispatch evaluator (cursor=%s, locals=%d, remotes=%d)",
				shortHash(c.Hash), len(locals), len(remotes))
			return m, evaluateGraphActionCmd(m.workdir, c.Hash, locals, remotes)
		case "d":
			// `d` opens the patch overlay for the focused commit. Sidebar
			// is gone so the previous paneRefs interpretation (worktree
			// remove via cursor row) moved into the `w` worktree modal.
			c, ok := m.graph.Selected()
			if !ok {
				return m, nil
			}
			m.diffReqID++
			m.diff.BeginPatchLoad(c.Hash, m.diffReqID)
			m.mode = viewModeDiffWindow
			m.diff.SetPatchViewportSize(m.width, m.height-1)
			return m, loadDiffPatchCmd(m.workdir, c.Hash, m.diffReqID)
		case "b":
			// Branches modal — local-branch list with cursor + `d` delete
			// entry. Global, independent of focused pane.
			return m.beginBranchesModal()
		case "w":
			// Toggle the top dashboard's focus mode. While focused, the
			// dashboard owns j/k/enter/a/d/esc; everything else (r, F, p,
			// ?, ,) still falls through to the normal-mode switch.
			if m.focused == paneDashboard {
				m.focused = paneGraph
				m.dashboardFocus = dashboardFocusState{}
				return m, nil
			}
			return m.enterDashboardFocus()
		}
		var cmd tea.Cmd
		m.graph, cmd = m.graph.Update(msg)
		return m, cmd
	}
	return m, nil
}

// applyPaneSizes recomputes the inner content dimensions for every sub-model
// from the current width/height. refs is a pure storage model
// post-sidebar-shell-subtract, so it owns no size of its own — the
// dashboard reads m.refs directly with the width handed to it by View.
func (m *Model) applyPaneSizes() {
	s := m.paneSizes()
	m.graph.SetSize(s.graphW, s.graphH)
	if m.mode == viewModeLocalChanges {
		m.localChanges.SetSize(s.lcTreeW, s.lcTreeH, s.lcDiffW, s.lcDiffH)
	}
}

// enterLocalChangesMode flips into the working-tree view and kicks off the
// first status load. refs / graph models are left untouched so exit returns
// to the exact prior state. focused stays on paneGraph (the only outer
// focus); the sub-focus inside the mode starts on the tree.
func (m *Model) enterLocalChangesMode() tea.Cmd {
	m.mode = viewModeLocalChanges
	m.focused = paneGraph
	m.localChanges.SetFocus(paneLCTree)
	m.applyPaneSizes()
	return tea.Batch(loadStatusCmd(m.workdir), loadLocalChangesSummaryCmd(m.workdir))
}

// exitLocalChangesMode flips back to the normal layout. Entries / cursor
// state are kept so re-entry restores them; the diff body is released so
// a large untracked-file diff doesn't sit resident between sessions.
func (m *Model) exitLocalChangesMode() {
	m.mode = viewModeNormal
	m.localChanges.ClosePatch()
	m.applyPaneSizes()
}

// cycleLocalChangesFocus implements the 2-way tab cycle inside the mode:
// tree → diff → tree. Sidebar focus retired in PR B2 so the outer focus
// stays on paneGraph and only the localChanges sub-focus toggles.
func (m Model) cycleLocalChangesFocus() Model {
	if m.localChanges.Focused() == paneLCTree {
		m.localChanges.SetFocus(paneLCDiff)
	} else {
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

// copyHashFromGraph handles `y`: copies the focused commit's full hash to
// the OS clipboard. Surfaces success ("copied <short>") or the OS error
// (typical: xclip/xsel missing on Linux) through the status line so the
// user always knows whether the clipboard was actually written.
func (m Model) copyHashFromGraph() Model {
	c, ok := m.graph.Selected()
	if !ok {
		return m
	}
	if err := clipboardWrite(c.Hash); err != nil {
		m.status = "clipboard unavailable: " + firstLine(err.Error())
		m.statusStyle = statusErrS
		return m
	}
	m.status = "copied " + shortHash(c.Hash)
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

// checkoutLabel renders the user-facing "checkout: …" prefix shared by the
// busy and success status lines. Detached checkouts short-hash the ref
// since the user picked a commit, not a name.
func checkoutLabel(ref string, detached bool) string {
	if detached {
		return "checkout: detached at " + shortHash(ref)
	}
	return "checkout: " + ref
}

// dispatchRefDelete fires branchDeleteCmd with the in-flight gate armed.
// force=false picks `git branch -d`; force=true picks `-D`. The inline
// prompt stays open while the cmd runs — branchDeleteSucceededMsg /
// FailedMsg / NotMergedMsg close (or re-arm) it.
func (m Model) dispatchRefDelete(force bool) (Model, tea.Cmd) {
	d := m.pendingRefDelete
	m.refActionInFlight = true
	if force {
		m.status = "deleting '" + d.localName + "' (forced)…"
	} else {
		m.status = "deleting '" + d.localName + "'…"
	}
	m.statusStyle = statusBusyS
	return m, branchDeleteCmd(m.workdir, d.localName, force)
}

// ffLabel renders the user-facing "fast-forward: <branch> +<N>" status
// prefix shared by busy and success lines. Mirrors checkoutLabel's role
// for the FF path.
func ffLabel(branch string, advance int) string {
	return fmt.Sprintf("fast-forward: %s +%d", branch, advance)
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
// stream's commitsStreamDoneMsg.err. The sidebar's cursor state
// (onWorktree / onLocalChanges) is preserved across the reload by
// refModel.ResetForReload — no per-ref persist handle needed now that the
// refs LIST is gone. Worktree inventory + its dirty fan-out are refreshed
// here too — sidebarWorktreesReqID bumps before dispatch so any in-flight
// fan-out from the previous load is invalidated by stale-drop.
func (m *Model) reloadCmd() tea.Cmd {
	m.cancelStream()
	m.streamReqID++
	m.sidebarWorktreesReqID++
	resetCmd := m.graph.ResetForReload()
	m.refs.ResetForReload()
	return tea.Batch(
		resetCmd,
		loadCommitsCmd(m.workdir, m.currentRefs, m.streamReqID),
		loadRefsCmd(m.workdir),
		loadHeadAncestorsCmd(m.workdir, m.streamReqID),
		loadWorktreesCmd(m.workdir, m.sidebarWorktreesReqID),
	)
}

// paneSizes holds the inner content dimensions for each rendered box. The
// outer (bordered) widths/heights are content + 2 along each axis. The
// layout stacks the top dashboard above the graph, full terminal width.
type paneSizes struct {
	dashW, dashH   int
	graphW, graphH int
	// lcTreeW/H, lcDiffW/H carry the in-mode split when mode ==
	// viewModeLocalChanges. Zero in any other mode — graphW/H stays
	// authoritative there.
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
	// Sidebar retired in PR B2; the bottom tab pane retired with the
	// subtract-bottom-pane change. The dashboard (top) + graph now stack
	// vertically across the full terminal width. Each box claims 2 cols
	// of border around its content.
	outerW := m.width

	// Dashboard: header + N worktree rows + separator + 2 border rows.
	// Data-driven; 0 (no dashboard) when there are no worktrees yet.
	var dashOuterH int
	if inner := dashboardLines(m); inner > 0 {
		dashOuterH = inner + 2
		if dashOuterH > mainH-3 {
			// Never starve the graph; cap the dashboard so the graph
			// gets ≥3 outer rows.
			dashOuterH = mainH - 3
			if dashOuterH < 0 {
				dashOuterH = 0
			}
		}
	}
	graphOuterH := mainH - dashOuterH
	if graphOuterH < 3 {
		graphOuterH = 3
	}

	s.dashW = outerW - 2
	s.dashH = dashOuterH - 2
	if s.dashW < 1 {
		s.dashW = 1
	}
	if s.dashH < 0 {
		s.dashH = 0
	}

	s.graphW = outerW - 2
	if s.graphW < 1 {
		s.graphW = 1
	}
	s.graphH = graphOuterH - 2
	if s.graphH < 1 {
		s.graphH = 1
	}

	// Local Changes mode replaces the graph with a horizontal tree | diff
	// split (35% to the file list). Reuses the full outer width.
	if m.mode == viewModeLocalChanges {
		treeOuterW := outerW * localChangesTreeRatio / 100
		if treeOuterW < 12 {
			treeOuterW = 12
		}
		if treeOuterW > outerW-12 {
			treeOuterW = outerW - 12
		}
		diffOuterW := outerW - treeOuterW
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

// localChangesTreeRatio is the percent of the full width given to the
// tree column in viewModeLocalChanges; the diff viewport takes the
// remainder. Tuned so paths still breathe on a typical 120-col terminal.
const localChangesTreeRatio = 35

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

// modalHeaderS is the bold style applied to the header row of the branch
// picker modal. Checkout confirm uses confirmPromptS (busy-color + bold);
// modalHeaderS is plain-bold so the picker doesn't read as a "warning".
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

// refDeleteInlineHint returns the bottom-hint prompt rendered while
// viewModeRefDeleteConfirm is active. It paints into the standard status
// line (no centered overlay) so the cursor row stays anchored to the ref
// being acted upon — matching the worktree-remove inline pattern.
func (m Model) refDeleteInlineHint() string {
	d := m.pendingRefDelete
	prompt := confirmPromptS.Render("delete '"+d.localName+"'?") + " " +
		help.Render("[y] delete · [Y] force · [esc] cancel")
	if m.status == "" {
		return prompt
	}
	statusRendered := m.statusStyle.Render(m.status)
	avail := m.width - lipgloss.Width(prompt) - 1
	if avail < 1 {
		return prompt
	}
	if lipgloss.Width(statusRendered) > avail {
		return prompt
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, prompt, " ", statusRendered)
}

// renderCheckoutConfirmInner returns the 3-row content for the dirty-tree
// confirm modal: bold "Uncommitted changes" header, a variant body line,
// and an abort hint. The hint stays uniform across variants since the only
// way forward through a dirty tree is to commit / drop changes outside the
// cockpit, then retry — the modal exists to name what was attempted.
func (m Model) renderCheckoutConfirmInner() string {
	p := m.pendingCheckout

	var body string
	switch {
	case p.withFF:
		body = "fast-forward '" + p.ref + "'?"
	case p.withCheckoutFF:
		body = "checkout '" + p.ref + "' and fast-forward?"
	default:
		body = "checkout '" + p.ref + "'?"
	}

	return strings.Join([]string{
		confirmPromptS.Render("Uncommitted changes"),
		statusBusyS.Render(body),
		help.Render("[a] abort · [esc] cancel"),
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

// diffOverlayHintBase is the keymap half of the bottom hint shown inside
// the patch overlay. The current-file half (path + N/M) is prepended at
// render time by renderDiffOverlayHint when files() is non-empty.
const diffOverlayHintBase = "j/k scroll · pgup/pgdn page · [ ] file · esc/q close"

// renderDiffOverlayHint builds the patch-overlay bottom line. For commits
// with at least one file boundary it leads with `<path> [N/M] · `; for
// empty diffs (merge commits) it falls back to the bare keymap so the
// line doesn't read as " [0/0]". When the path makes the line overflow
// the terminal width, the path is truncated from its left so the file
// name (the discriminating tail) survives.
func (m Model) renderDiffOverlayHint() string {
	path, idx, total := m.diff.CurrentFile()
	if total == 0 || path == "" {
		return fitHelpLine(diffOverlayHintBase, m.width)
	}
	prefix := fmt.Sprintf("%s [%d/%d] · ", path, idx, total)
	full := prefix + diffOverlayHintBase
	if lipgloss.Width(full) <= m.width {
		return help.Render(full)
	}
	// Path overflows. Reserve room for the suffix + `…[N/M] · ` ellipsis
	// pad, then truncate the path from the left so the basename survives.
	tail := fmt.Sprintf(" [%d/%d] · ", idx, total) + diffOverlayHintBase
	budget := m.width - lipgloss.Width(tail) - 1
	if budget < 4 {
		return fitHelpLine(diffOverlayHintBase, m.width)
	}
	for i := 0; i < len(path); i++ {
		candidate := "…" + path[i:]
		if lipgloss.Width(candidate) <= budget {
			return help.Render(candidate + tail)
		}
	}
	return fitHelpLine(diffOverlayHintBase, m.width)
}

// confirmPromptS reuses the busy color and adds bold so the modal prompt
// reads as "active dialog" rather than "an error just landed".
var confirmPromptS = statusBusyS.Bold(true)

func (m Model) View() string {
	if m.width == 0 {
		return "starting…"
	}
	if m.mode == viewModeDiffWindow {
		return lipgloss.JoinVertical(lipgloss.Left, m.diff.PatchView(), m.renderDiffOverlayHint())
	}
	s := m.paneSizes()

	var main string
	if m.mode == viewModeLocalChanges {
		treeFocused := m.localChanges.Focused() == paneLCTree
		diffFocused := m.localChanges.Focused() == paneLCDiff
		treeBox := boxStyle(treeFocused).Width(s.lcTreeW).Height(s.lcTreeH).Render(m.localChanges.TreeView())
		diffBox := boxStyle(diffFocused).Width(s.lcDiffW).Height(s.lcDiffH).Render(m.localChanges.DiffView())
		main = lipgloss.JoinHorizontal(lipgloss.Top, treeBox, diffBox)
	} else {
		graphBox := boxStyle(m.focused == paneGraph).Width(s.graphW).Height(s.graphH).Render(m.graph.View())
		if s.dashH > 0 {
			dashBox := boxStyle(m.focused == paneDashboard).Width(s.dashW).Height(s.dashH).Render(renderTopDashboard(m, s.dashW))
			main = lipgloss.JoinVertical(lipgloss.Left, dashBox, graphBox)
		} else {
			main = graphBox
		}
	}
	base := lipgloss.JoinVertical(lipgloss.Left, main, m.renderHelpStatus())

	switch m.mode {
	case viewModeBranchPicker:
		return composeOverlay(base, renderModalBox(m.renderBranchPickerInner()), m.width, m.height)
	case viewModeBranchesModal:
		return composeOverlay(base, renderModalBox(m.renderBranchesModalInner()), m.width, m.height)
	case viewModeCheckoutConfirm:
		return composeOverlay(base, renderModalBox(m.renderCheckoutConfirmInner()), m.width, m.height)
	case viewModeWorktreeAddInput:
		return composeOverlay(base, renderModalBox(m.renderWorktreeAddInputInner()), m.width, m.height)
	case viewModeWorktreeRemoveConfirm:
		return composeOverlay(base, renderModalBox(m.renderWorktreeRemoveConfirmInner()), m.width, m.height)
	case viewModeZombieCleanupConfirm:
		return composeOverlay(base, renderModalBox(m.renderZombieCleanupConfirmInner()), m.width, m.height)
	}
	return base
}

func boxStyle(focused bool) lipgloss.Style {
	if focused {
		return borderFocused
	}
	return borderUnfocused
}

// renderHelpStatus lays out the bottom line as "help … status". When the
// terminal is too narrow to fit both, status wins — the user just triggered
// an action and seeing its outcome matters more than the help reminder.
//
// Centered modal modes (branch picker / dirty-tree checkout confirm /
// worktree modals) drop their hint here: the modal box owns its own [esc]
// hint row, so duplicating it on the bottom line would just double the
// prompt. A blank space keeps the row count stable across the modal toggle
// so View()'s base frame doesn't jump in height.
//
// viewModeRefDeleteConfirm renders inline: there is no overlay box, so the
// bottom line itself shows the `delete '<branch>'? [y] / [Y] / [esc]`
// prompt. The cursor stays on the row being acted upon — matching the
// worktree-remove inline pattern.
//
// viewModeHelp expands the bottom line into a multi-row panel so the
// shortcut reference can fit the full key matrix.
func (m Model) renderHelpStatus() string {
	switch m.mode {
	case viewModeBranchPicker, viewModeBranchesModal,
		viewModeCheckoutConfirm, viewModeWorktreeAddInput,
		viewModeWorktreeRemoveConfirm, viewModeZombieCleanupConfirm:
		return " "
	case viewModeRefDeleteConfirm:
		return m.refDeleteInlineHint()
	case viewModeHelp:
		return renderHelpPanel(m.width, m.helpReservedRows())
	}
	hint := graphHintText
	hintRendered := graphHintRendered
	if m.mode == viewModeLocalChanges {
		hint = localChangesHintText
		hintRendered = localChangesHintRendered
	}
	// paneDashboard owns the cursor → replace the graph hint with the
	// dashboard-scoped key matrix. Local Changes is mutually exclusive
	// (enterLocalChangesMode resets focused to paneGraph) so the order
	// doesn't matter, but keep this last for explicit precedence.
	if m.focused == paneDashboard {
		hint = dashboardFocusHintText
		hintRendered = dashboardFocusHintRendered
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
