package git

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// RefKind partitions refs by namespace so the TUI can render them in
// separate sections without re-parsing FullName.
type RefKind int

const (
	RefKindUnknown RefKind = iota
	RefKindLocal
	RefKindRemote
	RefKindTag
)

func (k RefKind) String() string {
	switch k {
	case RefKindLocal:
		return "local"
	case RefKindRemote:
		return "remote"
	case RefKindTag:
		return "tag"
	default:
		return "unknown"
	}
}

// Ref is one entry from `git for-each-ref`. ObjectName is the resolved
// commit hash — for annotated tags this is the peeled commit (%(*objectname)),
// not the tag object itself, so the graph pane can use it directly.
type Ref struct {
	FullName   string
	ShortName  string
	Kind       RefKind
	ObjectName string
	IsHead     bool
	Upstream   string
}

// ForEachRefOptions selects which refs ForEachRef returns.
type ForEachRefOptions struct {
	Dir      string
	Patterns []string
}

const refFormat = "%(refname)%00%(refname:short)%00%(objecttype)%00%(objectname)%00%(*objectname)%00%(HEAD)%00%(upstream:short)"

// defaultRefPatterns intentionally excludes refs/stash, refs/notes, refs/pull,
// etc. — those have separate UX and would clutter the Local/Remote/Tags split.
var defaultRefPatterns = []string{"refs/heads/", "refs/remotes/", "refs/tags/"}

// ForEachRef runs `git for-each-ref` and returns one Ref per matching entry.
// Symbolic remote HEADs (refs/remotes/origin/HEAD) and refs that don't
// resolve to a commit-like object are dropped.
func ForEachRef(ctx context.Context, opts ForEachRefOptions) ([]Ref, error) {
	patterns := opts.Patterns
	if len(patterns) == 0 {
		patterns = defaultRefPatterns
	}
	args := append([]string{"for-each-ref", "--format=" + refFormat}, patterns...)

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = opts.Dir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("git for-each-ref: start: %w", err)
	}

	refs, parseErr := parseForEachRef(stdout)
	waitErr := cmd.Wait()
	if waitErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return nil, fmt.Errorf("git for-each-ref: %w", waitErr)
		}
		return nil, fmt.Errorf("git for-each-ref: %w: %s", waitErr, msg)
	}
	if parseErr != nil {
		return nil, fmt.Errorf("git for-each-ref: parse: %w", parseErr)
	}
	return refs, nil
}

func parseForEachRef(r io.Reader) ([]Ref, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var out []Ref
	for scanner.Scan() {
		ref, keep, err := parseRefLine(scanner.Text())
		if err != nil {
			return nil, err
		}
		if !keep {
			continue
		}
		out = append(out, ref)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// parseRefLine returns (ref, true, nil) for an accepted ref, (zero, false, nil)
// for refs we deliberately drop (symbolic remote HEAD, non-commit/non-tag
// objects, refs outside heads/remotes/tags), and (_, _, err) for malformed
// input.
func parseRefLine(line string) (Ref, bool, error) {
	fields := strings.Split(line, "\x00")
	if len(fields) != 7 {
		return Ref{}, false, fmt.Errorf("unexpected field count %d in %q", len(fields), line)
	}

	fullName := fields[0]
	shortName := fields[1]
	objType := fields[2]
	objName := fields[3]
	peeled := fields[4]
	headMark := fields[5]
	upstream := fields[6]

	kind := refKindFromName(fullName)
	if kind == RefKindUnknown {
		return Ref{}, false, nil
	}
	if kind == RefKindRemote && strings.HasSuffix(shortName, "/HEAD") {
		return Ref{}, false, nil
	}
	if objType != "commit" && objType != "tag" {
		return Ref{}, false, nil
	}

	// Annotated tag: %(*objectname) is the peeled commit; objectname is the
	// tag object. Lightweight tag / branch: %(*objectname) is empty so fall
	// back to objectname which is already the commit.
	hash := peeled
	if hash == "" {
		hash = objName
	}

	return Ref{
		FullName:   fullName,
		ShortName:  shortName,
		Kind:       kind,
		ObjectName: hash,
		IsHead:     headMark == "*",
		Upstream:   upstream,
	}, true, nil
}

func refKindFromName(fullName string) RefKind {
	switch {
	case strings.HasPrefix(fullName, "refs/heads/"):
		return RefKindLocal
	case strings.HasPrefix(fullName, "refs/remotes/"):
		return RefKindRemote
	case strings.HasPrefix(fullName, "refs/tags/"):
		return RefKindTag
	default:
		return RefKindUnknown
	}
}
