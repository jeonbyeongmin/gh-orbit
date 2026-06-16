package git

import "context"

// ResetMode selects how `git reset` treats the index and working tree when
// it moves the current branch to a target commit. Soft keeps both (the
// undone commits' changes stay staged); Mixed keeps the working tree but
// clears the index (changes become unstaged edits); Hard discards both — the
// working tree is reset to the target, dropping uncommitted work.
type ResetMode int

const (
	ResetSoft ResetMode = iota
	ResetMixed
	ResetHard
)

// flag maps a ResetMode to its `git reset` CLI flag.
func (m ResetMode) flag() string {
	switch m {
	case ResetSoft:
		return "--soft"
	case ResetHard:
		return "--hard"
	default:
		return "--mixed"
	}
}

// Reset runs `git reset --<mode> <hash>` — moving the current branch to
// <hash>. Unlike Revert it rewrites the branch pointer instead of adding a
// commit, so the TUI sites that drive it pre-gate on <hash> being an ancestor
// of HEAD (discard, not advance) and refuse when the dropped range is already
// pushed (that needs a force-push). reset never stops on a conflict; failures
// surface git's own message via runGitWrite.
func Reset(ctx context.Context, dir string, mode ResetMode, hash string) error {
	return runGitWrite(ctx, dir, "git reset", nil, "reset", mode.flag(), hash)
}

// Clean runs `git clean -fd` — force-removes untracked files AND untracked
// directories from the working tree. Ignored files are deliberately left alone
// (no -x), so build artifacts and local config survive a discard. Pairs with a
// `Reset(ResetHard, "HEAD")` to take the working tree all the way back to a
// clean state; on its own it only removes the new files Reset can't touch.
// Destructive and irreversible — git keeps no record of untracked content — so
// the only TUI caller gates it behind the discard confirm's explicit "all".
func Clean(ctx context.Context, dir string) error {
	return runGitWrite(ctx, dir, "git clean", nil, "clean", "-fd")
}
