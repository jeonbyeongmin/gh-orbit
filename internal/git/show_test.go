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
