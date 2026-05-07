package git

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// gitRun shells out to `git` in dir with deterministic author/committer env so
// integration tests don't depend on the developer's `~/.gitconfig`.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s in %s: %v: %s", strings.Join(args, " "), dir, err, out)
	}
}

// gitOutput is gitRun for read-only commands — returns trimmed stdout for
// assertion. Inherits the same deterministic identity env.
func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s in %s: %v", strings.Join(args, " "), dir, err)
	}
	return strings.TrimSpace(string(out))
}

// readHEAD returns the symbolic ref HEAD points at (e.g. "refs/heads/main")
// or fails the test. Detached HEAD makes symbolic-ref exit non-zero — call
// gitOutput("rev-parse", "HEAD") for the raw hash in that case.
func readHEAD(t *testing.T, dir string) string {
	t.Helper()
	return gitOutput(t, dir, "symbolic-ref", "HEAD")
}
