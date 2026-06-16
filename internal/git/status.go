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

// StatusEntry is a single record parsed from
// `git status --porcelain=v2 -z --untracked-files=all`. One entry = one file
// **per side** — a path whose worktree differs from both HEAD and the index
// produces a single ordinary record (IndexState != '.' && WorktreeState != '.')
// that the UI splits into Unstaged / Staged sections itself.
//
// IndexState / WorktreeState are the porcelain v2 XY pair (single-byte codes,
// see `git status --short`). For untracked entries both are '?', for ignored
// entries both are '!'. For unmerged (conflict) entries the XY pair carries
// the two stage descriptors (e.g. 'U'/'U' for both-modified) and Conflict is
// set so callers don't have to re-classify by hand.
//
// OrigPath is non-empty only for renamed/copied records — it names the source
// path the rename/copy started from. The receiver `Renamed()` is the test the
// UI uses ("did this entry move?") rather than peeking at IndexState directly.
type StatusEntry struct {
	Path          string
	OrigPath      string
	IndexState    byte
	WorktreeState byte
	Untracked     bool
	Conflict      bool
}

// Renamed reports whether the entry came from a rename or copy record (i.e.
// porcelain v2 prefix "2"). Defined as "has an origin path" so future record
// kinds that carry an origin path inherit the same answer.
func (e StatusEntry) Renamed() bool { return e.OrigPath != "" }

// Status reports the working tree state as a flat slice of entries. The
// underlying call is `git status --porcelain=v2 -z --untracked-files=all`,
// which is NUL-terminated (so paths with embedded newlines / tabs are safe)
// and emits a stable token grammar:
//
//	1 <XY> <sub> <mH> <mI> <mW> <hH> <hI> <path>           — ordinary change
//	2 <XY> <sub> <mH> <mI> <mW> <hH> <hI> <X><score> <p>   — rename/copy, followed by NUL + orig path
//	u <XY> <sub> <m1> <m2> <m3> <mW> <h1> <h2> <h3> <path> — unmerged (conflict)
//	? <path>                                               — untracked
//	! <path>                                               — ignored
//	# ...                                                  — branch headers (skipped)
//
// Unknown record kinds are silently skipped so adding `--ignored` or a future
// porcelain extension doesn't break the parser.
func Status(ctx context.Context, dir string) ([]StatusEntry, error) {
	raw, err := runStatus(ctx, dir)
	if err != nil {
		return nil, err
	}
	return parsePorcelainV2(raw)
}

func runStatus(ctx context.Context, dir string) (string, error) {
	// --no-optional-locks keeps this read-only probe from taking the index
	// lock to refresh the stat cache, which would rewrite .git/index. The
	// worktree dirty fan-out runs status on a loop and the external-change
	// watcher (internal/tui/worktreewatch.go) watches .git/index — without
	// this flag a single git op (merge/checkout) triggers status → index
	// write → fsnotify event → reload → status …, a self-sustaining flicker
	// loop. The cockpit only ever reads working-tree state here; it never
	// needs the opportunistic index refresh.
	cmd := exec.CommandContext(ctx, "git", "--no-optional-locks", "status", "--porcelain=v2", "-z", "--untracked-files=all")
	cmd.Dir = dir
	cmd.Env = gitEnv()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("git status: start: %w", err)
	}
	body, readErr := io.ReadAll(stdout)
	waitErr := cmd.Wait()
	if waitErr != nil {
		return "", wrapGitErr("git status", waitErr, stderr.String())
	}
	if readErr != nil {
		return "", fmt.Errorf("git status: read: %w", readErr)
	}
	return string(body), nil
}

// parsePorcelainV2 walks the NUL-separated record stream. Rename/copy records
// consume two tokens (the record itself, then the orig path).
func parsePorcelainV2(raw string) ([]StatusEntry, error) {
	if raw == "" {
		return nil, nil
	}
	tokens := strings.Split(raw, "\x00")
	if n := len(tokens); n > 0 && tokens[n-1] == "" {
		tokens = tokens[:n-1]
	}
	var out []StatusEntry
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if tok == "" {
			continue
		}
		switch tok[0] {
		case '1':
			e, err := parsePorcelainOrdinary(tok)
			if err != nil {
				return nil, err
			}
			out = append(out, e)
		case '2':
			if i+1 >= len(tokens) {
				return nil, fmt.Errorf("git status: rename record missing orig path: %q", tok)
			}
			e, err := parsePorcelainRename(tok, tokens[i+1])
			if err != nil {
				return nil, err
			}
			out = append(out, e)
			i++
		case 'u':
			e, err := parsePorcelainUnmerged(tok)
			if err != nil {
				return nil, err
			}
			out = append(out, e)
		case '?':
			e, err := parsePorcelainUntracked(tok)
			if err != nil {
				return nil, err
			}
			out = append(out, e)
		case '!', '#':
			continue
		default:
			continue
		}
	}
	return out, nil
}

// "1 XY sub mH mI mW hH hI path" — 9 space-separated fields, path can contain
// further spaces but is the rest of the token after the 8th split.
func parsePorcelainOrdinary(tok string) (StatusEntry, error) {
	parts := strings.SplitN(tok, " ", 9)
	if len(parts) != 9 {
		return StatusEntry{}, fmt.Errorf("git status: ordinary record short: %q", tok)
	}
	xy := parts[1]
	if len(xy) != 2 {
		return StatusEntry{}, fmt.Errorf("git status: ordinary XY %q", xy)
	}
	return StatusEntry{
		Path:          parts[8],
		IndexState:    xy[0],
		WorktreeState: xy[1],
	}, nil
}

// "2 XY sub mH mI mW hH hI <X><score> path" — 10 fields. The NUL-separated
// `orig` follows as the next token.
func parsePorcelainRename(tok, orig string) (StatusEntry, error) {
	parts := strings.SplitN(tok, " ", 10)
	if len(parts) != 10 {
		return StatusEntry{}, fmt.Errorf("git status: rename record short: %q", tok)
	}
	xy := parts[1]
	if len(xy) != 2 {
		return StatusEntry{}, fmt.Errorf("git status: rename XY %q", xy)
	}
	if orig == "" {
		return StatusEntry{}, fmt.Errorf("git status: rename orig empty: %q", tok)
	}
	return StatusEntry{
		Path:          parts[9],
		OrigPath:      orig,
		IndexState:    xy[0],
		WorktreeState: xy[1],
	}, nil
}

// "u XY sub m1 m2 m3 mW h1 h2 h3 path" — 11 fields.
func parsePorcelainUnmerged(tok string) (StatusEntry, error) {
	parts := strings.SplitN(tok, " ", 11)
	if len(parts) != 11 {
		return StatusEntry{}, fmt.Errorf("git status: unmerged record short: %q", tok)
	}
	xy := parts[1]
	if len(xy) != 2 {
		return StatusEntry{}, fmt.Errorf("git status: unmerged XY %q", xy)
	}
	return StatusEntry{
		Path:          parts[10],
		IndexState:    xy[0],
		WorktreeState: xy[1],
		Conflict:      true,
	}, nil
}

// "? path" — single space after the marker, rest is the path.
func parsePorcelainUntracked(tok string) (StatusEntry, error) {
	if len(tok) < 3 || tok[1] != ' ' {
		return StatusEntry{}, fmt.Errorf("git status: untracked record short: %q", tok)
	}
	return StatusEntry{
		Path:          tok[2:],
		IndexState:    '?',
		WorktreeState: '?',
		Untracked:     true,
	}, nil
}

// Add stages `path` (the equivalent of `git add -- <path>`). Untracked files,
// modified worktree edits, and unmerged paths all use the same call — the
// caller does not need to branch by state.
func Add(ctx context.Context, dir, path string) error {
	return runGitWrite(ctx, dir, "git add", nil, "add", "--", path)
}

// RestoreStaged moves `path` out of the index back to "matches HEAD" (i.e.
// `git restore --staged -- <path>`). For a path that's both staged and
// modified in the worktree, only the index half flips; the worktree edits
// stay put. The TUI relies on this to make `space` a true toggle.
func RestoreStaged(ctx context.Context, dir, path string) error {
	return runGitWrite(ctx, dir, "git restore --staged", nil, "restore", "--staged", "--", path)
}

// DiffFileRaw is DiffFile without ANSI color (`color.ui=never`). Per-hunk
// staging feeds the result to `git apply`, which can't parse the colored diff
// the viewport renders — so the patch is rebuilt from this uncolored copy.
func DiffFileRaw(ctx context.Context, dir, path string, staged bool) (string, error) {
	return diffFile(ctx, dir, path, staged, "never")
}

// ApplyCached pipes a unified-diff patch to `git apply --cached`, staging it
// into the index (reverse=true unstages — applies the patch backwards). The
// patch must be a complete, valid unified diff (file header + one or more
// hunks); the TUI builds it from a single hunk so `git add` (whole file) isn't
// the only granularity. Untracked / conflict paths aren't supported (no index
// baseline to apply against) — the caller gates those to whole-file staging.
func ApplyCached(ctx context.Context, dir, patch string, reverse bool) error {
	args := []string{"apply", "--cached"}
	if reverse {
		args = append(args, "--reverse")
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	cmd.Stdin = strings.NewReader(patch)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return wrapGitErr("git apply --cached", err, stderr.String())
	}
	return nil
}

// DiffFile returns the unified diff for one tracked path. staged=true asks
// for the index-vs-HEAD diff (`--cached`); staged=false asks for the
// worktree-vs-index diff. ANSI color is preserved via `-c color.ui=always`
// so the TUI viewport renders git's own coloring.
//
// `git diff` exits 0 even when there's no diff, so we treat any non-zero
// exit as a real failure (unlike DiffUntracked).
func DiffFile(ctx context.Context, dir, path string, staged bool) (string, error) {
	return diffFile(ctx, dir, path, staged, "always")
}

// diffFile is the shared body for DiffFile / DiffFileRaw — identical except the
// `color.ui` mode (always for the viewport, never for the apply patch).
func diffFile(ctx context.Context, dir, path string, staged bool, colorMode string) (string, error) {
	args := []string{"-c", "color.ui=" + colorMode, "diff"}
	if staged {
		args = append(args, "--cached")
	}
	args = append(args, "--", path)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", wrapGitErr("git diff", err, stderr.String())
	}
	return stdout.String(), nil
}

// DiffNumstat returns per-file insertion/deletion counts, parsed from
// `git diff --numstat`. staged=false reports worktree-vs-index, staged=true
// reports index-vs-HEAD (`--cached`). Untracked files have no tracked baseline
// so they never appear here — the caller renders those without a stat. Binary
// files come back with Insertions/Deletions == -1 (see FileStat.Binary).
func DiffNumstat(ctx context.Context, dir string, staged bool) ([]FileStat, error) {
	args := []string{"diff", "--numstat"}
	if staged {
		args = append(args, "--cached")
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, wrapGitErr("git diff --numstat", err, stderr.String())
	}
	return parseNumstat(stdout.String())
}

// DiffUntracked renders an untracked file as a full-addition diff via
// `git diff --no-index /dev/null <path>`. Exit code 1 means "files differ"
// (always the case for untracked files) and is treated as success; only
// exit codes ≥ 2 are real errors per git's convention for diff.
func DiffUntracked(ctx context.Context, dir, path string) (string, error) {
	cmd := exec.CommandContext(ctx, "git",
		"-c", "color.ui=always",
		"diff", "--no-index", "--", "/dev/null", path,
	)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.String(), nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return stdout.String(), nil
	}
	return "", wrapGitErr("git diff --no-index", err, stderr.String())
}

// DiffUntrackedNumstat returns one untracked file's +/- counts as a FileStat,
// via `git diff --no-index --numstat -- /dev/null <path>`. Untracked files have
// no tracked baseline so they're absent from DiffNumstat — without this their
// "+N" column would render blank. The diff is a full addition (Deletions == 0);
// binary files come back Insertions/Deletions == -1 (FileStat.Binary). Exit
// code 1 means "files differ" (always true here) and is treated as success,
// mirroring DiffUntracked. The returned Path is pinned to the input `path` (the
// porcelain path) so the caller's per-path stat map keys line up regardless of
// how `--no-index` echoes the b-side.
func DiffUntrackedNumstat(ctx context.Context, dir, path string) (FileStat, error) {
	cmd := exec.CommandContext(ctx, "git", "diff", "--no-index", "--numstat", "--", "/dev/null", path)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			return FileStat{}, wrapGitErr("git diff --no-index --numstat", err, stderr.String())
		}
	}
	stats, err := parseNumstat(stdout.String())
	if err != nil {
		return FileStat{}, err
	}
	if len(stats) == 0 {
		return FileStat{}, fmt.Errorf("git diff --no-index --numstat: no output for %q", path)
	}
	fs := stats[0]
	fs.Path = path
	return fs, nil
}
