package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestShowStatIntegration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitRun(t, dir, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	gitRun(t, dir, "add", "f.txt")
	gitRun(t, dir, "commit", "-m", "first")

	files, err := Stat(context.Background(), dir, "HEAD")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("got %d files, want 1: %+v", len(files), files)
	}
	got := files[0]
	if got.Path != "f.txt" {
		t.Errorf("Path = %q, want f.txt", got.Path)
	}
	if got.Insertions != 3 {
		t.Errorf("Insertions = %d, want 3", got.Insertions)
	}
	if got.Deletions != 0 {
		t.Errorf("Deletions = %d, want 0", got.Deletions)
	}
	if got.Binary() {
		t.Error("text file should not be flagged Binary")
	}
}

func TestParseNumstatHandlesBinaryAndRename(t *testing.T) {
	in := "3\t1\tinternal/tui/diff.go\n-\t-\tassets/logo.png\n5\t2\told/{a => b}/file.txt\n"
	out, err := parseNumstat(in)
	if err != nil {
		t.Fatalf("parseNumstat: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("got %d entries, want 3", len(out))
	}
	if out[0].Path != "internal/tui/diff.go" || out[0].Insertions != 3 || out[0].Deletions != 1 {
		t.Errorf("text entry = %+v", out[0])
	}
	if !out[1].Binary() || out[1].Path != "assets/logo.png" {
		t.Errorf("binary entry = %+v, want Binary=true Path=assets/logo.png", out[1])
	}
	if out[2].Path != "old/{a => b}/file.txt" || out[2].Insertions != 5 || out[2].Deletions != 2 {
		t.Errorf("rename entry = %+v", out[2])
	}
}

func TestParseNumstatRejectsMalformed(t *testing.T) {
	if _, err := parseNumstat("only-one-field\n"); err == nil {
		t.Error("expected error on malformed numstat line")
	}
	if _, err := parseNumstat("notanumber\t0\tfile\n"); err == nil {
		t.Error("expected error on non-numeric insertions")
	}
}

func TestShowPatchIntegration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitRun(t, dir, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	gitRun(t, dir, "add", "f.txt")
	gitRun(t, dir, "commit", "-m", "first")

	out, err := Patch(context.Background(), dir, "HEAD")
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if !strings.Contains(out, "diff --git") {
		t.Errorf("patch output should start with diff header, got %q", out)
	}
	// `+` and `alpha` may be wrapped in separate ANSI color escapes since the
	// + sign and the inserted text get distinct styling, so check independently.
	if !strings.Contains(out, "alpha") {
		t.Errorf("patch should include the added line content, got %q", out)
	}
	if !strings.Contains(out, "+") {
		t.Errorf("patch should include the addition marker, got %q", out)
	}
	if strings.Contains(out, "Author:") {
		t.Errorf("patch should not include commit header, got %q", out)
	}
}

func TestShowPatchForFileIsolatesPath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitRun(t, dir, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("beta\n"), 0o644); err != nil {
		t.Fatalf("write b: %v", err)
	}
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "first")

	out, err := PatchForFile(context.Background(), dir, "HEAD", "a.txt")
	if err != nil {
		t.Fatalf("PatchForFile: %v", err)
	}
	if !strings.Contains(out, "a.txt") {
		t.Errorf("patch should include the requested path, got %q", out)
	}
	if !strings.Contains(out, "alpha") {
		t.Errorf("patch should include the requested file's content, got %q", out)
	}
	if strings.Contains(out, "b.txt") || strings.Contains(out, "beta") {
		t.Errorf("patch must not leak the unselected file, got %q", out)
	}
}

func TestShowCommitDetailParsesAllFields(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitRun(t, dir, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	gitRun(t, dir, "add", "f.txt")
	// Commit with a multi-line body and a fixed date so the parser surface is
	// exercised end-to-end (subject, body, dates, sign-status).
	cmd := exec.Command("git", "commit", "-m", "subject line\n\nbody line 1\nbody line 2\n")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Alice", "GIT_AUTHOR_EMAIL=alice@example.com",
		"GIT_AUTHOR_DATE=2026-01-15T10:20:30+00:00",
		"GIT_COMMITTER_NAME=Bob", "GIT_COMMITTER_EMAIL=bob@example.com",
		"GIT_COMMITTER_DATE=2026-01-15T10:20:30+00:00",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, out)
	}

	d, err := CommitDetail(context.Background(), dir, "HEAD")
	if err != nil {
		t.Fatalf("CommitDetail: %v", err)
	}
	if len(d.Hash) != 40 {
		t.Errorf("Hash length = %d, want 40", len(d.Hash))
	}
	if d.AuthorName != "Alice" || d.AuthorEmail != "alice@example.com" {
		t.Errorf("author = %q <%q>, want Alice <alice@example.com>", d.AuthorName, d.AuthorEmail)
	}
	if d.CommitterName != "Bob" || d.CommitterEmail != "bob@example.com" {
		t.Errorf("committer = %q <%q>, want Bob <bob@example.com>", d.CommitterName, d.CommitterEmail)
	}
	if d.AuthorDate.IsZero() {
		t.Error("AuthorDate should parse from %aI")
	}
	if d.AuthorDate.Year() != 2026 || d.AuthorDate.Month() != 1 || d.AuthorDate.Day() != 15 {
		t.Errorf("AuthorDate = %v, want 2026-01-15", d.AuthorDate)
	}
	if d.SignStatus != "N" {
		t.Errorf("SignStatus = %q, want N (no signature)", d.SignStatus)
	}
	if !strings.Contains(d.Body, "subject line") || !strings.Contains(d.Body, "body line 1") {
		t.Errorf("Body should contain the full message, got %q", d.Body)
	}
	if len(d.Parents) != 0 {
		t.Errorf("first commit should have zero parents, got %v", d.Parents)
	}
}

func TestShowCommitDetailParsesParents(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitRun(t, dir, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	gitRun(t, dir, "add", "f.txt")
	gitRun(t, dir, "commit", "-m", "first")
	gitRun(t, dir, "checkout", "-b", "feat")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("a\nb\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	gitRun(t, dir, "commit", "-am", "feat side")
	gitRun(t, dir, "checkout", "main")
	if err := os.WriteFile(filepath.Join(dir, "g.txt"), []byte("c\n"), 0o644); err != nil {
		t.Fatalf("write g: %v", err)
	}
	gitRun(t, dir, "add", "g.txt")
	gitRun(t, dir, "commit", "-m", "main side")
	gitRun(t, dir, "merge", "--no-ff", "feat", "-m", "merge feat")

	d, err := CommitDetail(context.Background(), dir, "HEAD")
	if err != nil {
		t.Fatalf("CommitDetail: %v", err)
	}
	if len(d.Parents) != 2 {
		t.Errorf("merge commit should have 2 parents, got %d: %v", len(d.Parents), d.Parents)
	}
	for _, p := range d.Parents {
		if len(p) != 40 {
			t.Errorf("parent hash length = %d, want 40 (%q)", len(p), p)
		}
	}
}

func TestShowReturnsErrorWithStderr(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitRun(t, dir, "init", "-b", "main")
	gitRun(t, dir, "commit", "--allow-empty", "-m", "first")

	_, err := Stat(context.Background(), dir, "deadbeefcafebabe")
	if err == nil {
		t.Fatal("Stat against missing hash should return an error")
	}
	// git's stderr says something like "fatal: bad revision" or "ambiguous
	// argument 'deadbeef…': unknown revision". Either substring is fine.
	if !strings.Contains(err.Error(), "fatal") && !strings.Contains(err.Error(), "unknown revision") && !strings.Contains(err.Error(), "bad revision") {
		t.Errorf("error should carry git's stderr, got %q", err)
	}
}
