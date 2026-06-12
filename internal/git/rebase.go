package git

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
)

// ErrRebaseConflict marks a rebase stopped on conflicts. The wrapped error
// still carries git's stdout/stderr; callers branch on this sentinel to
// distinguish "resolve in your terminal" from a generic failure. The
// mid-rebase state is intentionally left in place — mirroring Pull's
// conflict contract, the TUI never auto-aborts what the user may want to
// resolve by hand.
var ErrRebaseConflict = errors.New("rebase conflict")

// Rebase runs `git rebase <onto>` — replaying the current branch on top of
// the given commit/ref. Dirty-tree refusals and other failures surface as
// generic errors with git's own message; conflict stops include
// ErrRebaseConflict in the chain.
func Rebase(ctx context.Context, dir, onto string) error {
	cmd := exec.CommandContext(ctx, "git", "rebase", onto)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	return wrapMergeLikeErr("git rebase", cmd.Run(), stdout.String(), stderr.String(), ErrRebaseConflict)
}
