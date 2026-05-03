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

// FetchOptions selects what Fetch will pull.
type FetchOptions struct {
	Dir string
	// All maps to `git fetch --all` (every configured remote). When false,
	// fetch behaves like the user's plain `git fetch` (default remote of the
	// current branch).
	All bool
}

// Fetch runs `git fetch` and returns nil on success. On failure the returned
// error wraps git's stderr so the TUI can surface a real reason ("could not
// resolve host", "Authentication failed", ...) instead of "exit status N".
//
// GIT_TERMINAL_PROMPT=0 is forced: git would otherwise grab stdin to ask for
// HTTPS credentials, which collides with the bubbletea altscreen. With the
// prompt disabled, missing credentials surface as a normal stderr error.
func Fetch(ctx context.Context, opts FetchOptions) error {
	args := []string{"fetch"}
	if opts.All {
		args = append(args, "--all")
	}

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = opts.Dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")

	// stdout is usually empty for fetch (progress goes to stderr). Drain it
	// anyway so a misbehaving hook printing to stdout can't deadlock us.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("git fetch: start: %w", err)
	}
	_, _ = io.Copy(io.Discard, stdout)
	if waitErr := cmd.Wait(); waitErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return fmt.Errorf("git fetch: %w", waitErr)
		}
		return fmt.Errorf("git fetch: %w: %s", waitErr, msg)
	}
	return nil
}
