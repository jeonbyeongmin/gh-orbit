package git

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// Stat returns `git show --stat` output for the given commit hash. The header
// (commit / Author / Date) is suppressed via --format= so the caller gets only
// the file list and the "X files changed, Y insertions(+), Z deletions(-)"
// summary. ANSI escapes are preserved (-c color.ui=always) so the TUI can
// render git's own coloring without re-implementing it.
func Stat(ctx context.Context, dir, hash string) (string, error) {
	return runShow(ctx, dir, "--stat", "--format=", hash)
}

// Patch returns the unified diff for the given commit hash. Same color/format
// conventions as Stat — header suppressed, ANSI preserved.
func Patch(ctx context.Context, dir, hash string) (string, error) {
	return runShow(ctx, dir, "--format=", "-p", hash)
}

func runShow(ctx context.Context, dir string, extra ...string) (string, error) {
	args := []string{"-c", "color.ui=always", "show"}
	args = append(args, extra...)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("git show: start: %w", err)
	}
	body, readErr := io.ReadAll(stdout)
	waitErr := cmd.Wait()
	if waitErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return "", fmt.Errorf("git show: %w", waitErr)
		}
		return "", fmt.Errorf("git show: %w: %s", waitErr, msg)
	}
	if readErr != nil {
		return "", fmt.Errorf("git show: read: %w", readErr)
	}
	return string(body), nil
}
