package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseLog(t *testing.T) {
	in := strings.Join([]string{
		"abc123\x00def456\x00Alice\x00alice@example.com\x001700000000\x00add feature",
		"def456\x00\x00Bob\x00bob@example.com\x001699999999\x00initial commit",
	}, "\n") + "\n"

	got, err := parseLog(strings.NewReader(in))
	if err != nil {
		t.Fatalf("parseLog: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d commits, want 2", len(got))
	}

	c0 := got[0]
	if c0.Hash != "abc123" || c0.Subject != "add feature" {
		t.Errorf("commit[0] = %+v", c0)
	}
	if len(c0.Parents) != 1 || c0.Parents[0] != "def456" {
		t.Errorf("commit[0].Parents = %v, want [def456]", c0.Parents)
	}
	if !c0.AuthorTime.Equal(time.Unix(1700000000, 0)) {
		t.Errorf("commit[0].AuthorTime = %v", c0.AuthorTime)
	}

	if got[1].Parents != nil {
		t.Errorf("root commit should have no parents, got %v", got[1].Parents)
	}
}

func TestParseLogMergeCommitHasMultipleParents(t *testing.T) {
	in := "merge1\x00p1 p2\x00Carol\x00carol@example.com\x001700000100\x00merge branch 'feat'\n"
	got, err := parseLog(strings.NewReader(in))
	if err != nil {
		t.Fatalf("parseLog: %v", err)
	}
	if len(got) != 1 || len(got[0].Parents) != 2 {
		t.Fatalf("expected 1 commit with 2 parents, got %+v", got)
	}
	if got[0].Parents[0] != "p1" || got[0].Parents[1] != "p2" {
		t.Errorf("parents = %v, want [p1 p2]", got[0].Parents)
	}
}

func TestParseLineRejectsMalformed(t *testing.T) {
	if _, err := parseLine("only-one-field"); err == nil {
		t.Error("expected error on malformed line")
	}
}

// TestLogIntegration exercises the real git binary against a throwaway repo,
// which catches format-string drift between our %H%x00... template and what
// git actually emits.
func TestLogIntegration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()

	gitRun(t, dir, "init", "-b", "main")
	gitRun(t, dir, "commit", "--allow-empty", "-m", "first")
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	gitRun(t, dir, "add", "f")
	gitRun(t, dir, "commit", "-m", "second")

	commits, err := Log(context.Background(), LogOptions{Dir: dir})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(commits) != 2 {
		t.Fatalf("got %d commits, want 2", len(commits))
	}
	if commits[0].Subject != "second" || commits[1].Subject != "first" {
		t.Errorf("subjects = [%q, %q], want [second, first]", commits[0].Subject, commits[1].Subject)
	}
	if len(commits[0].Parents) != 1 {
		t.Errorf("HEAD should have one parent, got %v", commits[0].Parents)
	}
	if len(commits[1].Parents) != 0 {
		t.Errorf("root should have no parents, got %v", commits[1].Parents)
	}
}

func TestLogReturnsErrorWithStderr(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	// A directory that isn't a git repo. git log should fail and we want
	// the wrapped error to carry stderr's message, not just "exit status N".
	dir := t.TempDir()
	_, err := Log(context.Background(), LogOptions{Dir: dir})
	if err == nil {
		t.Fatal("expected error running git log outside a repo")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("error %q should include git's stderr message", err)
	}
}
