package tui

import (
	"strings"
	"testing"
)

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

func TestDiffPatchLoadedStaleReqIDIsDropped(t *testing.T) {
	d := newDiffModel()
	d.BeginPatchLoad("current", 5)
	d.ApplyPatchLoaded(2, "old", "stale patch text")
	if !d.loadingPatch {
		t.Error("stale patch response must not clear loadingPatch")
	}
	if strings.Contains(d.PatchView(), "stale") {
		t.Errorf("stale patch must not flow into viewport, got %q", d.PatchView())
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
