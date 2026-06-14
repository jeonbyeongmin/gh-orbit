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
	"github.com/charmbracelet/bubbles/textarea"
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
	// viewModeBranchPicker gates the screen on a "pick which local branch"
	// modal triggered when the graph Enter evaluator returns multiple
	// chips at the cursor row (graphActionPicker). The 3-pane layout stays
	// visible underneath; only j/k/enter/esc are accepted while open.
	viewModeBranchPicker
	// viewModeRefDeleteConfirm gates the screen on the branch-delete
	// confirm dialog (centered overlay, same surface as every other
	// confirm). The status row inside the box carries the deleting… /
	// not-merged-retry feedback while the dialog stays open. Only
	// y/Y/esc/ctrl+c are accepted.
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
	// viewModePRsModal hosts the centered overlay listing every open PR.
	// Entered via `l` from viewModeNormal — same pattern as the branches /
	// worktrees modals. enter opens the cursor PR in the review overlay
	// (beginPRReviewFor), reaching PRs whose head branch isn't checked out
	// (which the `O` cursor path can't).
	viewModePRsModal
	// viewModeRebaseConfirm gates the screen on the "rebase <head>
	// onto <cursor>?" confirm dialog (centered overlay like the
	// branch-delete confirm). Only y/esc/ctrl+c are accepted.
	viewModeRebaseConfirm
	// viewModeCherryPickConfirm gates the screen on the
	// "cherry-pick <hash> onto <head>?" confirm dialog. Same surface
	// contract as the rebase confirm.
	viewModeCherryPickConfirm
	// viewModeBranchCreateInput hosts the branch-name textinput for `n`
	// (create branch at cursor + switch). Same modal shape as the
	// worktree add input.
	viewModeBranchCreateInput
	// viewModeRevertConfirm gates the "revert <hash> on <head>?" confirm
	// dialog (`v`). Same single-y surface as the cherry-pick confirm.
	viewModeRevertConfirm
	// viewModeResetConfirm gates the "reset <head> to <cursor>?" confirm
	// dialog (`x`). Unlike the single-y confirms it offers s/m/h for the
	// three reset modes; the hard row warns about working-tree loss.
	viewModeResetConfirm
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
	// prs is the head-branch → open-PR map behind the chip PR badges and
	// the `O` key. Loaded by `gh pr list` at startup, on `r`, and after
	// every successful fetch — the same cadence the user already expects
	// remote state to refresh on. Nil until the first load lands.
	prs map[string]prInfo
	// prList is the same open PRs in gh's newest-first order, backing the `l`
	// PR list modal — the surface that reaches PRs whose head branch isn't
	// checked out (which the `O` cursor path can't). Loaded alongside prs.
	prList []prInfo
	// prsInFlight gates PR-list dispatches so an F-spam can't stack
	// parallel `gh pr list` calls (and a slow older reply can't overwrite
	// a newer one). New() arms it because Init always dispatches.
	prsInFlight bool
	// reviewPRNumber is the open PR whose diff currently fills the patch
	// overlay (0 = the overlay shows a plain commit patch, not a PR). Set by
	// `O` (beginPRReview), cleared on overlay close / merge. While non-zero
	// the overlay's bottom line is renderPRReviewHint and `a`/`m` arm the
	// inline approve/merge confirms — see prreview.go.
	reviewPRNumber int
	// reviewReturnMode is where the PR-review overlay returns when it closes
	// (esc) or merges. Zero value (viewModeNormal) = the graph; `O` from the
	// worktree dashboard sets viewModeWorktreesModal so the review-and-compare
	// loop stays on the dashboard. A general "return here" slot rather than a
	// per-origin boolean, so future launchers set their own target.
	reviewReturnMode viewMode
	// prAction is the inline confirm sub-state inside the PR overlay
	// (none / approve / merge). Non-none gates the overlay keymap to the
	// confirm keys and swaps the hint to the confirm prompt.
	prAction prAction
	// prReviewInFlight gates the overlay keys (ctrl+c only) while an approve
	// or merge gh call runs, mirroring refActionInFlight for the delete
	// confirm. The busy status it sets drives the spinner via statusIsBusy.
	prReviewInFlight bool
	// prReviewNotice is the one-shot approve-ok / action-failed line shown in
	// the overlay hint (the diff View() never renders m.status). Cleared on
	// the next overlay keypress; prReviewNoticeErr picks its color.
	prReviewNotice    string
	prReviewNoticeErr bool
	// prReviewBody is the comment / request-changes textarea, live while
	// prAction is prActionComment / prActionRequestChanges. prReviewBodyErr is
	// the inline editor error (empty-body guard / gh failure) — a failed submit
	// keeps the editor open with the cause shown instead of closing it.
	prReviewBody    textarea.Model
	prReviewBodyErr string
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
	// tryHEADJump (called from refsLoadedMsg, commitsAppendedMsg, and
	// commitsStreamDoneMsg) clears it once the row lands in the NEW
	// window — it holds while graph.pendingSwap keeps the old rows on
	// screen. Empty string means "no pending jump".
	pendingHEADHash string
	// status is the one-line message rendered next to the help line:
	// "fetching…", "fetch: done", "fetch failed: …". Empty hides it.
	// statusStyle decides the color; zero value renders without color.
	status      string
	statusStyle lipgloss.Style
	// statusBusyText remembers the exact text of the last setBusyStatus
	// call. While m.status still equals it, the status line is an
	// in-flight operation and gets the animated spinner prefix; any
	// handler that overwrites m.status breaks the match and the spinner
	// disappears with it. Never cleared — a stale value can't match a
	// non-busy status text.
	statusBusyText string
	// spinnerFrame / spinnerArmed drive the gated loading-spinner tick.
	// armed means a spinnerTickCmd is in flight; the Update wrapper arms
	// it whenever spinnerVisible() flips true, and the spinnerTickMsg
	// handler stops re-arming once nothing is loading.
	spinnerFrame int
	spinnerArmed bool
	// quitArmed is set by the first ctrl+c and cleared by any other key.
	// While armed, the status line shows the quit hint and a second ctrl+c
	// actually quits. No timer — disarm is purely key-driven, handled at the
	// top of the tea.KeyMsg branch in Update.
	quitArmed bool
	// helpOpen toggles the inline `?` reference panel. It is orthogonal to
	// mode — the panel grows out of the footer on whichever page is showing
	// (graph / worktree / local changes) and the page's own keys keep working
	// while it's open, so it stays a reference, not a modal. showsHelp() gates
	// rendering to the bare page modes so a confirm/overlay hides it.
	helpOpen bool
	// checkoutInFlight gates Enter on the refs pane and Enter on the graph
	// while a background checkout is running. fetch/pull have their own
	// gates; git's .git/index.lock is the real serialization point.
	checkoutInFlight bool
	// pendingCheckout is set the moment beginCheckout fires so the dirty-
	// tree confirm modal can name what the user was attempting to do and
	// drop the slot on 'a'/esc.
	pendingCheckout pendingCheckout
	// stashNotice is armed by the dirty-tree confirm's `s` dispatch with
	// the local branch the auto-stash was taken on (empty when no stash
	// chain is in flight — and on a detached HEAD, whose stash just loses
	// its hints). The next checkout/FF terminal msg consumes it via
	// consumeStashNotice.
	stashNotice string
	// stashedRefs remembers, for this session, the branches stash-and-
	// continue left an auto-stash on. A later successful checkout back
	// onto one appends the `git stash pop` reminder to the status line.
	// Entries are never removed — a repeat reminder after a manual pop is
	// cheaper than asking `git stash list` on every checkout.
	stashedRefs map[string]bool
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
	// on every failure so an aborted chain can't pull later by surprise.
	// The clean-tree detour keeps it armed while the modal decides: `s`
	// (stash & continue) carries it through the retry, abort clears it.
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
	// prsModal backs viewModePRsModal. Cursor indexes into m.prList at
	// modal-open time. Reset to zero on close.
	prsModal prsModalState
	// pendingRebase backs viewModeRebaseConfirm. Stamped on `R` with the
	// cursor hash + display label + HEAD branch; consumed by the confirm
	// key handler. Reset on esc / dispatch.
	pendingRebase pendingRebase
	// rebaseInFlight gates `R` while rebaseCmd is running. Cleared by the
	// three rebase outcome msgs.
	rebaseInFlight bool
	// pendingCherryPick / cherryPickInFlight back viewModeCherryPickConfirm
	// and the `c` dispatch gate — same lifecycle as the rebase pair.
	pendingCherryPick  pendingCherryPick
	cherryPickInFlight bool
	// pendingRevert / revertInFlight back viewModeRevertConfirm and the `v`
	// dispatch gate — same lifecycle as the cherry-pick pair.
	pendingRevert  pendingRevert
	revertInFlight bool
	// pendingReset / resetInFlight back viewModeResetConfirm and the `x`
	// flow. resetInFlight latches across both the async ancestor/push
	// evaluation (evaluateResetCmd) and the reset itself.
	pendingReset  pendingReset
	resetInFlight bool
	// branchCreate backs viewModeBranchCreateInput (`n`).
	branchCreate branchCreateState
	// pushInFlight gates `P` while pushCmd is running.
	pushInFlight bool
	// pendingRefDelete backs viewModeRefDeleteConfirm. Stamped on `d`
	// keypress with the cursor's local-branch name; the confirm-dialog
	// renderer / key router reads it without re-deriving from refs.
	pendingRefDelete refDeleteState
	// refActionInFlight gates the confirm dialog's keys (and swaps its
	// hint to deleting…) while a branch-delete cmd is running. Distinct
	// from checkoutInFlight so a stuck refs write can't deadlock
	// checkout / pull / FF chains.
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
		prsInFlight:           true, // Init dispatches the first prListCmd
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
		prListCmd(m.workdir),
	)
}

// Update wraps update with the spinner-tick arming gate: after any message
// lands, if something is now loading and no tick is in flight, one gets
// armed. Centralizing the arm here means no dispatch site has to remember
// to start the animation.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	// Every update path returns the concrete Model; a panic here means a
	// handler broke that contract and should fail loudly.
	nm := next.(Model)
	if !nm.spinnerArmed && nm.spinnerVisible() {
		nm.spinnerArmed = true
		cmd = tea.Batch(cmd, spinnerTickCmd())
	}
	return nm, cmd
}

// spinnerVisible reports whether anything on screen is currently rendering
// the loading spinner — the gate for keeping the animation tick alive.
// Every branch terminates: graph/local-changes loads flip loaded=true even
// on error, diff clears loadingPatch on failure, and a busy status only
// matches statusBusyText until the operation's terminal handler overwrites
// the status line.
func (m Model) spinnerVisible() bool {
	// Before the first WindowSizeMsg nothing renders ("starting…"), so
	// there is no spinner to animate yet.
	if m.width == 0 {
		return false
	}
	if m.statusIsBusy() {
		return true
	}
	switch m.mode {
	case viewModeDiffWindow:
		return m.diff.loadingPatch
	case viewModeLocalChanges:
		return !m.localChanges.loaded || m.localChanges.diffLoading
	default:
		return !m.graph.loaded
	}
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinnerTickMsg:
		if !m.spinnerVisible() {
			m.spinnerArmed = false
			return m, nil
		}
		m.spinnerFrame++
		m.graph.spinnerFrame = m.spinnerFrame
		m.diff.spinnerFrame = m.spinnerFrame
		m.localChanges.spinnerFrame = m.spinnerFrame
		return m, spinnerTickCmd()

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
		stashFailedMsg,
		graphActionMsg,
		ffSucceededMsg,
		ffFailedMsg,
		ffNeedsCleanTreeMsg,
		checkoutThenFFSucceededMsg,
		ffCheckoutNeedsCleanTreeMsg,
		rebaseSucceededMsg,
		rebaseConflictMsg,
		rebaseFailedMsg,
		cherryPickSucceededMsg,
		cherryPickConflictMsg,
		cherryPickFailedMsg,
		revertSucceededMsg,
		revertConflictMsg,
		revertFailedMsg,
		resetEvalMsg,
		resetSucceededMsg,
		resetFailedMsg,
		branchCreateSucceededMsg,
		branchCreateFailedMsg,
		pushSucceededMsg,
		pushFailedMsg,
		browseFailedMsg,
		prBrowseOpenedMsg:
		return m.updateCheckoutMsg(msg)

	case prApproveDoneMsg,
		prApproveFailedMsg,
		prMergeDoneMsg,
		prMergeFailedMsg,
		prReviewBodyDoneMsg,
		prReviewBodyFailedMsg:
		return m.updatePRReviewMsg(msg)

	case tea.FocusMsg,
		fetchSucceededMsg,
		fetchFailedMsg,
		prsLoadedMsg,
		prsLoadFailedMsg,
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
		localChangesRestoreFailedMsg,
		localChangesApplySucceededMsg,
		localChangesApplyFailedMsg:
		return m.updateLocalChangesMsg(msg)
	}
	return m, nil
}

// gitMutationInFlight reports whether any working-tree / history mutating
// action is mid-flight (graph enter chain, rebase, cherry-pick, branch
// create, push). Every mutating begin* gate checks this one predicate so
// two git writers can never race on the same index/HEAD.
func (m Model) gitMutationInFlight() bool {
	return m.actionInFlight || m.checkoutInFlight || m.ffInFlight ||
		m.rebaseInFlight || m.cherryPickInFlight || m.branchCreate.inFlight ||
		m.pushInFlight || m.revertInFlight || m.resetInFlight
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

// enterGraphPage returns to the commit graph — the home page of the
// tab/shift+tab cycle. Local Changes entries / cursor state are kept so
// re-entry restores them, but the diff body is released so a large
// untracked-file patch doesn't sit resident behind the graph.
func (m Model) enterGraphPage() Model {
	if m.mode == viewModeLocalChanges {
		m.localChanges.ClosePatch()
	}
	m.mode = viewModeNormal
	m.applyPaneSizes()
	return m
}

// toggleHelp flips the inline `?` reference panel and reflows the current page
// around the reserved rows. Shared by every page's `?` handler so the toggle
// contract stays in one place.
func (m Model) toggleHelp() (tea.Model, tea.Cmd) {
	m.helpOpen = !m.helpOpen
	m.applyPaneSizes()
	return m, nil
}

// enterLocalChangesDiff drills the tree into the diff for the cursor entry:
// flips the sub-focus to the diff pane and loads its patch. The lazy
// counterpart to the retired tab toggle — the diff isn't fetched until the
// user asks to see it. No-op on an empty tree (nothing to drill into).
func (m Model) enterLocalChangesDiff() (tea.Model, tea.Cmd) {
	if _, ok := m.localChanges.CurrentEntry(); !ok {
		return m, nil
	}
	m.localChanges.SetFocus(paneLCDiff)
	return m.dispatchLocalChangesDiff()
}

// handleLocalChangesTreeKey routes j/k/g/G/space inside the tree pane. Cursor
// moves don't fetch a diff — the diff pane is single-pane drill-down, loaded
// lazily on `enter` (enterLocalChangesDiff), so moving the cursor while the
// tree is on screen costs nothing. space toggles the entry between Staged and
// Unstaged via Add / RestoreStaged.
func (m Model) handleLocalChangesTreeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		m.localChanges.MoveCursor(1)
		return m, nil
	case "k", "up":
		m.localChanges.MoveCursor(-1)
		return m, nil
	case "g":
		m.localChanges.JumpCursor(false)
		return m, nil
	case "G":
		m.localChanges.JumpCursor(true)
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
		m.setBusyStatus("unstage " + e.Path + "…")
		return m, restoreStagedCmd(m.workdir, e.Path)
	}
	m.localChanges.ScheduleSelectAfterReload(e.Path, true)
	m.setBusyStatus("stage " + e.Path + "…")
	return m, addCmd(m.workdir, e.Path)
}

// dispatchLocalChangesStageHunk stages (or, for a staged entry, unstages) just
// the focused hunk in the diff pane via `git apply --cached`. Untracked /
// conflict files have no index baseline to apply a hunk against, so they fall
// back to whole-file `space` in the tree — surfaced as a hint here. The
// pending-select hint keeps the cursor on the same path after the post-apply
// status reload.
func (m Model) dispatchLocalChangesStageHunk() (tea.Model, tea.Cmd) {
	e, ok := m.localChanges.CurrentEntry()
	if !ok {
		return m, nil
	}
	if e.Untracked || e.Conflict {
		m.status = "hunk staging: untracked/conflict stage whole-file (tree + space)"
		m.statusStyle = statusErrS
		return m, nil
	}
	hunkIdx, ok := m.localChanges.CurrentHunk()
	if !ok {
		m.status = "no hunk to stage"
		m.statusStyle = statusErrS
		return m, nil
	}
	m.localChanges.ScheduleSelectAfterReload(e.Path, e.Staged())
	if e.Staged() {
		m.setBusyStatus("unstage hunk in " + e.Path + "…")
	} else {
		m.setBusyStatus("stage hunk in " + e.Path + "…")
	}
	return m, stageHunkCmd(m.workdir, e.Path, e.Staged(), hunkIdx)
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
	m.setBusyStatus(checkoutLabel(ref, detached) + " …")
	return m, checkoutCmd(m.workdir, ref, detached)
}

// setBusyStatus paints an in-flight status line (busy color) and records
// its exact text in statusBusyText so renderHelpStatus prefixes the
// animated spinner while — and only while — that text is still on screen.
// Use for statuses that describe work in progress; terminal outcomes keep
// the plain m.status assignment.
func (m *Model) setBusyStatus(text string) {
	m.status = text
	m.statusStyle = statusBusyS
	m.statusBusyText = text
}

// statusIsBusy reports whether the status line currently shows the
// in-flight text recorded by setBusyStatus — the single predicate behind
// both the spinner prefix (renderHelpStatus) and the tick gate
// (spinnerVisible).
func (m Model) statusIsBusy() bool {
	return m.status != "" && m.status == m.statusBusyText
}

// consumeStashNotice folds the one-shot stash-and-continue reminder into
// the just-set status line: the suffix tells the user which branch their
// changes were stashed on, and the branch is recorded so checking it out
// again surfaces the pop hint. No-op when no stash chain was in flight.
func (m Model) consumeStashNotice() Model {
	if m.stashNotice == "" {
		return m
	}
	if m.stashedRefs == nil {
		m.stashedRefs = make(map[string]bool)
	}
	m.stashedRefs[m.stashNotice] = true
	if m.statusIsBusy() {
		// The suffix can land on an in-flight status (the checkout/FF
		// success handler chains a pull before consuming the notice) —
		// extend the recorded busy text too so the spinner match survives.
		m.statusBusyText += " · stashed on " + m.stashNotice
	}
	m.status += " · stashed on " + m.stashNotice
	m.stashNotice = ""
	return m
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
// force=false picks `git branch -d`; force=true picks `-D`. The confirm
// dialog stays open while the cmd runs (its hint row swaps to deleting…
// and the key handler swallows everything but ctrl+c) —
// branchDeleteSucceededMsg / FailedMsg / NotMergedMsg close (or re-arm)
// it.
func (m Model) dispatchRefDelete(force bool) (Model, tea.Cmd) {
	m.refActionInFlight = true
	return m, branchDeleteCmd(m.workdir, m.pendingRefDelete.localName, force)
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
// from refsLoadedMsg, commitsAppendedMsg, and commitsStreamDoneMsg —
// whichever sees the row first wins. Empty / sentinel pendingHEADHash →
// no-op.
func (m Model) tryHEADJump() Model {
	if m.pendingHEADHash == "" || m.pendingHEADHash == pendingHEADSentinel {
		return m
	}
	// While a stale-while-revalidate window is open the visible rows are
	// the OLD graph — jumping would consume the hash against rows the
	// swap is about to replace (and the swap's Select(0) would then undo
	// it). Keep the hash armed; the post-swap commitsAppendedMsg calls
	// back in once fresh rows exist.
	if m.graph.pendingSwap {
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

// reloadCmd marks the graph stale-while-revalidate and dispatches fresh
// log + refs queries: the old graph keeps rendering until the new stream's
// first batch swaps it out, so reload-heavy paths (watcher, post-checkout)
// don't flash a blank "loading…" frame. A stale ref in m.currentRefs
// surfaces via the new stream's commitsStreamDoneMsg.err. The sidebar's
// cursor state (onWorktree / onLocalChanges) is preserved across the
// reload by refModel.ResetForReload — no per-ref persist handle needed now
// that the refs LIST is gone. Worktree inventory + its dirty fan-out are
// refreshed here too — sidebarWorktreesReqID bumps before dispatch so any
// in-flight fan-out from the previous load is invalidated by stale-drop.
//
// Reloads where the old graph would mislead (worktree switch — a different
// tree entirely) must hard-reset via graph.ResetForReload before calling
// this; MarkStaleForReload then no-ops on the unloaded graph.
func (m *Model) reloadCmd() tea.Cmd {
	m.cancelStream()
	m.streamReqID++
	m.sidebarWorktreesReqID++
	m.graph.MarkStaleForReload()
	m.refs.ResetForReload()
	return tea.Batch(
		loadCommitsCmd(m.workdir, m.currentRefs, m.streamReqID),
		loadRefsCmd(m.workdir),
		loadHeadAncestorsCmd(m.workdir, m.streamReqID),
		loadWorktreesCmd(m.workdir, m.sidebarWorktreesReqID),
	)
}

// dispatchPRList fires prListCmd behind the prsInFlight gate. Returns nil
// while a list is already loading — callers batch the result only when
// non-nil. Deliberately not part of reloadCmd: watcher-driven reloads fire
// on every local commit, and local commits don't change PR
// state — only Init / `r` / a successful fetch do the gh round-trip.
func (m *Model) dispatchPRList() tea.Cmd {
	if m.prsInFlight {
		return nil
	}
	m.prsInFlight = true
	return prListCmd(m.workdir)
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
	// of the unchanged base by composeOverlay, so they reserve no extra rows
	// here — only an open help panel (showsHelp) grows the bottom region.
	helpReserved := 1
	if m.showsHelp() {
		helpReserved = m.helpReservedRows()
	}
	// pageTabsRows reserves the top breadcrumb row; it shows on every page
	// (the diff overlay returns before paneSizes, so it isn't affected).
	mainH := m.height - helpReserved - pageTabsRows
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

	// Local Changes mode is a single-pane drill-down: the tree OR the diff
	// fills the full main area (same seam as the graph). Both panes share the
	// graph's content box so flipping focus never reflows the layout.
	if m.mode == viewModeLocalChanges {
		s.lcTreeW = s.graphW
		s.lcTreeH = s.graphH
		s.lcDiffW = s.graphW
		s.lcDiffH = s.graphH
	}
	return s
}

// helpReservedRows returns how many bottom rows the inline `?` help panel
// claims. It's the column layout's natural height (tallest category +
// header), clamped so the graph keeps priority: never more than half the
// screen, and never so tall that the main area drops below 3 rows. Floor: 1.
func (m Model) helpReservedRows() int {
	want := lipgloss.Height(renderHelpColumns(helpCategoriesFor(m.currentPageIndex())))
	want = max(min(want, m.height/2), 3)
	// Keep ≥3 main rows after BOTH the help panel and the breadcrumb row are
	// carved off (paneSizes subtracts pageTabsRows too) — without the
	// -pageTabsRows the composed view overflows by a row on short terminals.
	if upper := m.height - 3 - pageTabsRows; upper > 0 {
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

// renderRefDeleteConfirmInner returns the branch-delete confirm dialog
// content. Delete-flow feedback renders from refDeleteState + the
// in-flight gate — never from the global m.status, which mode-blind
// async handlers (fetch / push / pull done) keep writing while the
// dialog is open. The in-flight hint swap mirrors the worktree-remove
// and zombie-cleanup confirms.
func (m Model) renderRefDeleteConfirmInner() string {
	d := m.pendingRefDelete
	rows := []string{confirmPromptS.Render("delete '" + d.localName + "'?")}
	if d.notMerged {
		rows = append(rows, statusErrS.Render("not fully merged — press [Y] to force"))
	}
	hintText := "[y] delete · [Y] force · [esc] cancel"
	if m.refActionInFlight {
		hintText = "deleting…"
	}
	rows = append(rows, help.Render(hintText))
	return strings.Join(rows, "\n")
}

// renderCheckoutConfirmInner returns the 3-row content for the dirty-tree
// confirm modal: bold "Uncommitted changes" header, a variant body line,
// and the option hint. `s` stashes the changes (incl. untracked) and
// replays the interrupted chain — the stash stays put for a later pop;
// `a` / esc leave the working tree alone. The hint stays uniform across
// variants — "continue" reads correctly for all three chains.
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
		help.Render("[s] stash & continue · [a] abort · [esc] cancel"),
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
	// pageTabActiveS / pageTabInactiveS style the top breadcrumb that names
	// the current page. Active reuses the focused-pane accent (205); inactive
	// reuses the dim help color so the three labels read as one quiet bar.
	pageTabActiveS   = lipgloss.NewStyle().Foreground(lipgloss.Color(colorSelected)).Bold(true)
	pageTabInactiveS = lipgloss.NewStyle().Foreground(lipgloss.Color(colorDim))
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
		hint := m.renderDiffOverlayHint()
		if m.reviewPRNumber != 0 {
			hint = m.renderPRReviewHint()
		}
		diffBase := lipgloss.JoinVertical(lipgloss.Left, m.diff.PatchView(), hint)
		// An armed approve / merge confirm is a centered dialog composed over
		// the diff itself (not the graph) — the diff dims behind the box so
		// the reviewer keeps it in view while deciding.
		if m.reviewPRNumber != 0 && m.prAction != prActionNone {
			return composeOverlay(diffBase, renderModalBox(m.renderPRActionConfirmInner()), m.width, m.height)
		}
		return diffBase
	}
	s := m.paneSizes()

	var main string
	switch {
	case m.mode == viewModeLocalChanges:
		// Drill-down: render only the focused pane, full-screen. `enter`
		// descends tree → diff; `esc` climbs back (see handleLocalChangesKey).
		if m.localChanges.Focused() == paneLCTree {
			main = boxStyle(true).Width(s.lcTreeW).Height(s.lcTreeH).Render(m.localChanges.TreeView())
		} else {
			main = boxStyle(true).Width(s.lcDiffW).Height(s.lcDiffH).Render(m.localChanges.DiffView())
		}
	case m.isWorktreesSurface():
		// Full-screen worktree dashboard replaces the graph (same seam as
		// Local Changes). Its add / remove confirm sub-modals keep the
		// dashboard as their backdrop via isWorktreesSurface so the centered
		// confirm box composes over it, not over the graph.
		main = boxStyle(true).Width(s.graphW).Height(s.graphH).Render(m.renderWorktreesView(s.graphW, s.graphH))
	default:
		main = boxStyle(m.focused == paneGraph).Width(s.graphW).Height(s.graphH).Render(m.graph.View())
	}
	base := lipgloss.JoinVertical(lipgloss.Left, m.renderPageTabs(), main, m.renderHelpStatus())

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
	case viewModePRsModal:
		return composeOverlay(base, renderModalBox(m.renderPRsModalInner()), m.width, m.height)
	case viewModeBranchCreateInput:
		return composeOverlay(base, renderModalBox(m.renderBranchCreateInputInner()), m.width, m.height)
	case viewModeRefDeleteConfirm:
		return composeOverlay(base, renderModalBox(m.renderRefDeleteConfirmInner()), m.width, m.height)
	case viewModeRebaseConfirm:
		return composeOverlay(base, renderModalBox(m.renderRebaseConfirmInner()), m.width, m.height)
	case viewModeCherryPickConfirm:
		return composeOverlay(base, renderModalBox(m.renderCherryPickConfirmInner()), m.width, m.height)
	case viewModeRevertConfirm:
		return composeOverlay(base, renderModalBox(m.renderRevertConfirmInner()), m.width, m.height)
	case viewModeResetConfirm:
		return composeOverlay(base, renderModalBox(m.renderResetConfirmInner()), m.width, m.height)
	}
	return base
}

func boxStyle(focused bool) lipgloss.Style {
	if focused {
		return borderFocused
	}
	return borderUnfocused
}

// isWorktreesSurface reports whether the full-screen worktree dashboard owns
// the main area — the dashboard itself or one of its centered confirm
// sub-modals (add / remove), which compose over the dashboard as their
// backdrop rather than over the graph.
func (m Model) isWorktreesSurface() bool {
	switch m.mode {
	case viewModeWorktreesModal, viewModeWorktreeAddInput, viewModeWorktreeRemoveConfirm:
		return true
	}
	return false
}

// pageTabsRows is the single row the page breadcrumb claims at the top of
// every page. Page navigation moved entirely onto the tab/shift+tab cycle
// (the `w` / `,` keys were retired), so the breadcrumb is the only always-on
// signal of which page is showing and that the others exist.
const pageTabsRows = 1

// pageTabLabels are the breadcrumb labels in cycle order: tab advances left to
// right (wrapping), shift+tab reverses. Index matches currentPageIndex.
var pageTabLabels = [...]string{"Graph", "Worktree", "Local Changes"}

// currentPageIndex maps the viewMode to its top-level page (0 graph, 1
// worktree, 2 local changes). Overlays / confirms resolve to the page they
// compose over (worktree sub-modals → worktree, everything else → graph) so
// the breadcrumb stays steady while a modal is open.
func (m Model) currentPageIndex() int {
	switch {
	case m.mode == viewModeLocalChanges:
		return 2
	case m.isWorktreesSurface():
		return 1
	default:
		return 0
	}
}

// isPageMode reports whether the bare top-level page owns the screen (graph /
// worktree / local changes) — i.e. no overlay, confirm, or sub-modal is up.
// The inline help panel only shows on these.
func (m Model) isPageMode() bool {
	switch m.mode {
	case viewModeNormal, viewModeWorktreesModal, viewModeLocalChanges:
		return true
	}
	return false
}

// showsHelp reports whether the inline `?` panel is currently painted: it is
// open AND a bare page owns the screen (opening a modal hides it without
// clearing the toggle, so closing the modal restores it).
func (m Model) showsHelp() bool { return m.helpOpen && m.isPageMode() }

// renderPageTabs draws the top breadcrumb: the three pages with the active one
// accented, plus a right-aligned cycle hint. One row tall (pageTabsRows).
func (m Model) renderPageTabs() string {
	cur := m.currentPageIndex()
	parts := make([]string, len(pageTabLabels))
	for i, label := range pageTabLabels {
		if i == cur {
			parts[i] = pageTabActiveS.Render(label)
		} else {
			parts[i] = pageTabInactiveS.Render(label)
		}
	}
	tabs := strings.Join(parts, pageTabInactiveS.Render(" · "))
	hint := help.Render("tab / shift+tab")
	// Drop the cycle hint before it would wrap the row on a narrow terminal —
	// the labels (and the active highlight) are the load-bearing part.
	if lipgloss.Width(tabs)+lipgloss.Width(hint)+1 > m.width {
		return tabs
	}
	return layoutLeftRight(tabs, hint, m.width)
}

// renderHelpStatus lays out the bottom line as "help … status". When the
// terminal is too narrow to fit both, status wins — the user just triggered
// an action and seeing its outcome matters more than the help reminder.
//
// Centered modal modes (branch picker / confirms / worktree modals) drop
// their hint here: the modal box owns its own hint row, so duplicating it
// on the bottom line would just double the prompt. A blank space keeps the
// row count stable across the modal toggle so View()'s base frame doesn't
// jump in height.
//
// An open help panel (showsHelp) expands the bottom line into a multi-row
// panel so the shortcut reference can fit the current page's key matrix.
func (m Model) renderHelpStatus() string {
	if m.showsHelp() {
		// Inline column reference panel, grown out of the footer over the rows
		// helpReservedRows() carved from the current page. Shows only that
		// page's categories (helpCategoriesFor).
		return renderHelpExpanded(helpCategoriesFor(m.currentPageIndex()), m.width, m.helpReservedRows())
	}
	switch m.mode {
	case viewModeBranchPicker, viewModeBranchesModal, viewModePRsModal,
		viewModeCheckoutConfirm, viewModeWorktreeAddInput,
		viewModeWorktreeRemoveConfirm, viewModeZombieCleanupConfirm,
		viewModeBranchCreateInput, viewModeRefDeleteConfirm,
		viewModeRebaseConfirm, viewModeCherryPickConfirm,
		viewModeRevertConfirm, viewModeResetConfirm:
		return " "
	}
	// Normal operation: the status message on the left, a single pressable
	// `? help` token pinned to the right edge. The full key reference lives
	// behind the `?` inline panel.
	if m.status == "" {
		return lipgloss.PlaceHorizontal(m.width, lipgloss.Right, collapsedHintRendered)
	}
	statusText := m.status
	if m.statusIsBusy() {
		statusText = spinnerGlyph(m.spinnerFrame) + " " + m.status
	}
	statusRendered := m.statusStyle.Render(statusText)

	avail := m.width - lipgloss.Width(statusRendered) - 1 // 1 for the spacer
	if avail < 1 {
		return statusRendered
	}
	helpRendered := fitHelpLine(collapsedHintText, avail)
	gap := m.width - lipgloss.Width(statusRendered) - lipgloss.Width(helpRendered)
	if gap < 1 {
		gap = 1
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, statusRendered, strings.Repeat(" ", gap), helpRendered)
}
