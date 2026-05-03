package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLogGraphLinear(t *testing.T) {
	in := "* " + graphSentinel + "abc123\x00\x00Alice\x00a@example.com\x001700000000\x00first\n"
	rows, err := parseLogGraph(strings.NewReader(in))
	if err != nil {
		t.Fatalf("parseLogGraph: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	r := rows[0]
	if !r.IsCommit {
		t.Error("expected commit row")
	}
	if r.GraphPrefix != "* " {
		t.Errorf("GraphPrefix = %q, want %q", r.GraphPrefix, "* ")
	}
	if r.Commit.Hash != "abc123" || r.Commit.Subject != "first" {
		t.Errorf("commit = %+v", r.Commit)
	}
}

func TestParseLogGraphMergeWithConnector(t *testing.T) {
	in := strings.Join([]string{
		"*   " + graphSentinel + "merge1\x00p1 p2\x00Bob\x00b@example.com\x001700000100\x00merge",
		"|\\",
		"| * " + graphSentinel + "feat1\x00p1\x00Bob\x00b@example.com\x001700000050\x00feat",
		"|/",
		"* " + graphSentinel + "p1\x00\x00Bob\x00b@example.com\x001700000000\x00root",
	}, "\n") + "\n"

	rows, err := parseLogGraph(strings.NewReader(in))
	if err != nil {
		t.Fatalf("parseLogGraph: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("got %d rows, want 5", len(rows))
	}

	commits := 0
	for _, r := range rows {
		if r.IsCommit {
			commits++
		}
	}
	if commits != 3 {
		t.Errorf("commit row count = %d, want 3", commits)
	}
	if rows[1].IsCommit || rows[1].GraphPrefix != "|\\" {
		t.Errorf("row[1] should be connector |\\, got %+v", rows[1])
	}
	if rows[3].IsCommit || rows[3].GraphPrefix != "|/" {
		t.Errorf("row[3] should be connector |/, got %+v", rows[3])
	}
	if rows[0].Commit.Subject != "merge" || len(rows[0].Commit.Parents) != 2 {
		t.Errorf("merge commit = %+v", rows[0].Commit)
	}
}

// First-occurrence parsing keeps a sentinel inside the subject from breaking
// the boundary detection.
func TestParseLogGraphSubjectContainingSentinel(t *testing.T) {
	in := "* " + graphSentinel + "abc\x00\x00A\x00a@b\x001700000000\x00mention " + graphSentinel + " marker\n"
	rows, err := parseLogGraph(strings.NewReader(in))
	if err != nil {
		t.Fatalf("parseLogGraph: %v", err)
	}
	if len(rows) != 1 || !rows[0].IsCommit {
		t.Fatalf("rows = %+v", rows)
	}
	want := "mention " + graphSentinel + " marker"
	if rows[0].Commit.Subject != want {
		t.Errorf("Subject = %q, want %q", rows[0].Commit.Subject, want)
	}
}

// TestLogGraphIntegration exercises the real git binary against a throwaway
// repo to catch any drift in the --graph output format.
func TestLogGraphIntegration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}

	run("init", "-b", "main")
	run("commit", "--allow-empty", "-m", "first")
	if err := os.WriteFile(filepath.Join(dir, "a"), []byte("a"), 0o644); err != nil {
		t.Fatalf("write a: %v", err)
	}
	run("add", "a")
	run("commit", "-m", "second on main")

	run("checkout", "-b", "feat")
	if err := os.WriteFile(filepath.Join(dir, "b"), []byte("b"), 0o644); err != nil {
		t.Fatalf("write b: %v", err)
	}
	run("add", "b")
	run("commit", "-m", "feat work")

	run("checkout", "main")
	run("merge", "--no-ff", "-m", "merge feat", "feat")

	rows, err := LogGraph(context.Background(), LogOptions{Dir: dir, Refs: []string{"--all"}})
	if err != nil {
		t.Fatalf("LogGraph: %v", err)
	}

	commits := 0
	for _, r := range rows {
		if r.IsCommit {
			commits++
			if r.GraphPrefix == "" {
				t.Errorf("commit row has empty GraphPrefix: %+v", r)
			}
			if !strings.ContainsAny(r.GraphPrefix, "*|") {
				t.Errorf("graph prefix %q lacks expected glyphs", r.GraphPrefix)
			}
		}
	}
	if commits != 4 {
		t.Errorf("commit count = %d, want 4 (first, second, feat, merge)", commits)
	}
}
