package git

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// PullStrategy selects which `git pull` mode the wrapper invokes. The zero
// value is PullStrategyFFOnly — the conservative final fallback when neither
// user prefs nor `git config pull.{ff,rebase}` express a preference.
type PullStrategy int

const (
	PullStrategyFFOnly PullStrategy = iota
	PullStrategyMerge
	PullStrategyRebase
)

// Pref strings recognized by user prefs (`config.toml` `[pull] strategy`)
// and the matching CLI flags. Keeping the literal in one place stops the
// strings from drifting between strategy.go, prefs schema, and the docs.
const (
	PrefStrategyFFOnly = "ff-only"
	PrefStrategyMerge  = "merge"
	PrefStrategyRebase = "rebase"
)

// args returns the CLI flag(s) `git pull` should receive for this strategy.
// Merge maps to `--no-rebase` so a user's global `pull.rebase=true` cannot
// silently override an explicit merge choice.
func (s PullStrategy) args() []string {
	switch s {
	case PullStrategyRebase:
		return []string{"--rebase"}
	case PullStrategyMerge:
		return []string{"--no-rebase"}
	default:
		return []string{"--ff-only"}
	}
}

// ErrPullConflict marks a `git pull` failure where stderr/stdout contained
// the "CONFLICT" marker. Callers `errors.Is(err, ErrPullConflict)` to surface
// the resolve-in-your-terminal message instead of a raw failure string.
var ErrPullConflict = errors.New("pull conflict")

// ErrStashPopConflict marks a `git stash pop` failure where the popped
// changes collided with the working tree. The TUI surfaces this so the user
// knows conflict markers are present and the stash entry is preserved (git
// keeps the slot on conflict so nothing is lost).
var ErrStashPopConflict = errors.New("stash pop conflict")

// ErrStashApplyConflict mirrors ErrStashPopConflict for `git stash apply` —
// apply never drops, so the slot survives regardless; the sentinel only
// signals that conflict markers landed in the tree.
var ErrStashApplyConflict = errors.New("stash apply conflict")

// ErrCheckoutNeedsCleanTree marks a `git checkout` rejection caused by a
// dirty working tree. The TUI uses this signal to prompt the user for a
// stash-and-retry vs. abort decision instead of just dumping git's stderr.
var ErrCheckoutNeedsCleanTree = errors.New("checkout needs clean working tree")

// ErrFFNotPossible marks a `git merge --ff-only` rejection where the target
// hash isn't a descendant of HEAD (divergence). The TUI sites that drive
// MergeFFOnly normally pre-gate with IsAncestor so this surfaces only on a
// race or a wording-shift edge; callers branch on errors.Is to distinguish
// "not a fast-forward" from transport / lock errors.
var ErrFFNotPossible = errors.New("fast-forward not possible")

// ErrBranchNotFullyMerged marks a `git branch -d` rejection because the
// branch has commits not reachable from HEAD or its upstream. Callers can
// branch on `errors.Is` to suggest a force delete.
var ErrBranchNotFullyMerged = errors.New("branch not fully merged")

// ErrBranchNotFound marks a `git branch -d` failure whose stderr matched
// a missing-branch phrase.
var ErrBranchNotFound = errors.New("branch not found")

// gitEnv returns the locale-locked, prompt-disabled env shared by every
// wrapper that parses git's textual output. LC_ALL=C / LANG=C keep markers
// like CONFLICT / "would be overwritten" in English so the parsers in this
// file stay stable across user locales; GIT_TERMINAL_PROMPT=0 stops git
// from grabbing stdin for HTTPS credentials, which would collide with the
// bubbletea altscreen.
func gitEnv() []string {
	return append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"LC_ALL=C",
		"LANG=C",
	)
}

// wrapMergeLikeErr formats the post-run error for git commands that can
// emit a "CONFLICT" marker (pull and future merge/cherry-pick). Both
// stdout and stderr are scanned because git writes the marker to stdout
// during the merge phase but transport / strategy errors land on stderr —
// capture both, prefer stderr's first non-empty line for the human
// message. Conflict failures wrap conflictSentinel so callers can branch
// on errors.Is; non-conflict failures fall through to wrapGitErr.
func wrapMergeLikeErr(label string, runErr error, stdout, stderr string, conflictSentinel error) error {
	if runErr == nil {
		return nil
	}
	conflict := strings.Contains(stderr, "CONFLICT") || strings.Contains(stdout, "CONFLICT")
	msg := strings.TrimSpace(stderr)
	if msg == "" {
		msg = strings.TrimSpace(stdout)
	}
	if conflict {
		if msg == "" {
			return fmt.Errorf("%s: %w", label, conflictSentinel)
		}
		return fmt.Errorf("%s: %w: %s", label, conflictSentinel, msg)
	}
	return wrapGitErr(label, runErr, msg)
}

// Fetch runs `git fetch --all` and returns nil on success. On failure the
// returned error wraps git's stderr so the TUI can surface a real reason
// ("could not resolve host", "Authentication failed", ...) instead of
// "exit status N".
//
// GIT_TERMINAL_PROMPT=0 is forced: git would otherwise grab stdin to ask for
// HTTPS credentials, which collides with the bubbletea altscreen. With the
// prompt disabled, missing credentials surface as a normal stderr error.
// Locale envs are deliberately not forced here — `git fetch --all` doesn't
// emit any markers we parse, so user-locale stderr is fine to surface.
func Fetch(ctx context.Context, dir string) error {
	cmd := exec.CommandContext(ctx, "git", "fetch", "--all")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	cmd.Stdout = io.Discard

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return wrapGitErr("git fetch", err, stderr.String())
	}
	return nil
}

// Pull runs `git pull <strategy-flag>` and returns nil on success. Failures
// are wrapped with stderr/stdout; if either stream contains "CONFLICT" the
// error chain includes ErrPullConflict so the TUI can branch on a
// merge/rebase conflict vs. a transport / auth / non-fast-forward error.
func Pull(ctx context.Context, dir string, strategy PullStrategy) error {
	args := append([]string{"pull"}, strategy.args()...)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	return wrapMergeLikeErr("git pull", cmd.Run(), stdout.String(), stderr.String(), ErrPullConflict)
}

// CheckoutTarget translates a Ref into the local-name argument that
// Checkout expects. Remote-tracking refs lose their "<remote>/" segment
// so git's dwim rule creates a local tracking branch; local branches and
// tags pass through. Living in internal/git keeps the per-Kind rule next
// to Checkout itself rather than leaking it into the TUI layer.
func CheckoutTarget(ref Ref) string {
	if ref.Kind != RefKindRemote {
		return ref.ShortName
	}
	if i := strings.IndexByte(ref.ShortName, '/'); i >= 0 {
		return ref.ShortName[i+1:]
	}
	return ref.ShortName
}

// Checkout runs `git checkout <name>`. Pass CheckoutTarget(ref) for a
// refs-pane selection so dwim picks up remote-tracking refs; tags pass
// through to a detached HEAD on the tagged commit.
//
// Dirty working tree errors (git refuses with "Please commit your changes
// or stash them" / "would be overwritten") are wrapped with
// ErrCheckoutNeedsCleanTree so the TUI can branch on the conflict-style
// signal vs. a generic checkout failure.
func Checkout(ctx context.Context, dir, name string) error {
	return runCheckout(ctx, dir, []string{name})
}

// CheckoutNewBranch runs `git checkout -b <name> <startPoint>` — create a
// branch at an arbitrary commit and switch to it in one step. Same
// dirty-tree wrapping as Checkout; a name collision surfaces git's own
// "already exists" message as a generic error.
func CheckoutNewBranch(ctx context.Context, dir, name, startPoint string) error {
	return runCheckout(ctx, dir, []string{"-b", name, startPoint})
}

// CheckoutDetached runs `git checkout --detach <hash>`. Used when the user
// picks a commit on the graph pane (no ref name) — the result is a detached
// HEAD pointing at that commit. Same dirty-tree wrapping as Checkout.
func CheckoutDetached(ctx context.Context, dir, hash string) error {
	return runCheckout(ctx, dir, []string{"--detach", hash})
}

// runCheckout is the shared body of Checkout and CheckoutDetached.
// LC_ALL=C / LANG=C lock git's stderr to English so the dirty-tree pattern
// match below stays stable across user locales — the same reasoning Pull
// uses for the CONFLICT marker.
func runCheckout(ctx context.Context, dir string, args []string) error {
	cmd := exec.CommandContext(ctx, "git", append([]string{"checkout"}, args...)...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	cmd.Stdout = io.Discard
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if runErr == nil {
		return nil
	}
	msg := strings.TrimSpace(stderr.String())
	if isCheckoutDirty(msg) {
		if msg == "" {
			return fmt.Errorf("git checkout: %w", ErrCheckoutNeedsCleanTree)
		}
		return fmt.Errorf("git checkout: %w: %s", ErrCheckoutNeedsCleanTree, msg)
	}
	return wrapGitErr("git checkout", runErr, msg)
}

// StashPush runs `git stash push --include-untracked`. The dirty-tree
// confirm modal's stash-and-continue branch uses it to move the user's
// uncommitted work out of the way before retrying the interrupted chain.
// --include-untracked is deliberate: git refuses checkouts over untracked
// collisions too, and a plain stash would leave those behind to refuse
// again. The entry stays in the stash — nothing pops it automatically.
func StashPush(ctx context.Context, dir string) error {
	return runGitWrite(ctx, dir, "git stash push", nil, "stash", "push", "--include-untracked")
}

// StashPop runs `git stash pop <ref>` — apply the entry, then drop it on
// success. On conflict git keeps the slot and writes markers into the tree;
// the error chain includes ErrStashPopConflict so the TUI can say "markers
// in tree, stash kept". Other failures (no such entry, lock) wrap stderr.
func StashPop(ctx context.Context, dir, ref string) error {
	cmd := exec.CommandContext(ctx, "git", "stash", "pop", ref)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	return wrapMergeLikeErr("git stash pop", cmd.Run(), stdout.String(), stderr.String(), ErrStashPopConflict)
}

// StashApply runs `git stash apply <ref>` — apply without dropping. On
// conflict the error chain includes ErrStashApplyConflict; the entry is
// preserved either way (apply never drops).
func StashApply(ctx context.Context, dir, ref string) error {
	cmd := exec.CommandContext(ctx, "git", "stash", "apply", ref)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	return wrapMergeLikeErr("git stash apply", cmd.Run(), stdout.String(), stderr.String(), ErrStashApplyConflict)
}

// StashDrop runs `git stash drop <ref>` — destructive remove of a single
// entry. No conflict mode: it either removes the slot or errors with "is not
// a valid reference" / a lock failure.
func StashDrop(ctx context.Context, dir, ref string) error {
	return runGitWrite(ctx, dir, "git stash drop", nil, "stash", "drop", ref)
}

// StashEntry is one row from `git stash list`. Ref is git's slot selector
// (e.g. "stash@{0}"); Hash is the stash commit object; Parents are its
// parents from %P (parent[0] is the base commit the stash was taken on,
// parent[1] is the index commit, parent[2] — when present — is the
// untracked-files commit); Subject is the reflog subject (%gs).
type StashEntry struct {
	Ref     string
	Hash    string
	Parents []string
	Subject string
}

// stashListFormat: %gd = reflog selector ("stash@{N}"), %H = commit hash,
// %P = parent hashes (space-separated), %gs = reflog subject. NUL field
// separators keep the parser stable against subjects with spaces / colons.
const stashListFormat = "%gd%x00%H%x00%P%x00%gs"

// StashList runs `git stash list --format=...` and returns one StashEntry
// per slot, newest first (stash@{0} ahead of stash@{1}, ...). An empty stash
// returns (nil, nil). `--format` (vs. `git for-each-ref refs/stash`, which
// returns only the top entry) lists the full reflog history of refs/stash.
func StashList(ctx context.Context, dir string) ([]StashEntry, error) {
	cmd := exec.CommandContext(ctx, "git", "stash", "list", "--format="+stashListFormat)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("git stash list: start: %w", err)
	}
	entries, parseErr := parseStashList(stdout)
	waitErr := cmd.Wait()
	if waitErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return nil, fmt.Errorf("git stash list: %w", waitErr)
		}
		return nil, fmt.Errorf("git stash list: %w: %s", waitErr, msg)
	}
	if parseErr != nil {
		return nil, fmt.Errorf("git stash list: parse: %w", parseErr)
	}
	return entries, nil
}

func parseStashList(r io.Reader) ([]StashEntry, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	var out []StashEntry
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\x00")
		if len(fields) != 4 {
			return nil, fmt.Errorf("unexpected field count %d in %q", len(fields), line)
		}
		var parents []string
		if fields[2] != "" {
			parents = strings.Split(fields[2], " ")
		}
		out = append(out, StashEntry{
			Ref:     fields[0],
			Hash:    fields[1],
			Parents: parents,
			Subject: fields[3],
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// MergeFFOnly runs `git merge --ff-only <hash>`. On success the current
// branch tip advances to <hash> with no merge commit and no checkout.
// Callers may pre-gate with CountAhead (advance > 0 implies the FF will
// land); the --ff-only flag is the runtime safety net for divergence.
//
// Failure wrapping mirrors Checkout: dirty-tree wording (which `merge`
// emits with the same "would be overwritten" / "Please commit your
// changes" phrases as `checkout`) routes to ErrCheckoutNeedsCleanTree so
// the TUI can reuse the existing stash-or-abort confirm modal. The FF-
// rejection phrase ("Not possible to fast-forward, aborting") routes to
// ErrFFNotPossible so callers can branch on a divergence vs. a lock /
// transport failure. Anything else falls through to wrapGitErr.
func MergeFFOnly(ctx context.Context, dir, hash string) error {
	cmd := exec.CommandContext(ctx, "git", "merge", "--ff-only", hash)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if runErr == nil {
		return nil
	}
	// `merge` writes the FF-rejection phrase to stderr; the dirty-tree
	// phrases also land on stderr. Scan stderr first, fall back to stdout
	// for the human message (some git versions write hints to stdout).
	msg := strings.TrimSpace(stderr.String())
	if msg == "" {
		msg = strings.TrimSpace(stdout.String())
	}
	if isCheckoutDirty(stderr.String()) || isCheckoutDirty(stdout.String()) {
		if msg == "" {
			return fmt.Errorf("git merge --ff-only: %w", ErrCheckoutNeedsCleanTree)
		}
		return fmt.Errorf("git merge --ff-only: %w: %s", ErrCheckoutNeedsCleanTree, msg)
	}
	if isFFNotPossible(stderr.String()) || isFFNotPossible(stdout.String()) {
		if msg == "" {
			return fmt.Errorf("git merge --ff-only: %w", ErrFFNotPossible)
		}
		return fmt.Errorf("git merge --ff-only: %w: %s", ErrFFNotPossible, msg)
	}
	return wrapGitErr("git merge --ff-only", runErr, msg)
}

// CountAhead returns how many commits are reachable from `descendant` but
// not from `ancestor` (i.e., the `+N` advance count) using `git rev-list
// --count <ancestor>..<descendant>`. Returns 0 when descendant == ancestor
// or when descendant is behind ancestor (rev-list emits an empty range).
// The TUI uses this to render `fast-forward: <branch> +N` in the status
// bar after a successful MergeFFOnly.
func CountAhead(ctx context.Context, dir, ancestor, descendant string) (int, error) {
	cmd := exec.CommandContext(ctx, "git", "rev-list", "--count", ancestor+".."+descendant)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return 0, wrapGitErr("git rev-list --count", err, stderr.String())
	}
	n, err := strconv.Atoi(strings.TrimSpace(stdout.String()))
	if err != nil {
		return 0, fmt.Errorf("git rev-list --count: parse %q: %w", stdout.String(), err)
	}
	return n, nil
}

// isFFNotPossible matches the phrase git emits when `merge --ff-only`
// rejects because the target isn't a descendant of HEAD. Wording has
// drifted slightly across git versions ("Not possible to fast-forward,
// aborting." vs. "fatal: Not possible to fast-forward") so we match a
// case-insensitive substring of the stable core.
func isFFNotPossible(stderr string) bool {
	return strings.Contains(stderr, "Not possible to fast-forward") ||
		strings.Contains(stderr, "not possible to fast-forward")
}

// isCheckoutDirty matches the three phrases git emits when a checkout is
// blocked by uncommitted changes. We look at substrings rather than parsing
// the full message — git's wording differs between "would be overwritten by
// checkout" (tracked changes) and "untracked working tree files would be
// overwritten" (untracked collisions), and both should route to the same
// stash-or-abort prompt.
func isCheckoutDirty(stderr string) bool {
	return strings.Contains(stderr, "Please commit your changes or stash them") ||
		strings.Contains(stderr, "would be overwritten") ||
		strings.Contains(stderr, "Your local changes")
}

// wrapGitErr formats the "<label>: <runErr>[: <stderr first line>]" pattern
// shared by Fetch / Pull / readGitConfig. stderr is expected to be already
// trimmed; passing the raw buffer string is fine — wrapGitErr trims again.
func wrapGitErr(label string, runErr error, stderr string) error {
	msg := strings.TrimSpace(stderr)
	if msg == "" {
		return fmt.Errorf("%s: %w", label, runErr)
	}
	return fmt.Errorf("%s: %w: %s", label, runErr, msg)
}

// branchSentinelMatch pairs a stderr-substring predicate with the sentinel
// error to wrap with when it matches. Order matters — the first matcher
// whose predicate returns true wins. Callers list sentinels in the order
// they should be classified; an empty list means "no sentinels, fall
// through to wrapGitErr".
type branchSentinelMatch struct {
	matches  func(string) bool
	sentinel error
}

// runGitWrite is the shared body of the local-only branch wrappers. It
// runs the command with locale-locked env, captures stderr, and on failure
// classifies the error against `sentinels` before falling back to
// wrapGitErr. Returning the same error layout (`<label>: <sentinel>: <msg>`)
// for every wrapper keeps the TUI's stderr surface uniform.
func runGitWrite(ctx context.Context, dir, label string, sentinels []branchSentinelMatch, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	cmd.Stdout = io.Discard
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if runErr == nil {
		return nil
	}
	msg := strings.TrimSpace(stderr.String())
	for _, m := range sentinels {
		if m.matches(msg) {
			if msg == "" {
				return fmt.Errorf("%s: %w", label, m.sentinel)
			}
			return fmt.Errorf("%s: %w: %s", label, m.sentinel, msg)
		}
	}
	return wrapGitErr(label, runErr, msg)
}

// BranchDelete runs `git branch -d <name>` (force=false) or `git branch -D
// <name>` (force=true). The unmerged-rejection phrase ("not fully merged")
// is wrapped with ErrBranchNotFullyMerged; missing-branch routes to
// ErrBranchNotFound.
func BranchDelete(ctx context.Context, dir, name string, force bool) error {
	flag := "-d"
	if force {
		flag = "-D"
	}
	return runGitWrite(ctx, dir, "git branch -d", []branchSentinelMatch{
		{isBranchNotFullyMerged, ErrBranchNotFullyMerged},
		{isBranchNotFound, ErrBranchNotFound},
	}, "branch", flag, name)
}

// isBranchNotFullyMerged matches git's safe-delete rejection. The phrase has
// been stable since the early 2010s ("error: The branch '<x>' is not fully
// merged.") so substring matching is robust.
func isBranchNotFullyMerged(stderr string) bool {
	return strings.Contains(stderr, "not fully merged")
}

// isBranchNotFound matches git's missing-branch rejection for `branch -d`
// (`error: branch '<x>' not found.`). Stable phrasing since the early
// 2010s — substring match is robust.
func isBranchNotFound(stderr string) bool {
	return strings.Contains(stderr, "not found")
}
