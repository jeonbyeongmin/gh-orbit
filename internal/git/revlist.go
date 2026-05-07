package git

import (
	"bufio"
	"bytes"
	"context"
	"os/exec"
	"strings"
)

// RevListAncestors returns the set of commit hashes reachable from ref
// (i.e. ref's ancestry — ref itself plus every parent transitively).
// Membership lookup is the consumer's hot path — graph render checks one
// hash per visible row — so the return type is a set keyed on full 40-char
// hashes for O(1) lookup.
//
// Empty input ref defaults to "HEAD" so callers don't have to special-case
// the common path. On a fatal git error (unknown revision, empty repo,
// transport failure for a remote ref) the wrapped stderr surfaces in the
// returned error via wrapGitErr.
func RevListAncestors(ctx context.Context, dir, ref string) (map[string]struct{}, error) {
	if ref == "" {
		ref = "HEAD"
	}
	cmd := exec.CommandContext(ctx, "git", "rev-list", ref)
	cmd.Dir = dir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, wrapGitErr("git rev-list", err, stderr.String())
	}

	out := make(map[string]struct{}, 256)
	scanner := bufio.NewScanner(&stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		hash := strings.TrimSpace(scanner.Text())
		if hash == "" {
			continue
		}
		out[hash] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		return nil, wrapGitErr("git rev-list: scan", err, "")
	}
	return out, nil
}
