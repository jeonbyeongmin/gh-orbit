package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

func TestDiffStatViewEmpty(t *testing.T) {
	d := newDiffModel()
	if got := d.StatView(); got != "(no commit selected)" {
		t.Errorf("empty StatView = %q, want %q", got, "(no commit selected)")
	}
}

func TestDiffStatViewLoading(t *testing.T) {
	d := newDiffModel()
	d.MarkLoadingStat("abc1234", 1)
	if got := d.StatView(); got != "loading…" {
		t.Errorf("loading StatView = %q, want loading…", got)
	}
}

func TestDiffStatViewLoaded(t *testing.T) {
	d := newDiffModel()
	d.SetSize(40, 10)
	d.MarkLoadingStat("abc1234", 1)
	d.ApplyStatLoaded(1, "abc1234", []git.FileStat{
		{Path: "f.txt", Insertions: 3, Deletions: 0},
	})
	view := d.StatView()
	if !strings.Contains(view, "f.txt") {
		t.Errorf("loaded StatView should include filename, got %q", view)
	}
	if !strings.Contains(view, "+3") {
		t.Errorf("loaded StatView should include insertion count, got %q", view)
	}
	if !strings.Contains(view, "-0") {
		t.Errorf("loaded StatView should include deletion count, got %q", view)
	}
	if !strings.Contains(view, "1 files") {
		t.Errorf("loaded StatView should include summary line, got %q", view)
	}
}

func TestDiffStatViewBinary(t *testing.T) {
	d := newDiffModel()
	d.SetSize(40, 10)
	d.MarkLoadingStat("abc1234", 1)
	d.ApplyStatLoaded(1, "abc1234", []git.FileStat{
		{Path: "logo.png", Insertions: -1, Deletions: -1},
	})
	view := d.StatView()
	if !strings.Contains(view, "Bin") {
		t.Errorf("binary file should render as 'Bin', got %q", view)
	}
	if !strings.Contains(view, "logo.png") {
		t.Errorf("binary entry should still show path, got %q", view)
	}
}

func TestDiffStatViewError(t *testing.T) {
	d := newDiffModel()
	d.MarkLoadingStat("abc1234", 1)
	d.ApplyStatFailed(1, "abc1234", errors.New("git show: fatal: bad object\nmore lines"))
	view := d.StatView()
	if !strings.HasPrefix(view, "error:") {
		t.Errorf("error StatView should start with 'error:', got %q", view)
	}
	if strings.Contains(view, "more lines") {
		t.Errorf("error StatView should keep only the first line, got %q", view)
	}
}

func TestDiffStatViewEmptyFilesShowsNoChanges(t *testing.T) {
	d := newDiffModel()
	d.MarkLoadingStat("abc1234", 1)
	d.ApplyStatLoaded(1, "abc1234", nil)
	if got := d.StatView(); got != "(no changes)" {
		t.Errorf("empty stat should render '(no changes)', got %q", got)
	}
}

func TestDiffStatLoadedStaleReqIDIsDropped(t *testing.T) {
	d := newDiffModel()
	d.MarkLoadingStat("current", 5)
	d.ApplyStatLoaded(2, "old", []git.FileStat{{Path: "stale.txt", Insertions: 1}})
	if d.statLoaded {
		t.Error("stale stat response must not flip statLoaded")
	}
	if !d.loadingStat {
		t.Error("stale stat response must not clear loadingStat")
	}
}

func TestDiffStatLoadedHashMismatchIsDropped(t *testing.T) {
	d := newDiffModel()
	d.MarkLoadingStat("current", 5)
	d.ApplyStatLoaded(5, "different", []git.FileStat{{Path: "wrong.txt", Insertions: 9}})
	if d.statLoaded {
		t.Error("hash-mismatched response must not flip statLoaded")
	}
}

func TestDiffPatchLoadedFlowsThroughViewport(t *testing.T) {
	d := newDiffModel()
	d.SetPatchViewportSize(80, 20)
	d.BeginPatchLoad("abc1234", 1)
	d.ApplyPatchLoaded(1, "abc1234", "diff --git a/f b/f\n@@ -0,0 +1 @@\n+hello\n")
	if d.loadingPatch {
		t.Error("loadingPatch should clear after ApplyPatchLoaded")
	}
	if !strings.Contains(d.PatchView(), "hello") {
		t.Errorf("patch view should include the inserted line, got %q", d.PatchView())
	}
}

func TestDiffSetPatchViewportSizePropagates(t *testing.T) {
	d := newDiffModel()
	d.SetPatchViewportSize(120, 40)
	if d.viewport.Width != 120 || d.viewport.Height != 40 {
		t.Errorf("viewport dims = %dx%d, want 120x40", d.viewport.Width, d.viewport.Height)
	}
}

func TestTruncatePathDropsLeadingDirsForNarrow(t *testing.T) {
	got := truncatePath("internal/tui/diff.go", 10)
	if got == "internal/tui/diff.go" {
		t.Errorf("expected truncation when path > width, got full path")
	}
	if !strings.HasPrefix(got, "…") {
		t.Errorf("truncated path should start with '…', got %q", got)
	}
	if !strings.HasSuffix(got, "diff.go") {
		t.Errorf("truncation should preserve filename, got %q", got)
	}
}

func TestTruncatePathLeavesShortPathsAlone(t *testing.T) {
	if got := truncatePath("f.txt", 40); got != "f.txt" {
		t.Errorf("short path should pass through, got %q", got)
	}
}
