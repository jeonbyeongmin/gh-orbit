package git

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
)

// ErrCherryPickConflict marks a cherry-pick stopped on conflicts. Same
// contract as ErrRebaseConflict: the mid-pick state is left in place for
// the terminal; the TUI only reports and reloads.
var ErrCherryPickConflict = errors.New("cherry-pick conflict")

// CherryPick runs `git cherry-pick <hash>` — applying one commit onto the
// current branch. Conflict stops include ErrCherryPickConflict in the
// chain; everything else (dirty tree, empty commit, bad hash) surfaces as
// a generic error with git's own message.
func CherryPick(ctx context.Context, dir, hash string) error {
	cmd := exec.CommandContext(ctx, "git", "cherry-pick", hash)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	return wrapMergeLikeErr("git cherry-pick", cmd.Run(), stdout.String(), stderr.String(), ErrCherryPickConflict)
}
