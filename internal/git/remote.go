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
