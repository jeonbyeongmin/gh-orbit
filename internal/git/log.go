// Package git wraps the user's `git` binary. The TUI must never construct an
// *exec.Cmd directly — go through the typed wrappers here so stubbing in
// tests stays tractable and stderr surfaces in errors.
package git

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Commit is one entry from `git log`. Parents come straight from %P, so the
// graph renderer has everything it needs to draw lines without a second
// round-trip to git.
type Commit struct {
	Hash        string
	Parents     []string
	AuthorName  string
	AuthorEmail string
	AuthorTime  time.Time
	Subject     string
	// RefNames carries the raw "%D" tokens (e.g. "HEAD -> main",
	// "origin/main", "tag: v0.0.1"). Tokens keep their decoration prefixes;
	// semantic classification lives in ParseDecoration so the renderer
	// doesn't fork on token shape.
	RefNames []string
}

// LogOptions selects which commits Log returns.
type LogOptions struct {
	// Refs to walk. Empty defaults to HEAD. Pass "--all" to include every ref.
	Refs []string
	// Hard limit. 0 means no limit (let git decide / defer to repo size).
	MaxCount int
	// Working directory of the repo. Empty uses the process cwd.
	Dir string
}

// NUL between fields, newline between records. Subjects are single-line in
// git log output, so newline-as-record-separator is safe; field values may
// contain anything except NUL, which git itself will never emit here.
const logFormat = "%H%x00%P%x00%an%x00%ae%x00%at%x00%s%x00%D"

// Log runs `git log` and returns every matching commit, buffered. Fine for
// the MVP; once we wire up the graph pane against large repos we'll add a
// streaming variant that emits commits over a channel as they're parsed.
func Log(ctx context.Context, opts LogOptions) ([]Commit, error) {
	args := []string{"log", "--format=" + logFormat}
	if opts.MaxCount > 0 {
		args = append(args, "-n", strconv.Itoa(opts.MaxCount))
	}
	if len(opts.Refs) > 0 {
		args = append(args, opts.Refs...)
	} else {
		args = append(args, "HEAD")
	}

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = opts.Dir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("git log: start: %w", err)
	}

	commits, parseErr := parseLog(stdout)
	waitErr := cmd.Wait()
	if waitErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return nil, fmt.Errorf("git log: %w", waitErr)
		}
		return nil, fmt.Errorf("git log: %w: %s", waitErr, msg)
	}
	if parseErr != nil {
		return nil, fmt.Errorf("git log: parse: %w", parseErr)
	}
	return commits, nil
}

func parseLog(r io.Reader) ([]Commit, error) {
	scanner := bufio.NewScanner(r)
	// Subject lines can blow past Scanner's 64KiB default in pathological repos.
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var out []Commit
	for scanner.Scan() {
		c, err := parseLine(scanner.Text())
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func parseLine(line string) (Commit, error) {
	fields := strings.Split(line, "\x00")
	if len(fields) != 7 {
		return Commit{}, fmt.Errorf("unexpected field count %d in %q", len(fields), line)
	}
	ts, err := strconv.ParseInt(fields[4], 10, 64)
	if err != nil {
		return Commit{}, fmt.Errorf("parse author timestamp %q: %w", fields[4], err)
	}
	var parents []string
	if fields[1] != "" {
		parents = strings.Split(fields[1], " ")
	}
	return Commit{
		Hash:        fields[0],
		Parents:     parents,
		AuthorName:  fields[2],
		AuthorEmail: fields[3],
		AuthorTime:  time.Unix(ts, 0).UTC(),
		Subject:     fields[5],
		RefNames:    parseRefNames(fields[6]),
	}, nil
}

// parseRefNames splits a raw "%D" payload ("HEAD -> main, origin/main, tag: v0.0.1")
// into its comma-separated tokens. Empty payload returns nil — common, since
// most commits aren't a ref tip.
func parseRefNames(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ", ")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
