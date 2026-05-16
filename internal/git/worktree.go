package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// Worktree is one entry from `git worktree list --porcelain -z`. Path is
// always populated; Branch is the short branch name (refs/heads/ stripped)
// for an attached worktree and "" for a detached one. IsMain marks the
// main worktree — git always emits it first in the porcelain output.
type Worktree struct {
	Path           string
	HEAD           string
	Branch         string
	Detached       bool
	Locked         bool
	LockReason     string
	Prunable       bool
	PrunableReason string
	IsMain         bool
}

// ErrWorktreeDirty marks a `git worktree remove` rejection caused by
// untracked or modified files in the target worktree. Callers branch on
// errors.Is to prompt for `--force`.
var ErrWorktreeDirty = errors.New("worktree contains modifications")

// ErrWorktreeLocked marks a `git worktree remove` rejection because the
// target worktree was locked via `git worktree lock`. The TUI surfaces
// the lock reason (if any) and prompts for `--force`.
var ErrWorktreeLocked = errors.New("worktree is locked")

const worktreeRefsHeadsPrefix = "refs/heads/"

// Worktrees runs `git worktree list --porcelain -z` and parses one
// Worktree per entry. The `-z` flag (git ≥ 2.36) makes paths with spaces
// or unusual bytes safe — fields are NUL-terminated and entries are
// double-NUL terminated, so no line-based parser games.
func Worktrees(ctx context.Context, dir string) ([]Worktree, error) {
	cmd := exec.CommandContext(ctx, "git", "worktree", "list", "--porcelain", "-z")
	cmd.Dir = dir
	cmd.Env = gitEnv()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("git worktree list: start: %w", err)
	}
	body, readErr := io.ReadAll(stdout)
	waitErr := cmd.Wait()
	if waitErr != nil {
		return nil, wrapGitErr("git worktree list", waitErr, stderr.String())
	}
	if readErr != nil {
		return nil, fmt.Errorf("git worktree list: read: %w", readErr)
	}
	return parseWorktreePorcelain(string(body))
}

// parseWorktreePorcelain splits the -z stream on double-NUL into entries,
// then on single NUL into "<label>[ <value>]" fields. The first entry is
// always the main worktree (git's documented ordering).
func parseWorktreePorcelain(raw string) ([]Worktree, error) {
	if raw == "" {
		return nil, nil
	}
	entries := strings.Split(raw, "\x00\x00")
	var out []Worktree
	first := true
	for _, entry := range entries {
		entry = strings.Trim(entry, "\x00")
		if entry == "" {
			continue
		}
		wt, err := parseWorktreeEntry(entry, first)
		if err != nil {
			return nil, err
		}
		out = append(out, wt)
		first = false
	}
	return out, nil
}

func parseWorktreeEntry(entry string, isFirst bool) (Worktree, error) {
	wt := Worktree{IsMain: isFirst}
	for _, field := range strings.Split(entry, "\x00") {
		if field == "" {
			continue
		}
		label, value, _ := strings.Cut(field, " ")
		switch label {
		case "worktree":
			wt.Path = value
		case "HEAD":
			wt.HEAD = value
		case "branch":
			wt.Branch = strings.TrimPrefix(value, worktreeRefsHeadsPrefix)
		case "detached":
			wt.Detached = true
		case "locked":
			wt.Locked = true
			wt.LockReason = value
		case "prunable":
			wt.Prunable = true
			wt.PrunableReason = value
		case "bare":
			// Bare worktrees have neither HEAD nor branch; the entry stays
			// in the list so the TUI can render them as inactive, but no
			// extra field is needed beyond IsMain.
		}
	}
	if wt.Path == "" {
		return Worktree{}, fmt.Errorf("git worktree: entry missing path: %q", entry)
	}
	return wt, nil
}

// WorktreeAdd runs `git worktree add ...`. When createBranch is true the
// invocation is `git worktree add -b <branch> <path>` — a fresh branch is
// created at HEAD and checked out into <path>. When false, <branch> must
// exist and is checked out into <path> via `git worktree add <path> <branch>`.
// The v1 modal always passes createBranch=true; the false path exists so a
// future "attach existing branch" affordance doesn't need a new wrapper.
func WorktreeAdd(ctx context.Context, dir, path, branch string, createBranch bool) error {
	args := []string{"worktree", "add"}
	if createBranch {
		args = append(args, "-b", branch, path)
	} else {
		args = append(args, path, branch)
	}
	return runGitWrite(ctx, dir, "git worktree add", nil, args...)
}

// WorktreeRemove runs `git worktree remove [--force] <path>`. Dirty
// (untracked/modified files) and locked rejections are wrapped with
// sentinels so the TUI can branch on them and present the force prompt
// instead of dumping git's stderr verbatim.
func WorktreeRemove(ctx context.Context, dir, path string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, path)
	return runGitWrite(ctx, dir, "git worktree remove", []branchSentinelMatch{
		{isWorktreeDirty, ErrWorktreeDirty},
		{isWorktreeLocked, ErrWorktreeLocked},
	}, args...)
}

// isWorktreeDirty matches the two phrases git emits when `worktree remove`
// refuses because the target has modified or untracked files. Wording has
// drifted slightly across versions; both substrings are stable cores.
func isWorktreeDirty(stderr string) bool {
	return strings.Contains(stderr, "contains modified or untracked files") ||
		strings.Contains(stderr, "is dirty")
}

// isWorktreeLocked matches the phrase git emits when `worktree remove`
// refuses because the target is locked. The "is locked" branch covers
// older git versions that emit "<path> is locked".
func isWorktreeLocked(stderr string) bool {
	return strings.Contains(stderr, "locked working tree") ||
		strings.Contains(stderr, "is locked")
}
