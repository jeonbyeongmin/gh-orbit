package git

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
)

// ErrRevertConflict marks a revert stopped on conflicts. Same contract as
// ErrCherryPickConflict: the mid-revert state is left in place for the
// terminal; the TUI only reports and reloads.
var ErrRevertConflict = errors.New("revert conflict")

// Revert runs `git revert --no-edit <hash>` — recording a new commit that
// undoes <hash> while preserving history. --no-edit takes git's default
// "Revert <subject>" message so the flow stays non-interactive (an editor
// launch would collide with the bubbletea altscreen). Conflict stops wrap
// ErrRevertConflict; everything else (dirty tree, a merge commit needing
// -m, a bad hash) surfaces as a generic error with git's own message.
func Revert(ctx context.Context, dir, hash string) error {
	cmd := exec.CommandContext(ctx, "git", "revert", "--no-edit", hash)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	return wrapMergeLikeErr("git revert", cmd.Run(), stdout.String(), stderr.String(), ErrRevertConflict)
}
