package git

import "context"

// CommitStaged runs `git commit -m <msg>` — recording the staged index as a
// new commit on the current branch. It shells out like every other wrapper, so
// the user's .gitconfig identity, commit hooks, and GPG signing all apply
// unchanged (the README contract). The only TUI caller gates on a non-empty
// index before calling, so a failure here is a real git refusal (hook reject,
// signing failure) whose stderr is surfaced verbatim. commit never stops
// mid-flight, so there is no conflict sentinel. (The bare name Commit is the
// graph-row struct in log.go.)
func CommitStaged(ctx context.Context, dir, msg string) error {
	return runGitWrite(ctx, dir, "git commit", nil, "commit", "-m", msg)
}
