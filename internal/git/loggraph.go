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
)

// GraphRow is one line of `git log --graph` output. For commit lines,
// GraphPrefix carries the ASCII graph segment ('*', '|', '/', '\', '_', '-')
// and Commit holds the parsed metadata. For connector-only rows (e.g. "|/",
// "|\", "| | |") Commit is the zero value and IsCommit is false; the caller
// can drop those rows or use them to draw richer connectors between commits.
type GraphRow struct {
	GraphPrefix string
	Commit      Commit
	IsCommit    bool
}

// graphSentinel marks the boundary between the graph prefix and the commit
// format on a commit line. The string is chosen to be unlikely in commit
// messages, and parsing uses the *first* occurrence on each line — so an
// accidental match inside the subject (the last format field) is harmless.
const graphSentinel = "~~ORBIT~~"

// LogGraph runs `git log --graph --color=never` and returns one GraphRow per
// output line. IsCommit distinguishes commit lines from connector-only rows.
func LogGraph(ctx context.Context, opts LogOptions) ([]GraphRow, error) {
	args := []string{
		"log",
		"--graph",
		"--color=never",
		"--format=" + graphSentinel + logFormat,
	}
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
		return nil, fmt.Errorf("git log --graph: start: %w", err)
	}

	rows, parseErr := parseLogGraph(stdout)
	waitErr := cmd.Wait()
	if waitErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return nil, fmt.Errorf("git log --graph: %w", waitErr)
		}
		return nil, fmt.Errorf("git log --graph: %w: %s", waitErr, msg)
	}
	if parseErr != nil {
		return nil, fmt.Errorf("git log --graph: parse: %w", parseErr)
	}
	return rows, nil
}

func parseLogGraph(r io.Reader) ([]GraphRow, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var out []GraphRow
	for scanner.Scan() {
		line := scanner.Text()
		idx := strings.Index(line, graphSentinel)
		if idx < 0 {
			out = append(out, GraphRow{GraphPrefix: line})
			continue
		}
		prefix := line[:idx]
		rest := line[idx+len(graphSentinel):]
		c, err := parseLine(rest)
		if err != nil {
			return nil, err
		}
		out = append(out, GraphRow{
			GraphPrefix: prefix,
			Commit:      c,
			IsCommit:    true,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
