package git

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Fetch runs `git fetch --all` and returns nil on success. On failure the
// returned error wraps git's stderr so the TUI can surface a real reason
// ("could not resolve host", "Authentication failed", ...) instead of
// "exit status N".
//
// GIT_TERMINAL_PROMPT=0 is forced: git would otherwise grab stdin to ask for
// HTTPS credentials, which collides with the bubbletea altscreen. With the
// prompt disabled, missing credentials surface as a normal stderr error.
func Fetch(ctx context.Context, dir string) error {
	cmd := exec.CommandContext(ctx, "git", "fetch", "--all")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	cmd.Stdout = io.Discard

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return fmt.Errorf("git fetch: %w", err)
		}
		return fmt.Errorf("git fetch: %w: %s", err, msg)
	}
	return nil
}
