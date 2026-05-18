package git

import (
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

// ErrBranchAlreadyExists marks a `git branch <name>` / `git branch -m`
// failure whose stderr matched the "already exists" wording.
var ErrBranchAlreadyExists = errors.New("branch already exists")

// ErrBranchNotFullyMerged marks a `git branch -d` rejection because the
// branch has commits not reachable from HEAD or its upstream. Callers can
// branch on `errors.Is` to suggest a force delete.
var ErrBranchNotFullyMerged = errors.New("branch not fully merged")

// ErrBranchNotFound marks a `git branch -d` / `git branch -m` failure
// whose stderr matched a missing-branch phrase.
var ErrBranchNotFound = errors.New("branch not found")

// ErrInvalidRefName marks a `git check-ref-format` failure (or the same-
// style rejection from create / rename).
var ErrInvalidRefName = errors.New("invalid ref name")

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

// BranchCreate runs `git branch <name> [<base>]`. An empty base means HEAD —
// git itself defaults to HEAD when the second arg is omitted. The TUI passes
// either a commit hash (graph cursor) or a ref tip (refs cursor); resolving
// the right base lives in the caller because it depends on focus state.
func BranchCreate(ctx context.Context, dir, name, base string) error {
	args := []string{"branch", name}
	if base != "" {
		args = append(args, base)
	}
	return runGitWrite(ctx, dir, "git branch", []branchSentinelMatch{
		{isBranchAlreadyExists, ErrBranchAlreadyExists},
	}, args...)
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

// BranchRename runs `git branch -m <old> <new>`. HEAD-on-old is allowed by
// git itself (the rename moves HEAD too), so the wrapper does not pre-gate.
func BranchRename(ctx context.Context, dir, oldName, newName string) error {
	return runGitWrite(ctx, dir, "git branch -m", []branchSentinelMatch{
		{isBranchAlreadyExists, ErrBranchAlreadyExists},
		{isBranchNotFound, ErrBranchNotFound},
	}, "branch", "-m", oldName, newName)
}

// RemoteBranchDelete runs `git push <remote> --delete <branch>`. There is
// no force flag — the delete-ref operation is unconditional from the local
// side, and any rejection (protected branch, auth, missing ref) is
// meaningful as-is. stderr is surfaced verbatim because the failure space
// is too varied to classify (network / auth / branch-protection / not-found).
func RemoteBranchDelete(ctx context.Context, dir, remote, branch string) error {
	return runGitWrite(ctx, dir, "git push --delete", nil,
		"push", remote, "--delete", branch)
}

// CheckRefFormat runs `git check-ref-format --branch <name>`. A non-zero
// exit always means an invalid name (the command has no other failure
// modes), so we route every error to ErrInvalidRefName.
func CheckRefFormat(ctx context.Context, dir, name string) error {
	return runGitWrite(ctx, dir, "git check-ref-format",
		[]branchSentinelMatch{{func(string) bool { return true }, ErrInvalidRefName}},
		"check-ref-format", "--branch", name)
}

// isBranchAlreadyExists matches git's wording when create/rename collides
// with an existing local branch. Two flavors land here: "A branch named '<x>'
// already exists." (create / rename target) and "fatal: A branch named '<x>'
// already exists." across versions.
func isBranchAlreadyExists(stderr string) bool {
	return strings.Contains(stderr, "already exists")
}

// isBranchNotFullyMerged matches git's safe-delete rejection. The phrase has
// been stable since the early 2010s ("error: The branch '<x>' is not fully
// merged.") so substring matching is robust.
func isBranchNotFullyMerged(stderr string) bool {
	return strings.Contains(stderr, "not fully merged")
}

// isBranchNotFound matches git's missing-branch rejection for `branch -d` /
// `branch -m`. Wording differs by subcommand AND across versions:
//   - `branch -d <missing>` → "error: branch '<x>' not found."
//   - `branch -m <missing> <new>` → "fatal: No branch named '<x>'." (older)
//     or "fatal: no branch named '<x>'" (newer; lowercase 'no').
//
// strings.Contains is case-sensitive, so we match both casings of the
// rename phrase. No other branch-failure mode uses these substrings.
func isBranchNotFound(stderr string) bool {
	return strings.Contains(stderr, "not found") ||
		strings.Contains(stderr, "No branch named") ||
		strings.Contains(stderr, "no branch named")
}
