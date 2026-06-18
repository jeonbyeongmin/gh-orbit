// Async cmds and msg shapes for the Local Changes view. Mirrors refsaction.go's
// seam pattern: each git wrapper is reachable through a package-level var so
// tests can stub the subprocess calls; each cmd emits a typed msg the root
// model branches on.
package tui

import (
	"context"
	"errors"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

const localChangesCmdTimeout = 60 * time.Second

// maxUntrackedNumstatProbes caps how many untracked files get a per-file
// `git diff --no-index --numstat` probe per status load. The poll re-runs the
// load every ~1s, so an unbounded loop over a large untracked dir would fork a
// process storm; beyond the cap untracked rows render without a "+N" count.
const maxUntrackedNumstatProbes = 50

// Package-level seams over git.* — tests stub these to keep the cmd suite
// hermetic (no real subprocess in unit tests).
var (
	statusExec               = git.Status
	diffNumstatExec          = git.DiffNumstat
	diffUntrackedNumstatExec = git.DiffUntrackedNumstat
	diffFileExec             = git.DiffFile
	diffFileRawExec          = git.DiffFile // the apply-patch source (see stageHunkCmd)
	diffUntrackedExec        = git.DiffUntracked
	addExec                  = git.Add
	restoreStagedExec        = git.RestoreStaged
	applyCachedExec          = git.ApplyCached
	stashAllExec             = git.StashPush
	resetHardExec            = git.Reset
	cleanExec                = git.Clean
)

// Status load. The numstat slices carry the per-file +/- counts the tree
// renders alongside each row; they're best-effort (a probe failure just drops
// the column) and split by side because the counts come from two diffs.
type localChangesStatusLoadedMsg struct {
	entries      []git.StatusEntry
	unstagedStat []git.FileStat
	stagedStat   []git.FileStat
	// preserveCursor is set by the background poll (loadStatusCmd's second
	// arg) so the handler re-pins the tree cursor to its file after the
	// reclassify — a 1s refresh shouldn't drift the selection out from under
	// the user. Action reloads leave it false and use the pendingSelect hint.
	preserveCursor bool
}

type localChangesStatusFailedMsg struct {
	err error
}

// Diff load
type localChangesDiffLoadedMsg struct {
	reqID  uint64
	path   string
	staged bool
	text   string
}

type localChangesDiffFailedMsg struct {
	reqID  uint64
	path   string
	staged bool
	err    error
}

// Stage / unstage action results
type localChangesAddSucceededMsg struct {
	path string
}

type localChangesAddFailedMsg struct {
	path string
	err  error
}

type localChangesRestoreSucceededMsg struct {
	path string
}

type localChangesRestoreFailedMsg struct {
	path string
	err  error
}

// Per-hunk apply results. staged carries the side the hunk came from so the
// success handler can re-land the cursor on the same tree row.
type localChangesApplySucceededMsg struct {
	path   string
	staged bool
}

type localChangesApplyFailedMsg struct {
	path string
	err  error
}

// Mode entry signal — refs.go emits this when enter is pressed on the sticky
// "Local Changes" row, so the root model can flip viewMode + kick off the
// first status load.
type localChangesEnterRequestedMsg struct{}

// loadStatusCmd dispatches a fresh `git status --porcelain=v2` snapshot plus
// the two `git diff --numstat` probes that feed the per-row +/- column. The
// numstat probes are best-effort: a failure leaves the slice nil and the tree
// renders without stats rather than failing the whole reload. preserveCursor
// rides through to the msg so the poll-driven reload can keep the tree cursor
// pinned (see localChangesStatusLoadedMsg).
func loadStatusCmd(dir string, preserveCursor bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), localChangesCmdTimeout)
		defer cancel()
		entries, err := statusExec(ctx, dir)
		if err != nil {
			return localChangesStatusFailedMsg{err: err}
		}
		unstaged, _ := diffNumstatExec(ctx, dir, false)
		staged, _ := diffNumstatExec(ctx, dir, true)
		// git diff --numstat has no baseline for untracked files, so their
		// "+N" column would be blank. Probe each with a no-index numstat and
		// fold it into the unstaged side (where untracked rows render). Each
		// probe is best-effort: a failure just leaves that one row's stat blank.
		//
		// One subprocess per untracked file, and the poll re-runs this every
		// ~1s — so cap the probes: an un-ignored build/deps dir (hundreds of
		// untracked files) would otherwise fork a per-second process storm.
		// Past the cap the extra rows just render without a count (still "?").
		probes := 0
		for _, e := range entries {
			if !e.Untracked {
				continue
			}
			if probes >= maxUntrackedNumstatProbes {
				break
			}
			probes++
			if fs, ferr := diffUntrackedNumstatExec(ctx, dir, e.Path); ferr == nil {
				unstaged = append(unstaged, fs)
			}
		}
		return localChangesStatusLoadedMsg{
			entries:        entries,
			unstagedStat:   unstaged,
			stagedStat:     staged,
			preserveCursor: preserveCursor,
		}
	}
}

// localChangesPollInterval is how often the Local Changes page re-runs
// loadStatusCmd while it's open, so working-tree edits from another editor
// surface without a manual reload. 1s reads as "near real-time" without
// hammering git; the poll is gated to the tree pane and pauses during an
// in-flight stash / discard (see the localChangesPollMsg handler).
const localChangesPollInterval = 1 * time.Second

// localChangesPollMsg fires on the poll tick. Like the spinner tick it is
// self-perpetuating but mode-gated — the handler stops re-arming the moment
// the page is left, so an idle cockpit schedules no wakeups.
type localChangesPollMsg struct{}

func localChangesPollCmd() tea.Cmd {
	return tea.Tick(localChangesPollInterval, func(time.Time) tea.Msg {
		return localChangesPollMsg{}
	})
}

// Stash-all / discard-all action results. Stash is reversible (git stash pop),
// so it fires straight off `s`; discard is destructive and goes through the
// confirm dialog. includeUntracked echoes the discard scope the user picked so
// the success line can name what was removed.
type localChangesStashAllDoneMsg struct{}

type localChangesStashAllFailedMsg struct {
	err error
}

type localChangesDiscardDoneMsg struct {
	includeUntracked bool
}

type localChangesDiscardFailedMsg struct {
	err error
}

// stashAllCmd runs `git stash push --include-untracked` — moves every tracked
// edit and untracked file into a new stash entry, leaving a clean tree.
func stashAllCmd(dir string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), localChangesCmdTimeout)
		defer cancel()
		if err := stashAllExec(ctx, dir); err != nil {
			return localChangesStashAllFailedMsg{err: err}
		}
		return localChangesStashAllDoneMsg{}
	}
}

// discardAllCmd resets the working tree to HEAD. Tracked edits + the index go
// via `git reset --hard HEAD`; when includeUntracked is set, a follow-up
// `git clean -fd` also removes new files (reset can't touch what HEAD never
// knew about). The clean only runs after a successful reset.
func discardAllCmd(dir string, includeUntracked bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), localChangesCmdTimeout)
		defer cancel()
		if err := resetHardExec(ctx, dir, git.ResetHard, "HEAD"); err != nil {
			return localChangesDiscardFailedMsg{err: err}
		}
		if includeUntracked {
			if err := cleanExec(ctx, dir); err != nil {
				return localChangesDiscardFailedMsg{err: err}
			}
		}
		return localChangesDiscardDoneMsg{includeUntracked: includeUntracked}
	}
}

// loadDiffCmd routes to DiffFile or DiffUntracked based on (untracked, staged).
// Conflict files use the staged=false branch (interview decision 5: "git diff
// <file> 그대로"); the caller passes untracked=false for them.
func loadDiffCmd(dir, path string, staged, untracked bool, reqID uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), localChangesCmdTimeout)
		defer cancel()
		var (
			text string
			err  error
		)
		if untracked && !staged {
			text, err = diffUntrackedExec(ctx, dir, path)
		} else {
			text, err = diffFileExec(ctx, dir, path, staged)
		}
		if err != nil {
			return localChangesDiffFailedMsg{reqID: reqID, path: path, staged: staged, err: err}
		}
		return localChangesDiffLoadedMsg{reqID: reqID, path: path, staged: staged, text: text}
	}
}

func addCmd(dir, path string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), localChangesCmdTimeout)
		defer cancel()
		if err := addExec(ctx, dir, path); err != nil {
			return localChangesAddFailedMsg{path: path, err: err}
		}
		return localChangesAddSucceededMsg{path: path}
	}
}

func restoreStagedCmd(dir, path string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), localChangesCmdTimeout)
		defer cancel()
		if err := restoreStagedExec(ctx, dir, path); err != nil {
			return localChangesRestoreFailedMsg{path: path, err: err}
		}
		return localChangesRestoreSucceededMsg{path: path}
	}
}

// stageHunkCmd stages (or, for a staged entry, unstages) a single hunk. It
// fetches the diff fresh — the viewport copy is styled (syntax + word-level)
// and `git apply` can't parse it — extracts the hunkIdx-th hunk into a minimal
// patch, and applies it to the index. staged=true means the hunk came from
// the staged side, so the patch is applied in reverse to remove it.
func stageHunkCmd(dir, path string, staged bool, hunkIdx int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), localChangesCmdTimeout)
		defer cancel()
		raw, err := diffFileRawExec(ctx, dir, path, staged)
		if err != nil {
			return localChangesApplyFailedMsg{path: path, err: err}
		}
		patch, ok := extractHunkPatch(raw, hunkIdx)
		if !ok {
			return localChangesApplyFailedMsg{path: path, err: errHunkOutOfRange}
		}
		if err := applyCachedExec(ctx, dir, patch, staged); err != nil {
			return localChangesApplyFailedMsg{path: path, err: err}
		}
		return localChangesApplySucceededMsg{path: path, staged: staged}
	}
}

// errHunkOutOfRange surfaces when the hunk index no longer maps to a hunk in
// the freshly-fetched diff (the working tree changed between the diff render
// and the stage keypress). The user reloads (`r`) and retries.
var errHunkOutOfRange = errors.New("hunk no longer present — reload (r) and retry")

// extractHunkPatch builds a minimal applyable patch from an uncolored single-
// file `git diff`: the file header (every line before the first `@@`) plus the
// hunkIdx-th `@@` hunk. Returns false when the diff has no hunk at that index.
func extractHunkPatch(diff string, hunkIdx int) (string, bool) {
	lines := strings.Split(diff, "\n")
	firstHunk := -1
	for i, ln := range lines {
		if strings.HasPrefix(ln, "@@") {
			firstHunk = i
			break
		}
	}
	if firstHunk < 0 {
		return "", false
	}
	// Hunk i spans [starts[i], starts[i+1]) — or to EOF for the last one.
	var starts []int
	for i := firstHunk; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "@@") {
			starts = append(starts, i)
		}
	}
	if hunkIdx < 0 || hunkIdx >= len(starts) {
		return "", false
	}
	end := len(lines)
	if hunkIdx+1 < len(starts) {
		end = starts[hunkIdx+1]
	}
	out := append([]string{}, lines[:firstHunk]...)
	out = append(out, lines[starts[hunkIdx]:end]...)
	patch := strings.Join(out, "\n")
	if !strings.HasSuffix(patch, "\n") {
		patch += "\n"
	}
	return patch, true
}
