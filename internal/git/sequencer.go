package git

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SequencerKind identifies an in-progress, multi-step git operation that can
// stop mid-flight on conflicts and be resumed (`--continue`) or unwound
// (`--abort`). The cockpit detects these so a conflict no longer forces the
// reviewer out to a shell — the "stay in the terminal" contract.
type SequencerKind int

const (
	SequencerNone SequencerKind = iota
	SequencerRebase
	SequencerMerge
	SequencerCherryPick
	SequencerRevert
)

// String returns the git subcommand for the kind ("cherry-pick", "rebase", …)
// — the single source of truth for both building `git <sub> --continue|--abort`
// and labelling the in-progress banner. SequencerNone is "".
func (k SequencerKind) String() string {
	switch k {
	case SequencerRebase:
		return "rebase"
	case SequencerMerge:
		return "merge"
	case SequencerCherryPick:
		return "cherry-pick"
	case SequencerRevert:
		return "revert"
	}
	return ""
}

// ErrSequencerConflict marks a `--continue` that advanced into a fresh
// conflict — a multi-commit cherry-pick / rebase stops again on the next
// commit. Same leave-in-place contract as ErrCherryPickConflict: the TUI
// reports and reloads, the in-progress banner stays up.
var ErrSequencerConflict = errors.New("sequencer conflict")

// DetectSequencer reports which in-progress operation, if any, the repo is
// parked in. There is no porcelain command that answers this, so it checks for
// the marker files git itself writes, under the per-worktree git dir resolved
// via rev-parse (so linked worktrees and the gitfile case land in the right
// place). Precedence follows git's own status logic — rebase first, since its
// state dir is the most specific operation in progress (a stopped rebase pick
// is a rebase, not the cherry-pick/merge it uses internally).
func DetectSequencer(ctx context.Context, dir string) (SequencerKind, error) {
	gitDir, err := absoluteGitDir(ctx, dir)
	if err != nil {
		return SequencerNone, err
	}
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(gitDir, name))
		return err == nil
	}
	switch {
	case exists("rebase-merge") || exists("rebase-apply"):
		return SequencerRebase, nil
	case exists("MERGE_HEAD"):
		return SequencerMerge, nil
	case exists("CHERRY_PICK_HEAD"):
		return SequencerCherryPick, nil
	case exists("REVERT_HEAD"):
		return SequencerRevert, nil
	}
	return SequencerNone, nil
}

// absoluteGitDir returns the absolute git directory for dir's worktree.
// --absolute-git-dir resolves the gitfile indirection of a linked worktree, so
// the marker-file paths above land in that worktree's own git dir rather than
// the shared one.
func absoluteGitDir(ctx context.Context, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--absolute-git-dir")
	cmd.Dir = dir
	cmd.Env = gitEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", wrapGitErr("git rev-parse --absolute-git-dir", err, stderr.String())
	}
	return strings.TrimSpace(stdout.String()), nil
}

// SequencerContinue runs `git <kind> --continue`, resuming the operation after
// its conflicts are resolved and staged. GIT_EDITOR=true suppresses the
// commit-message editor git would otherwise launch — an interactive editor
// collides with the bubbletea altscreen (the same reason Revert forces
// --no-edit); the prepared message is committed as-is. A conflict on the next
// step wraps ErrSequencerConflict. The user's hooks / signing / .gitconfig
// identity all still apply (the README contract).
func SequencerContinue(ctx context.Context, dir string, kind SequencerKind) error {
	sub := kind.String()
	if sub == "" {
		return errors.New("git --continue: no sequencer in progress")
	}
	cmd := exec.CommandContext(ctx, "git", sub, "--continue")
	cmd.Dir = dir
	cmd.Env = append(gitEnv(), "GIT_EDITOR=true")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	return wrapMergeLikeErr("git "+sub+" --continue", cmd.Run(), stdout.String(), stderr.String(), ErrSequencerConflict)
}

// SequencerAbort runs `git <kind> --abort`, unwinding the in-progress operation
// back to its pre-start state. Abort never stops mid-flight, so it has no
// conflict sentinel; it is destructive (the partial result is discarded), so
// the TUI gates it behind a confirm.
func SequencerAbort(ctx context.Context, dir string, kind SequencerKind) error {
	sub := kind.String()
	if sub == "" {
		return errors.New("git --abort: no sequencer in progress")
	}
	return runGitWrite(ctx, dir, "git "+sub+" --abort", nil, sub, "--abort")
}
