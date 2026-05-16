package git

import (
	"bytes"
	"context"
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
	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain=v2", "-z", "--untracked-files=all")
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
