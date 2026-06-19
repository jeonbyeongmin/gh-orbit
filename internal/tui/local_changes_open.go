// Local Changes "open in default app" action. `enter` on a tree row launches
// the cursor's file in whatever app the OS has registered for that file type —
// `open` on macOS, `xdg-open` on Linux, `cmd /c start` on Windows. The launcher
// returns immediately; the app it spawns outlives the cockpit. Mirrors
// prreview.go's exec-seam pattern so tests can stub the subprocess.
package tui

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// openCmdTimeout bounds the launcher subprocess. The launcher forks the
// registered app and exits, so this only guards a hung launcher — not the
// app's own lifetime.
const openCmdTimeout = 10 * time.Second

type localChangesOpenSucceededMsg struct{ path string }

type localChangesOpenFailedMsg struct {
	path string
	err  error
}

// openFileExec is the package-level seam over the OS "open with default app"
// launcher. path is relative to dir (the workdir); it's resolved to an absolute
// path so the launcher's CWD doesn't matter. A missing file — a worktree
// deletion still listed in the tree — is reported before the launcher runs so
// the status line says "gone" instead of surfacing the launcher's raw error.
var openFileExec = func(ctx context.Context, dir, path string) error {
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(dir, path)
	}
	if _, err := os.Stat(abs); err != nil {
		return fmt.Errorf("%s is gone", path)
	}
	return osLaunch(ctx, abs)
}

// osLaunch forks the OS "open with the registered handler" launcher on a single
// target — an absolute file path (openFileExec) or a URL (openURLExec) — and
// returns once the handler is spawned. The shared half of the two seams; only
// the file path's existence pre-check lives in openFileExec.
func osLaunch(ctx context.Context, target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", target)
	case "windows":
		// The empty title arg keeps `start` from swallowing a quoted target.
		cmd = exec.CommandContext(ctx, "cmd", "/c", "start", "", target)
	default:
		cmd = exec.CommandContext(ctx, "xdg-open", target)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("%s", firstLine(msg))
		}
		return err
	}
	return nil
}

func openFileCmd(dir, path string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), openCmdTimeout)
		defer cancel()
		if err := openFileExec(ctx, dir, path); err != nil {
			return localChangesOpenFailedMsg{path: path, err: err}
		}
		return localChangesOpenSucceededMsg{path: path}
	}
}
