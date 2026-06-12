package git

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
)

// Push runs `git push`. A first-push branch (no upstream configured) is
// retried once with `--set-upstream origin <branch>` so the everyday
// "publish this new branch" case needs no flag dance; branch is the
// current branch name the caller already knows. Never forces.
func Push(ctx context.Context, dir, branch string) error {
	if err := runPush(ctx, dir, nil); err != nil {
		if strings.Contains(err.Error(), "has no upstream branch") && branch != "" {
			return runPush(ctx, dir, []string{"--set-upstream", "origin", branch})
		}
		return err
	}
	return nil
}

func runPush(ctx context.Context, dir string, args []string) error {
	cmd := exec.CommandContext(ctx, "git", append([]string{"push"}, args...)...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return wrapGitErr("git push", err, stderr.String())
	}
	return nil
}
