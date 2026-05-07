package git

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func TestRevListAncestorsReturnsErrorOutsideRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	_, err := RevListAncestors(context.Background(), dir, "HEAD")
	if err == nil {
		t.Fatal("expected error running rev-list outside a repo")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("error %q should include git's stderr message", err)
	}
}

func TestRevListAncestorsReturnsErrorOnUnknownRevision(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitRun(t, dir, "init", "-b", "main")
	gitRun(t, dir, "commit", "--allow-empty", "-m", "first")

	_, err := RevListAncestors(context.Background(), dir, "no-such-ref")
	if err == nil {
		t.Fatal("expected error for unknown revision")
	}
	if !strings.Contains(err.Error(), "unknown revision") &&
		!strings.Contains(err.Error(), "Needed a single revision") {
		t.Errorf("error %q should mention git's revision error", err)
	}
}

func TestRevListAncestorsHappyPath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitRun(t, dir, "init", "-b", "main")
	gitRun(t, dir, "commit", "--allow-empty", "-m", "first")
	first := gitOutput(t, dir, "rev-parse", "HEAD")
	gitRun(t, dir, "commit", "--allow-empty", "-m", "second")
	second := gitOutput(t, dir, "rev-parse", "HEAD")

	got, err := RevListAncestors(context.Background(), dir, "HEAD")
	if err != nil {
		t.Fatalf("RevListAncestors: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d ancestors, want 2: %v", len(got), got)
	}
	if _, ok := got[first]; !ok {
		t.Errorf("first commit %q missing from ancestors %v", first, got)
	}
	if _, ok := got[second]; !ok {
		t.Errorf("second commit %q missing from ancestors %v", second, got)
	}
}

func TestRevListAncestorsEmptyRefDefaultsToHEAD(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitRun(t, dir, "init", "-b", "main")
	gitRun(t, dir, "commit", "--allow-empty", "-m", "first")

	got, err := RevListAncestors(context.Background(), dir, "")
	if err != nil {
		t.Fatalf("RevListAncestors empty ref: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d ancestors, want 1", len(got))
	}
}

func TestRevListAncestorsCancelStopsCommand(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitRun(t, dir, "init", "-b", "main")
	gitRun(t, dir, "commit", "--allow-empty", "-m", "first")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := RevListAncestors(ctx, dir, "HEAD")
	if err == nil {
		t.Fatal("expected error when ctx is pre-cancelled")
	}
	if !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "context canceled") &&
		!strings.Contains(err.Error(), "killed") && !strings.Contains(err.Error(), "signal:") {
		t.Errorf("error %q should reflect cancellation", err)
	}
}
