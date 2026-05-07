package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// pullStrategyConfigTimeout caps the `git config --get` lookups used while
// resolving the pull strategy. The fetch / pull commands have their own
// generous budgets; this is just a guard against a hung config read.
const pullStrategyConfigTimeout = 5 * time.Second

// ResolvePullStrategy decides which PullStrategy to apply, in priority order:
//
//  1. user prefs ("ff-only" / "merge" / "rebase")
//  2. `git config --get pull.rebase` == "true"  → rebase
//  3. `git config --get pull.ff`     == "only"  → ff-only
//  4. final fallback: ff-only
//
// `git config --get` returns exit code 1 with empty stdout when the key is
// unset; we treat that as "no preference" and fall through. Any other
// non-zero exit (≥ 2) bubbles up as an error so callers can surface a real
// problem instead of silently picking ff-only.
func ResolvePullStrategy(ctx context.Context, dir string, prefs string) (PullStrategy, error) {
	if s, ok := pullStrategyFromPrefs(prefs); ok {
		return s, nil
	}

	cfgCtx, cancel := context.WithTimeout(ctx, pullStrategyConfigTimeout)
	defer cancel()

	rebase, err := readGitConfig(cfgCtx, dir, "pull.rebase")
	if err != nil {
		return PullStrategyFFOnly, err
	}
	if rebase == "true" {
		return PullStrategyRebase, nil
	}

	ff, err := readGitConfig(cfgCtx, dir, "pull.ff")
	if err != nil {
		return PullStrategyFFOnly, err
	}
	if ff == "only" {
		return PullStrategyFFOnly, nil
	}

	return PullStrategyFFOnly, nil
}

func pullStrategyFromPrefs(prefs string) (PullStrategy, bool) {
	switch strings.TrimSpace(prefs) {
	case "ff-only":
		return PullStrategyFFOnly, true
	case "merge":
		return PullStrategyMerge, true
	case "rebase":
		return PullStrategyRebase, true
	default:
		return PullStrategyFFOnly, false
	}
}

// readGitConfig returns the value of `git config --get <key>` in dir. Missing
// key → empty string + nil error. Any other failure → wrapped error with
// stderr.
func readGitConfig(ctx context.Context, dir, key string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "config", "--get", key)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return strings.TrimSpace(stdout.String()), nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		// missing key — git config conventional "no such entry"
		return "", nil
	}
	msg := strings.TrimSpace(stderr.String())
	if msg == "" {
		return "", fmt.Errorf("git config --get %s: %w", key, err)
	}
	return "", fmt.Errorf("git config --get %s: %w: %s", key, err, msg)
}
