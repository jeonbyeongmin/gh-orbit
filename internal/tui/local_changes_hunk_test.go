package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

func mustWriteFile(t *testing.T, dir, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

const twoHunkDiff = `diff --git a/f.txt b/f.txt
index 1111111..2222222 100644
--- a/f.txt
+++ b/f.txt
@@ -1,3 +1,4 @@
 a
+inserted-early
 b
 c
@@ -10,3 +11,3 @@
 j
-k
+K
 l
`

func TestExtractHunkPatch(t *testing.T) {
	// Hunk 0 carries the header + the first @@ block, and nothing from hunk 1.
	p0, ok := extractHunkPatch(twoHunkDiff, 0)
	if !ok {
		t.Fatal("hunk 0: extract returned false")
	}
	for _, want := range []string{"diff --git a/f.txt b/f.txt", "--- a/f.txt", "+++ b/f.txt", "@@ -1,3 +1,4 @@", "+inserted-early"} {
		if !strings.Contains(p0, want) {
			t.Errorf("hunk 0 patch missing %q:\n%s", want, p0)
		}
	}
	if strings.Contains(p0, "+K") {
		t.Errorf("hunk 0 patch leaked hunk 1 content:\n%s", p0)
	}
	if !strings.HasSuffix(p0, "\n") {
		t.Error("hunk 0 patch must end with a newline for git apply")
	}

	// Hunk 1 carries the header + the second @@ block, and nothing from hunk 0.
	p1, ok := extractHunkPatch(twoHunkDiff, 1)
	if !ok {
		t.Fatal("hunk 1: extract returned false")
	}
	if !strings.Contains(p1, "@@ -10,3 +11,3 @@") || !strings.Contains(p1, "+K") {
		t.Errorf("hunk 1 patch missing its hunk:\n%s", p1)
	}
	if strings.Contains(p1, "inserted-early") {
		t.Errorf("hunk 1 patch leaked hunk 0 content:\n%s", p1)
	}

	// Out of range and no-hunk diffs return false.
	if _, ok := extractHunkPatch(twoHunkDiff, 2); ok {
		t.Error("hunk index 2 should be out of range")
	}
	if _, ok := extractHunkPatch("diff --git a/x b/x\n--- a/x\n+++ b/x\n", 0); ok {
		t.Error("a diff with no @@ hunk should return false")
	}
}

func TestParseHunkStarts(t *testing.T) {
	starts := parseHunkStarts(twoHunkDiff)
	if len(starts) != 2 {
		t.Fatalf("plain diff: got %d hunk starts, want 2", len(starts))
	}
	if starts[0] != 4 || starts[1] != 9 {
		t.Errorf("plain diff hunk starts = %v, want [4 9]", starts)
	}
	// ANSI-colored headers (git's color.ui=always) must still be detected: a
	// header line starts with an escape, not "@@".
	colored := "\x1b[1mdiff --git a/x b/x\x1b[m\n\x1b[36m@@ -1 +1 @@\x1b[m\n-a\n+b\n"
	cs := parseHunkStarts(colored)
	if len(cs) != 1 || cs[0] != 1 {
		t.Errorf("colored diff hunk starts = %v, want [1]", cs)
	}
}

func TestLocalChangesMoveHunkClamps(t *testing.T) {
	var m localChangesModel
	m.SetSize(40, 20, 40, 20)
	m.focused = paneLCDiff
	m.diffReqID = 1
	m.ApplyDiffLoaded(1, twoHunkDiff)

	if _, ok := m.CurrentHunk(); !ok {
		t.Fatal("CurrentHunk should be set after a 2-hunk diff loads")
	}
	if m.hunkCursor != 0 {
		t.Fatalf("load cursor = %d, want 0", m.hunkCursor)
	}
	m.MoveHunk(-1) // clamp at top
	if m.hunkCursor != 0 {
		t.Errorf("MoveHunk(-1) at top = %d, want 0", m.hunkCursor)
	}
	m.MoveHunk(5) // clamp at bottom (2 hunks → max index 1)
	if m.hunkCursor != 1 {
		t.Errorf("MoveHunk(5) = %d, want 1 (clamped)", m.hunkCursor)
	}

	// A rename-only / no-hunk diff yields no current hunk.
	m.ApplyDiffLoaded(1, "diff --git a/x b/y\nsimilarity index 100%\n")
	if _, ok := m.CurrentHunk(); ok {
		t.Error("no-hunk diff should report no current hunk")
	}
}

// TestApplyMsgRoutedClearsBusy guards the routing bug: localChangesApply*
// messages must reach updateLocalChangesMsg. They were missing from update()'s
// type-switch, so the apply result fell through to `return m, nil` — git
// staged the hunk but the UI's "stage hunk…" spinner span forever.
func TestApplyMsgRoutedClearsBusy(t *testing.T) {
	m := initSized(t)
	m.mode = viewModeLocalChanges

	m.setBusyStatus("stage hunk in f.txt…")
	if !m.statusIsBusy() {
		t.Fatal("precondition: setBusyStatus should mark busy")
	}
	updated, cmd := m.Update(localChangesApplySucceededMsg{path: "f.txt", staged: false})
	m = updated.(Model)
	if m.statusIsBusy() {
		t.Error("apply success not routed — busy status stuck (dispatcher dropped the msg)")
	}
	if m.status != "staged hunk in f.txt" {
		t.Errorf("status = %q, want 'staged hunk in f.txt'", m.status)
	}
	if cmd == nil {
		t.Error("apply success should kick a status reload cmd")
	}

	// Failure routes too: busy clears, error surfaces.
	m.setBusyStatus("stage hunk in f.txt…")
	updated, _ = m.Update(localChangesApplyFailedMsg{path: "f.txt", err: errHunkOutOfRange})
	m = updated.(Model)
	if m.statusIsBusy() {
		t.Error("apply failure not routed — busy status stuck")
	}
}

// TestSelectByPathPrefersSide guards the cursor-jump bug: when a file sits in
// both Staged and Unstaged at once (per-hunk staging), SelectByPath must land
// on the requested side, not just the first match. classifyStatus appends the
// staged entry first, so the old "first match" logic always jumped to Staged.
func TestSelectByPathPrefersSide(t *testing.T) {
	var m localChangesModel
	m.ApplyStatusLoaded([]git.StatusEntry{
		{Path: "f.txt", IndexState: 'M', WorktreeState: 'M'},
	})
	if len(m.entries) != 2 {
		t.Fatalf("want 2 entries (staged + unstaged), got %d", len(m.entries))
	}

	if !m.SelectByPath("f.txt", false) {
		t.Fatal("SelectByPath(false) found nothing")
	}
	if e, _ := m.CurrentEntry(); e.Section != sectionUnstaged {
		t.Errorf("preferStaged=false landed on section %d, want unstaged", e.Section)
	}

	if !m.SelectByPath("f.txt", true) {
		t.Fatal("SelectByPath(true) found nothing")
	}
	if e, _ := m.CurrentEntry(); e.Section != sectionStaged {
		t.Errorf("preferStaged=true landed on section %d, want staged", e.Section)
	}
}

// TestBeginDiffLoadClearsHunks guards the stale-hunk window: when a new diff
// load begins (tree cursor moved to another file), the previous file's hunk
// state must be dropped so a `space` pressed before the new diff lands reports
// "no hunk" instead of staging a stale-indexed hunk of the wrong file.
func TestBeginDiffLoadClearsHunks(t *testing.T) {
	var m localChangesModel
	m.SetSize(40, 20, 40, 20)
	m.focused = paneLCDiff
	m.diffReqID = 1
	m.ApplyDiffLoaded(1, twoHunkDiff)
	m.MoveHunk(1)
	if _, ok := m.CurrentHunk(); !ok {
		t.Fatal("precondition: a hunk should be selected")
	}

	m.BeginDiffLoad(2) // tree cursor moved → new load in flight
	if _, ok := m.CurrentHunk(); ok {
		t.Error("hunk state must be cleared during the load window")
	}
	if m.hunkCursor != 0 || m.hunkStarts != nil {
		t.Errorf("BeginDiffLoad left stale hunk state: cursor=%d starts=%v", m.hunkCursor, m.hunkStarts)
	}
}

// gitOut runs git and returns trimmed stdout, failing the test on error.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestStageHunkEndToEnd drives the whole per-hunk pipeline against real git:
// DiffFileRaw → extractHunkPatch → ApplyCached. Staging only hunk 0 must put
// hunk 0 in the index and leave hunk 1 unstaged; reversing it must clear the
// index again. This is the load-bearing proof the built patch is applyable.
func TestStageHunkEndToEnd(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")

	// 20-line baseline so two edits land in two separate hunks.
	var base strings.Builder
	for i := 1; i <= 20; i++ {
		base.WriteString("line")
		base.WriteByte(byte('0' + i%10))
		base.WriteByte('\n')
	}
	mustWriteFile(t, dir, "f.txt", base.String())
	runGit(t, dir, "add", "f.txt")
	runGit(t, dir, "commit", "-m", "base")

	// Edit line 2 (hunk 0) and line 18 (hunk 1).
	edited := strings.Split(base.String(), "\n")
	edited[1] = "EARLY-EDIT"
	edited[17] = "LATE-EDIT"
	mustWriteFile(t, dir, "f.txt", strings.Join(edited, "\n"))

	ctx := context.Background()
	raw, err := git.DiffFileRaw(ctx, dir, "f.txt", false)
	if err != nil {
		t.Fatalf("DiffFileRaw: %v", err)
	}
	if n := len(parseHunkStarts(raw)); n != 2 {
		t.Fatalf("expected 2 hunks in the working diff, got %d:\n%s", n, raw)
	}

	patch, ok := extractHunkPatch(raw, 0)
	if !ok {
		t.Fatal("extractHunkPatch(raw, 0) returned false")
	}
	if err := git.ApplyCached(ctx, dir, patch, false); err != nil {
		t.Fatalf("ApplyCached(stage hunk 0): %v", err)
	}

	staged := gitOut(t, dir, "diff", "--cached")
	if !strings.Contains(staged, "EARLY-EDIT") {
		t.Errorf("staged diff should contain hunk 0 (EARLY-EDIT):\n%s", staged)
	}
	if strings.Contains(staged, "LATE-EDIT") {
		t.Errorf("staged diff must NOT contain hunk 1 (LATE-EDIT):\n%s", staged)
	}
	unstaged := gitOut(t, dir, "diff")
	if !strings.Contains(unstaged, "LATE-EDIT") {
		t.Errorf("unstaged diff should still contain hunk 1 (LATE-EDIT):\n%s", unstaged)
	}

	// Reverse the same patch → index clean again.
	if err := git.ApplyCached(ctx, dir, patch, true); err != nil {
		t.Fatalf("ApplyCached(reverse): %v", err)
	}
	if after := gitOut(t, dir, "diff", "--cached"); after != "" {
		t.Errorf("after reverse, staged diff should be empty:\n%s", after)
	}
}
