package git

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// FileStat is one file's contribution to a commit. Insertions/Deletions are
// -1 for binary files (git --numstat emits "-\t-" for them).
type FileStat struct {
	Path       string
	Insertions int
	Deletions  int
}

// Binary reports whether this entry is a binary file change. numstat marks
// these with literal "-" instead of a count, which we encode as -1.
func (f FileStat) Binary() bool { return f.Insertions < 0 }

// Stat returns the per-file diff stats for the given commit, parsed from
// `git show --numstat`. Returning structured data lets the TUI right-pane
// render its own colored "+N -M" layout instead of git's variable-width bar.
func Stat(ctx context.Context, dir, hash string) ([]FileStat, error) {
	raw, err := runShow(ctx, dir, "--numstat", "--format=", hash)
	if err != nil {
		return nil, err
	}
	return parseNumstat(raw)
}

// Patch returns the unified diff for the given commit hash. Header is
// suppressed via --format= and ANSI is preserved (-c color.ui=always) so the
// d-window viewport can show git's own coloring.
func Patch(ctx context.Context, dir, hash string) (string, error) {
	return runShow(ctx, dir, "--format=", "-p", hash)
}

// PatchForFile returns the unified diff for the given commit hash restricted
// to a single file path. The Changes-tab follower viewport calls this each
// time the file-list cursor moves so the right side reflects the focused
// entry.
func PatchForFile(ctx context.Context, dir, hash, path string) (string, error) {
	return runShow(ctx, dir, "--format=", "-p", hash, "--", path)
}

// Detail bundles the metadata the Commit tab renders for the focused commit:
// full hash, parent hashes, author/committer identity + ISO 8601 dates, the
// raw `%G?` sign-status code, and the entire commit message body.
//
// SignStatus follows git's --format=%G? table:
//
//	G = good (valid) signature
//	B = bad signature
//	U = good signature with unknown validity
//	X = good signature that has expired
//	Y = good signature made by an expired key
//	R = good signature made by a revoked key
//	E = signature cannot be checked (missing key, etc.)
//	N = no signature
type Detail struct {
	Hash           string
	Parents        []string
	AuthorName     string
	AuthorEmail    string
	AuthorDate     time.Time
	CommitterName  string
	CommitterEmail string
	CommitterDate  time.Time
	SignStatus     string
	Body           string
}

// CommitDetail loads the metadata bundle for a single commit via one
// `git show --no-patch` call. NUL-separated fields keep newlines in the body
// from interfering with the parser, and time.RFC3339 covers `%aI` / `%cI`.
func CommitDetail(ctx context.Context, dir, hash string) (Detail, error) {
	const format = "--format=%H%x00%P%x00%an%x00%ae%x00%aI%x00%cn%x00%ce%x00%cI%x00%G?%x00%B"
	raw, err := runShow(ctx, dir, "--no-patch", format, hash)
	if err != nil {
		return Detail{}, err
	}
	raw = strings.TrimRight(raw, "\n")
	parts := strings.SplitN(raw, "\x00", 10)
	if len(parts) < 10 {
		return Detail{}, fmt.Errorf("commit detail: expected 10 fields, got %d", len(parts))
	}
	d := Detail{
		Hash:           parts[0],
		AuthorName:     parts[2],
		AuthorEmail:    parts[3],
		CommitterName:  parts[5],
		CommitterEmail: parts[6],
		SignStatus:     parts[8],
		Body:           parts[9],
	}
	if parts[1] != "" {
		d.Parents = strings.Split(parts[1], " ")
	}
	if t, perr := time.Parse(time.RFC3339, parts[4]); perr == nil {
		d.AuthorDate = t
	}
	if t, perr := time.Parse(time.RFC3339, parts[7]); perr == nil {
		d.CommitterDate = t
	}
	return d, nil
}

func parseNumstat(s string) ([]FileStat, error) {
	var out []FileStat
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("numstat: unexpected line %q", line)
		}
		f := FileStat{Path: parts[2]}
		if parts[0] == "-" && parts[1] == "-" {
			f.Insertions, f.Deletions = -1, -1
			out = append(out, f)
			continue
		}
		ins, err := strconv.Atoi(parts[0])
		if err != nil {
			return nil, fmt.Errorf("numstat: insertions %q: %w", parts[0], err)
		}
		del, err := strconv.Atoi(parts[1])
		if err != nil {
			return nil, fmt.Errorf("numstat: deletions %q: %w", parts[1], err)
		}
		f.Insertions, f.Deletions = ins, del
		out = append(out, f)
	}
	return out, nil
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
