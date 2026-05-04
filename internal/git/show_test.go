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

	out, err := Stat(context.Background(), dir, "HEAD")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !strings.Contains(out, "f.txt") {
		t.Errorf("stat output should mention the file, got %q", out)
	}
	// "1 file changed" plus "3 insertions" or similar — git's wording is
	// stable enough that a substring check is fine.
	if !strings.Contains(out, "insertion") {
		t.Errorf("stat output should mention insertions, got %q", out)
	}
	if strings.Contains(out, "Author:") {
		t.Errorf("stat output should not include the commit header, got %q", out)
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
