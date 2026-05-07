package git

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
)

// pullStrategyConfigTimeout caps the `git config --get` lookups used while
// resolving the pull strategy. Independent of fetch / pull's own timeouts.
const pullStrategyConfigTimeout = 5 * time.Second

// ResolvePullStrategy decides which PullStrategy to apply, in priority order:
//
//  1. user prefs ("ff-only" / "merge" / "rebase")
//  2. `git config --get pull.rebase` == "true"  → rebase
//  3. final fallback: ff-only
//
// `pull.ff` is intentionally not consulted: the only setting (`only`) maps to
// the same final fallback, so reading it would be a subprocess for no
// behavior change. Restore the read here when the fallback shifts away from
// ff-only.
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
	return PullStrategyFFOnly, nil
}

func pullStrategyFromPrefs(prefs string) (PullStrategy, bool) {
	switch strings.TrimSpace(prefs) {
	case PrefStrategyFFOnly:
		return PullStrategyFFOnly, true
	case PrefStrategyMerge:
		return PullStrategyMerge, true
	case PrefStrategyRebase:
		return PullStrategyRebase, true
	default:
		return PullStrategyFFOnly, false
	}
}

// readGitConfig returns the value of `git config --get <key>` in dir. Missing
// key (`git config` exits 1 with empty stdout) → empty string + nil error.
// Any other failure → wrapped error with stderr.
func readGitConfig(ctx context.Context, dir, key string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "config", "--get", key)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return strings.TrimSpace(stdout.String()), nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return "", nil
	}
	return "", wrapGitErr("git config --get "+key, err, stderr.String())
}
