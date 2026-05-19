// Zombie branch detection — local branches that the cockpit can safely
// suggest for cleanup. Three-condition guard (matches CEO plan D4):
//
//  1. merged into the baseline (default branch). Anything not yet merged
//     would lose work on a plain `git branch -d`.
//  2. upstream gone (`[gone]` track marker). The remote-side branch has
//     been deleted, which is the strongest signal that the local copy is
//     vestigial after a squash-merge.
//  3. not checked out in any worktree. `git branch -d` rejects a
//     currently checked-out branch anyway, but the cockpit filters this
//     up-front so the confirm list never offers an option that's going
//     to fail.
package git

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// ZombieBranch is one local branch ready for cleanup. Only the short name
// is needed for the confirm list + delete dispatch — the TUI doesn't render
// per-row metadata for the cleanup modal.
type ZombieBranch struct {
	Name string
}

// DetectZombieBranches returns the local branches that satisfy the three
// guard conditions against baseline. baseline is the default branch name
// (e.g. "develop") — typically resolved via ResolveDefaultBranch beforehand.
//
// Returns an empty slice (not nil) when nothing qualifies so callers can
// treat len(result) == 0 as the "no zombies" status without nil checks.
func DetectZombieBranches(ctx context.Context, dir, baseline string) ([]ZombieBranch, error) {
	if baseline == "" {
		return nil, fmt.Errorf("DetectZombieBranches: baseline required")
	}
	checkedOut, err := checkedOutBranches(ctx, dir)
	if err != nil {
		return nil, err
	}
	return detectZombiesWith(ctx, dir, baseline, checkedOut)
}

func detectZombiesWith(ctx context.Context, dir, baseline string, checkedOut map[string]struct{}) ([]ZombieBranch, error) {
	raw, err := forEachRefMergedWithTrack(ctx, dir, baseline)
	if err != nil {
		return nil, err
	}
	rows, err := parseZombieRows(raw)
	if err != nil {
		return nil, err
	}
	return filterZombieRows(rows, baseline, checkedOut), nil
}

// filterZombieRows applies the three-condition guard: drop the baseline
// itself, drop branches checked out in any worktree, keep only entries
// where upstream:track says `[gone]`. Split out from detectZombiesWith
// so the guard can be unit-tested without spinning up a real git repo.
func filterZombieRows(rows []zombieRow, baseline string, checkedOut map[string]struct{}) []ZombieBranch {
	out := make([]ZombieBranch, 0, len(rows))
	for _, row := range rows {
		if row.name == "" || row.name == baseline {
			continue
		}
		if _, busy := checkedOut[row.name]; busy {
			continue
		}
		if !row.upstreamGone {
			continue
		}
		out = append(out, ZombieBranch{Name: row.name})
	}
	return out
}

// zombieRow is the raw shape of a `git for-each-ref --merged` line. Kept
// internal so the porcelain format stays an implementation detail of this
// file (parse + detect are the only two callers).
type zombieRow struct {
	name         string
	upstreamGone bool
}

func forEachRefMergedWithTrack(ctx context.Context, dir, baseline string) (string, error) {
	cmd := exec.CommandContext(ctx, "git",
		"for-each-ref",
		"--merged="+baseline,
		"--format=%(refname:short)%00%(upstream:track)",
		"refs/heads",
	)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", wrapGitErr("git for-each-ref --merged", err, stderr.String())
	}
	return stdout.String(), nil
}

func parseZombieRows(raw string) ([]zombieRow, error) {
	var out []zombieRow
	for _, line := range strings.Split(raw, "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\x00", 2)
		if len(parts) < 2 {
			return nil, fmt.Errorf("git for-each-ref: malformed row: %q", line)
		}
		out = append(out, zombieRow{
			name:         parts[0],
			upstreamGone: strings.Contains(parts[1], "[gone]"),
		})
	}
	return out, nil
}

// checkedOutBranches enumerates every branch attached to a worktree (main
// + linked). Detached HEADs contribute nothing. The set is keyed by the
// short branch name so detectZombiesWith can do a direct lookup.
func checkedOutBranches(ctx context.Context, dir string) (map[string]struct{}, error) {
	worktrees, err := Worktrees(ctx, dir)
	if err != nil {
		return nil, err
	}
	out := make(map[string]struct{}, len(worktrees))
	for _, w := range worktrees {
		if w.Branch != "" {
			out[w.Branch] = struct{}{}
		}
	}
	return out, nil
}

// ResolveDefaultBranch picks the baseline that DetectZombieBranches walks
// against. Resolution order: gh CLI → origin/HEAD symref → "develop".
// All three steps are best-effort; the final fallback ensures the cockpit
// always has *some* baseline so the cleanup flow stays interactive (an
// empty string would force callers into an error branch).
func ResolveDefaultBranch(ctx context.Context, dir string) string {
	if name := resolveDefaultBranchGH(ctx, dir); name != "" {
		return name
	}
	if name := resolveDefaultBranchOriginHead(ctx, dir); name != "" {
		return name
	}
	return "develop"
}

func resolveDefaultBranchGH(ctx context.Context, dir string) string {
	if _, err := exec.LookPath("gh"); err != nil {
		return ""
	}
	cmd := exec.CommandContext(ctx, "gh", "repo", "view", "--json", "defaultBranchRef", "-q", ".defaultBranchRef.name")
	cmd.Dir = dir
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(stdout.String())
}

func resolveDefaultBranchOriginHead(ctx context.Context, dir string) string {
	cmd := exec.CommandContext(ctx, "git", "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	cmd.Dir = dir
	cmd.Env = gitEnv()
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return ""
	}
	out := strings.TrimSpace(stdout.String())
	if i := strings.IndexByte(out, '/'); i >= 0 {
		out = out[i+1:]
	}
	return out
}
