package tui

import (
	"errors"
	"strings"
	"testing"
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
	d.MarkLoadingStat("abc1234", 1)
	d.ApplyStatLoaded(1, "abc1234", " f.txt | 3 +++\n 1 file changed, 3 insertions(+)\n")
	view := d.StatView()
	if !strings.Contains(view, "f.txt") {
		t.Errorf("loaded StatView should include filename, got %q", view)
	}
	if !strings.Contains(view, "1 file changed") {
		t.Errorf("loaded StatView should include summary, got %q", view)
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

func TestDiffStatViewEmptyTextShowsNoChanges(t *testing.T) {
	d := newDiffModel()
	d.MarkLoadingStat("abc1234", 1)
	d.ApplyStatLoaded(1, "abc1234", "   \n")
	if got := d.StatView(); got != "(no changes)" {
		t.Errorf("empty stat text should render '(no changes)', got %q", got)
	}
}

func TestDiffStatLoadedStaleReqIDIsDropped(t *testing.T) {
	d := newDiffModel()
	d.MarkLoadingStat("current", 5)
	// A stale response from reqID=2 lands after the user moved on.
	d.ApplyStatLoaded(2, "old", "stale text")
	if d.statText == "stale text" {
		t.Error("stale stat response must not overwrite statText")
	}
	if !d.loadingStat {
		t.Error("stale stat response must not clear loadingStat")
	}
}

func TestDiffStatLoadedHashMismatchIsDropped(t *testing.T) {
	d := newDiffModel()
	d.MarkLoadingStat("current", 5)
	// Same reqID but different hash — shouldn't happen in practice, but the
	// guard should still drop it instead of corrupting state.
	d.ApplyStatLoaded(5, "different", "wrong commit text")
	if d.statText == "wrong commit text" {
		t.Error("hash-mismatched response must not overwrite statText")
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
