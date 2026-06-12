// Package tui hosts the Bubble Tea models, panes, and key bindings for the
// full-screen commit graph plus its overlay modals. The
// per-commit diff lives in the full-screen `d` patch overlay.
package tui

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/atotto/clipboard"
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
	// panel is open — `?` re-toggles, j/k navigate, etc. — so the
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
	// layout for working-tree work. Entered via the `,` keybind.
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
	// viewModeWorktreesModal hosts the centered overlay listing every
	// worktree. Entered via `w` from viewModeNormal — same pattern as the
	// branches modal. enter switches, a/d reuse the existing add-input /
	// remove-confirm sub-modals, s toggles last-commit sort.
	viewModeWorktreesModal
	// viewModeRebaseConfirm gates the screen on the inline "rebase <head>
	// onto <cursor>?" confirm. Bottom-hint style like the branch-delete
	// confirm — the cursor stays anchored on the onto-row. Only
	// y/esc/ctrl+c are accepted.
	viewModeRebaseConfirm
)

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
	// ctrl+c invoke it so the git process is reaped instead of leaking.
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
	// quitArmed is set by the first ctrl+c and cleared by any other key.
	// While armed, the status line shows the quit hint and a second ctrl+c
	// actually quits. No timer — disarm is purely key-driven, handled at the
	// top of the tea.KeyMsg branch in Update.
	quitArmed bool
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
	// pullAfterAction arms the "enter on origin/xx" chain: set when the
	// graphActionMsg dispatch carried pullAfter, consumed by the
	// checkout/FF success handlers (which then fire pullCmd) and cleared
	// on every failure / clean-tree detour so an aborted chain can't pull
	// later by surprise.
	pullAfterAction bool
	// branchPicker backs viewModeBranchPicker. Reset to the zero value on
	// esc / enter; the picker reads candidates+cursor while open and
	// dispatches a graph-Enter checkout on enter.
	branchPicker branchPickerState
	// branchesModal backs viewModeBranchesModal. Cursor indexes into
	// m.refs.LocalRefs() at modal-open time. Reset on esc.
	branchesModal branchesModalState
	// worktreesModal backs viewModeWorktreesModal. Cursor indexes into
	// m.modalWorktrees() at modal-open time. Reset to zero on close.
	worktreesModal worktreesModalState
	// pendingRebase backs viewModeRebaseConfirm. Stamped on `R` with the
	// cursor hash + display label + HEAD branch; consumed by the confirm
	// key handler. Reset on esc / dispatch.
	pendingRebase pendingRebase
	// rebaseInFlight gates `R` while rebaseCmd is running. Cleared by the
	// three rebase outcome msgs.
	rebaseInFlight bool
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

	case tea.KeyMsg:
		return m.updateKey(msg)

	case switchWorktreeMsg,
		worktreesLoadedMsg,
		worktreeWatchedChangeMsg,
		worktreesLoadFailedMsg,
		worktreeDirtyResultMsg,
		worktreeAddSucceededMsg,
		worktreeAddFailedMsg,
		worktreeRemoveSucceededMsg,
		worktreeRemoveFailedMsg:
		return m.updateWorktreeMsg(msg)

	case commitsStreamStartedMsg,
		commitsAppendedMsg,
		commitsStreamDoneMsg,
		headAncestorsLoadedMsg,
		refsLoadedMsg,
		refsLoadFailedMsg,
		diffPatchLoadedMsg,
		diffPatchFailedMsg:
		return m.updateCommitsMsg(msg)

	case checkoutSucceededMsg,
		checkoutNeedsCleanTreeMsg,
		checkoutFailedMsg,
		graphActionMsg,
		ffSucceededMsg,
		ffFailedMsg,
		ffNeedsCleanTreeMsg,
		checkoutThenFFSucceededMsg,
		ffCheckoutNeedsCleanTreeMsg,
		rebaseSucceededMsg,
		rebaseConflictMsg,
		rebaseFailedMsg:
		return m.updateCheckoutMsg(msg)

	case tea.FocusMsg,
		fetchSucceededMsg,
		fetchFailedMsg,
		pullSucceededMsg,
		pullConflictMsg,
		pullFailedMsg:
		return m.updateFetchPullMsg(msg)

	case branchDeleteSucceededMsg,
		branchDeleteFailedMsg,
		branchDeleteNotMergedMsg,
		zombieDetectedMsg,
		zombieDetectFailedMsg,
		zombieDeletedMsg:
		return m.updateBranchOpsMsg(msg)

	case localChangesEnterRequestedMsg,
		localChangesStatusLoadedMsg,
		localChangesStatusFailedMsg,
		localChangesDiffLoadedMsg,
		localChangesDiffFailedMsg,
		localChangesAddSucceededMsg,
		localChangesAddFailedMsg,
		localChangesRestoreSucceededMsg,
		localChangesRestoreFailedMsg:
		return m.updateLocalChangesMsg(msg)
	}
	return m, nil
}

// quitArmHint is the status line shown after the first ctrl+c. Kept as a
// const so the disarm chokepoint in updateKey can match it exactly before
// clearing — an unrelated status message is left untouched.
const quitArmHint = "^C again to quit"

// handleCtrlC implements the two-press quit. The first press arms quit and
// paints quitArmHint; the second (while still armed) cancels the in-flight
// stream and quits. Disarm happens key-driven at the top of the tea.KeyMsg
// branch, so no timer is involved. Every ctrl+c site routes through here to
// keep the rule uniform across modes; callers must re-assign the returned
// Model (`return m.handleCtrlC()`) or the armed flag is lost.
func (m Model) handleCtrlC() (Model, tea.Cmd) {
	if m.quitArmed {
		m.cancelStream()
		return m, tea.Quit
	}
	m.quitArmed = true
	m.status = quitArmHint
	m.statusStyle = statusErrS
	return m, nil
}

// applyPaneSizes recomputes the inner content dimensions for every sub-model
// from the current width/height. refs is a pure storage model
// post-sidebar-shell-subtract, so it owns no size of its own — the
// modal reads m.refs directly at render time.
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
// graph fills the full terminal; worktrees live behind the `w` modal.
type paneSizes struct {
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
	// Reserve 1 row for the bottom help/status line, or the full inline panel
	// height when `?` is open. The centered overlay modal modes (branchPicker /
	// branchesModal / checkoutConfirm / worktree sub-modals) are painted on top
	// of the unchanged 3-pane base by composeOverlay, so they reserve no extra
	// rows here — only viewModeHelp grows the bottom region.
	helpReserved := 1
	if m.mode == viewModeHelp {
		helpReserved = m.helpReservedRows()
	}
	mainH := m.height - helpReserved
	if mainH < 1 {
		mainH = 1
	}
	// Sidebar retired in PR B2; the bottom tab pane retired with the
	// subtract-bottom-pane change; the top dashboard retired with the
	// worktrees-modal change. The graph owns the whole main area, full
	// terminal width. Each box claims 2 cols of border around its content.
	outerW := m.width
	graphOuterH := mainH
	if graphOuterH < 3 {
		graphOuterH = 3
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

// helpReservedRows returns how many bottom rows the inline `?` help panel
// claims. It's the column layout's natural height (tallest category +
// header), clamped so the graph keeps priority: never more than half the
// screen, and never so tall that the main area drops below 3 rows. Floor: 1.
func (m Model) helpReservedRows() int {
	want := lipgloss.Height(renderHelpColumns())
	want = max(min(want, m.height/2), 3)
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

	lines := []string{modalHeaderS.Render("[Branch select]")}
	lines = append(lines, renderScrollWindow(
		m.branchPicker.viewportTop, visibleRows, len(m.branchPicker.candidates),
		func(i int) string {
			if i == m.branchPicker.cursor {
				return selectedStyle.Render("> " + m.branchPicker.candidates[i])
			}
			return "  " + m.branchPicker.candidates[i]
		})...)
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
const diffOverlayHintBase = "j/k scroll · pgup/pgdn page · [ ] file · esc close"

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
		main = boxStyle(m.focused == paneGraph).Width(s.graphW).Height(s.graphH).Render(m.graph.View())
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
	case viewModeWorktreesModal:
		return composeOverlay(base, renderModalBox(m.renderWorktreesModalInner()), m.width, m.height)
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
	case viewModeRebaseConfirm:
		return m.rebaseInlineHint()
	case viewModeHelp:
		// Inline column reference panel, grown out of the footer over the rows
		// helpReservedRows() carved from the graph.
		return renderHelpExpanded(m.width, m.helpReservedRows())
	}
	// Normal operation: a single pressable `? help` token + the status
	// message. The full key reference lives behind the `?` inline panel.
	if m.status == "" {
		return collapsedHintRendered
	}
	statusRendered := m.statusStyle.Render(m.status)

	avail := m.width - lipgloss.Width(statusRendered) - 1 // 1 for the spacer
	if avail < 1 {
		return statusRendered
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, fitHelpLine(collapsedHintText, avail), " ", statusRendered)
}
