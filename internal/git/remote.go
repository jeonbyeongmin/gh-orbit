package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
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

// Fetch runs `git fetch --all` and returns nil on success. On failure the
// returned error wraps git's stderr so the TUI can surface a real reason
// ("could not resolve host", "Authentication failed", ...) instead of
// "exit status N".
//
// GIT_TERMINAL_PROMPT=0 is forced: git would otherwise grab stdin to ask for
// HTTPS credentials, which collides with the bubbletea altscreen. With the
// prompt disabled, missing credentials surface as a normal stderr error.
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
//
// LC_ALL=C and LANG=C are forced so the conflict marker stays in English —
// the wrapper parses git's output programmatically; the TUI builds its own
// user-facing messages. GIT_TERMINAL_PROMPT=0 mirrors Fetch.
func Pull(ctx context.Context, dir string, strategy PullStrategy) error {
	args := append([]string{"pull"}, strategy.args()...)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"LC_ALL=C",
		"LANG=C",
	)

	// `git pull` writes the CONFLICT marker to stdout (the merge runs there)
	// while transport / auth errors land on stderr. Capture both.
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if runErr == nil {
		return nil
	}

	conflict := strings.Contains(stderr.String(), "CONFLICT") ||
		strings.Contains(stdout.String(), "CONFLICT")
	msg := strings.TrimSpace(stderr.String())
	if msg == "" {
		msg = strings.TrimSpace(stdout.String())
	}
	if conflict {
		if msg == "" {
			return fmt.Errorf("git pull: %w", ErrPullConflict)
		}
		return fmt.Errorf("git pull: %w: %s", ErrPullConflict, msg)
	}
	return wrapGitErr("git pull", runErr, msg)
}

// Checkout runs `git checkout <name>`. Pass the *local* branch name the
// user wants to end up on — for a remote-tracking ref the caller should
// strip the "<remote>/" prefix first, so git's dwim rule kicks in and
// creates a local tracking branch (e.g. pass "feat" not "origin/feat").
// Tags pass through and yield a detached HEAD, which is the expected
// behavior when the user picks a tag.
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

// Stash runs `git stash push -m <message>`. The "-u" flag is intentionally
// not used: untracked files stay in the working tree across the checkout,
// which matches Fork/lazygit habit and the interview decision. Callers
// surface the conventional `stash@{0}` label to the user — git itself
// re-indexes stashes on push, but in a single-user TUI session the newest
// entry is always at slot 0.
func Stash(ctx context.Context, dir, message string) error {
	cmd := exec.CommandContext(ctx, "git", "stash", "push", "-m", message)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"LC_ALL=C",
		"LANG=C",
	)
	cmd.Stdout = io.Discard
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return wrapGitErr("git stash", err, stderr.String())
	}
	return nil
}

// runCheckout is the shared body of Checkout and CheckoutDetached.
// LC_ALL=C / LANG=C lock git's stderr to English so the dirty-tree pattern
// match below stays stable across user locales — the same reasoning Pull
// uses for the CONFLICT marker.
func runCheckout(ctx context.Context, dir string, args []string) error {
	cmd := exec.CommandContext(ctx, "git", append([]string{"checkout"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"LC_ALL=C",
		"LANG=C",
	)
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
