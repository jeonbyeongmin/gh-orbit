package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseStashList(t *testing.T) {
	// Two entries: stash@{0} with an untracked-files commit (3 parents),
	// stash@{1} with index only (2 parents). NUL field separators.
	in := strings.Join([]string{
		"stash@{0}\x00aaa0\x00base0 idx0 unt0\x00On main: debug logging",
		"stash@{1}\x00aaa1\x00base1 idx1\x00WIP on main: abc123 commit subject",
	}, "\n") + "\n"

	got, err := parseStashList(strings.NewReader(in))
	if err != nil {
		t.Fatalf("parseStashList: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Ref != "stash@{0}" || got[0].Hash != "aaa0" || got[0].Subject != "On main: debug logging" {
		t.Errorf("got[0] = %+v", got[0])
	}
	if len(got[0].Parents) != 3 || got[0].Parents[0] != "base0" || got[0].Parents[2] != "unt0" {
		t.Errorf("got[0].Parents = %v, want [base0 idx0 unt0]", got[0].Parents)
	}
	if len(got[1].Parents) != 2 || got[1].Parents[1] != "idx1" {
		t.Errorf("got[1].Parents = %v, want [base1 idx1]", got[1].Parents)
	}
}

func TestParseStashListEmpty(t *testing.T) {
	got, err := parseStashList(strings.NewReader(""))
	if err != nil || got != nil {
		t.Errorf("empty: got=%v err=%v, want (nil,nil)", got, err)
	}
}

// TestStashListApplyPopDropIntegration drives the real git stash surface end
// to end: list (newest-first, with parents), apply (keeps the slot), pop
// (applies + drops), drop (removes).
func TestStashListApplyPopDropIntegration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	work := t.TempDir()
	gitRun(t, work, "init", "-b", "main")
	gitRun(t, work, "config", "user.name", "Local")
	gitRun(t, work, "config", "user.email", "local@example.com")
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(work, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("f.txt", "base\n")
	gitRun(t, work, "add", "f.txt")
	gitRun(t, work, "commit", "-m", "base")
	base := strings.TrimSpace(gitOutput(t, work, "rev-parse", "HEAD"))

	// Two stashes, newest-first: stash@{0}="second", stash@{1}="first".
	write("f.txt", "first change\n")
	gitRun(t, work, "stash", "push", "-m", "first")
	write("f.txt", "second change\n")
	gitRun(t, work, "stash", "push", "-m", "second")

	ctx := context.Background()
	entries, err := StashList(ctx, work)
	if err != nil {
		t.Fatalf("StashList: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("StashList len = %d, want 2", len(entries))
	}
	if entries[0].Ref != "stash@{0}" || !strings.Contains(entries[0].Subject, "second") {
		t.Errorf("entries[0] = %+v, want newest 'second'", entries[0])
	}
	if len(entries[0].Parents) == 0 || entries[0].Parents[0] != base {
		t.Errorf("entries[0].Parents[0] = %v, want base %s", entries[0].Parents, base)
	}

	// Apply stash@{0} ("second") — tree gets it, slot survives.
	if err := StashApply(ctx, work, "stash@{0}"); err != nil {
		t.Fatalf("StashApply: %v", err)
	}
	if got := strings.TrimSpace(readFile(t, work, "f.txt")); got != "second change" {
		t.Errorf("f.txt = %q after apply, want 'second change'", got)
	}
	if out := gitOutput(t, work, "stash", "list"); strings.Count(out, "stash@{") != 2 {
		t.Errorf("apply must keep both slots; list = %q", out)
	}

	// Reset the tree, then pop stash@{0} — applies and drops it.
	gitRun(t, work, "checkout", "--", "f.txt")
	if err := StashPop(ctx, work, "stash@{0}"); err != nil {
		t.Fatalf("StashPop: %v", err)
	}
	if out := gitOutput(t, work, "stash", "list"); strings.Count(out, "stash@{") != 1 {
		t.Errorf("pop must drop the slot; list = %q", out)
	}

	// Drop the remaining stash@{0} ("first").
	if err := StashDrop(ctx, work, "stash@{0}"); err != nil {
		t.Fatalf("StashDrop: %v", err)
	}
	if out := strings.TrimSpace(gitOutput(t, work, "stash", "list")); out != "" {
		t.Errorf("drop must empty the stash; list = %q", out)
	}
}

func readFile(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}
