package tui

import (
	"strings"
	"testing"
)

func TestDiffPatchLoadedFlowsThroughViewport(t *testing.T) {
	d := newDiffModel()
	d.SetPatchViewportSize(80, 5)
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

// threeFilePatch is the fixture for the file-nav tests. Three files,
// hand-counted line offsets so the test assertions stay readable.
const threeFilePatch = `diff --git a/alpha.go b/alpha.go
index 111..222 100644
--- a/alpha.go
+++ b/alpha.go
@@ -1 +1 @@
-alpha old
+alpha new
diff --git a/beta.go b/beta.go
index 333..444 100644
--- a/beta.go
+++ b/beta.go
@@ -1 +1 @@
-beta old
+beta new
diff --git a/gamma.go b/gamma.go
index 555..666 100644
--- a/gamma.go
+++ b/gamma.go
@@ -1 +1 @@
-gamma old
+gamma new
`

func TestParseFileBoundariesExtractsBSidePath(t *testing.T) {
	got := parseFileBoundaries(threeFilePatch)
	want := []fileBoundary{
		{line: 0, path: "alpha.go"},
		{line: 7, path: "beta.go"},
		{line: 14, path: "gamma.go"},
	}
	if len(got) != len(want) {
		t.Fatalf("len(boundaries) = %d, want %d (got %v)", len(got), len(want), got)
	}
	for i, b := range got {
		if b != want[i] {
			t.Errorf("boundary[%d] = %+v, want %+v", i, b, want[i])
		}
	}
}

func TestParseFileBoundariesEmpty(t *testing.T) {
	if got := parseFileBoundaries(""); got != nil {
		t.Errorf("empty patch should yield nil boundaries, got %v", got)
	}
}

// TestParseFileBoundariesStripsANSI guards against the bug that shipped
// in PR 3 first cut: gh-orbit invokes git with `color.ui=always` (see
// internal/git/show.go) so every line, headers included, arrives wrapped
// in SGR escapes (`\x1b[1mdiff --git ...\x1b[m`). HasPrefix on the raw
// line then misses every header, files comes back empty, the bracket
// keys feel dead. The parser must strip ANSI before matching.
func TestParseFileBoundariesStripsANSI(t *testing.T) {
	const colored = "\x1b[1mdiff --git a/alpha.go b/alpha.go\x1b[m\n" +
		"\x1b[1mindex 111..222 100644\x1b[m\n" +
		"\x1b[1m--- a/alpha.go\x1b[m\n" +
		"\x1b[1m+++ b/alpha.go\x1b[m\n" +
		"\x1b[36m@@ -1 +1 @@\x1b[m\n" +
		"\x1b[31m-alpha old\x1b[m\n" +
		"\x1b[32m+alpha new\x1b[m\n" +
		"\x1b[1mdiff --git a/beta.go b/beta.go\x1b[m\n"
	got := parseFileBoundaries(colored)
	if len(got) != 2 {
		t.Fatalf("len(boundaries) = %d, want 2 (got %v)", len(got), got)
	}
	if got[0].path != "alpha.go" || got[0].line != 0 {
		t.Errorf("boundary[0] = %+v, want {line:0 path:alpha.go}", got[0])
	}
	if got[1].path != "beta.go" || got[1].line != 7 {
		t.Errorf("boundary[1] = %+v, want {line:7 path:beta.go}", got[1])
	}
}

func TestParseFileDiffHeaderQuotedPath(t *testing.T) {
	const line = `diff --git "a/dir with space/x" "b/dir with space/x"`
	path, ok := parseFileDiffHeader(line)
	if !ok {
		t.Fatalf("quoted header must parse, got ok=false")
	}
	if path != "dir with space/x" {
		t.Errorf("path = %q, want %q", path, "dir with space/x")
	}
}

func TestParseFileDiffHeaderRename(t *testing.T) {
	// Rename: a-side and b-side paths differ. We always take the b-side
	// because that's the destination — the file the reviewer is reading.
	path, ok := parseFileDiffHeader("diff --git a/old.go b/new.go")
	if !ok || path != "new.go" {
		t.Errorf("rename header: path=%q ok=%v, want path=new.go ok=true", path, ok)
	}
}

func TestParseFileDiffHeaderNonHeaderRejected(t *testing.T) {
	for _, line := range []string{
		"",
		"index abc..def 100644",
		"--- a/foo",
		"+++ b/foo",
		"@@ -1 +1 @@",
		"+added line",
	} {
		if _, ok := parseFileDiffHeader(line); ok {
			t.Errorf("non-header %q must be rejected", line)
		}
	}
}

func TestDiffJumpToNextFileAdvancesActiveAndViewport(t *testing.T) {
	d := newDiffModel()
	// Viewport smaller than patch (22 lines) so SetYOffset isn't clamped.
	d.SetPatchViewportSize(80, 5)
	d.BeginPatchLoad("h", 1)
	d.ApplyPatchLoaded(1, "h", threeFilePatch)
	if d.activeFile != 0 {
		t.Fatalf("setup: activeFile = %d, want 0 after load", d.activeFile)
	}
	d.JumpToNextFile()
	if d.activeFile != 1 || d.viewport.YOffset != 7 {
		t.Errorf("after ], active=%d YOffset=%d, want 1 / 7 (beta)", d.activeFile, d.viewport.YOffset)
	}
	d.JumpToNextFile()
	if d.activeFile != 2 || d.viewport.YOffset != 14 {
		t.Errorf("after ]×2, active=%d YOffset=%d, want 2 / 14 (gamma)", d.activeFile, d.viewport.YOffset)
	}
	// At last file — } is a no-op (no wrap).
	d.JumpToNextFile()
	if d.activeFile != 2 || d.viewport.YOffset != 14 {
		t.Errorf("} at last file leaked: active=%d YOffset=%d, want stay 2 / 14", d.activeFile, d.viewport.YOffset)
	}
}

func TestPatchHeaderCarriesPathAndHunk(t *testing.T) {
	d := newDiffModel()
	d.SetPatchViewportSize(120, 10)
	d.BeginPatchLoad("h", 1)
	d.ApplyPatchLoaded(1, "h", threeFilePatch)

	if h := d.patchHeader(); !strings.Contains(h, "alpha.go") || !strings.Contains(h, "[hunk 1/3]") {
		t.Errorf("header = %q, want alpha.go + [hunk 1/3]", h)
	}
	// ] moves to beta's hunk; the header follows both the file and the hunk
	// index (the file/N-M role the bottom hint used to carry).
	d.MoveHunk(1)
	if h := d.patchHeader(); !strings.Contains(h, "beta.go") || !strings.Contains(h, "[hunk 2/3]") {
		t.Errorf("after MoveHunk, header = %q, want beta.go + [hunk 2/3]", h)
	}
}

func TestMoveHunkWalksAllHunks(t *testing.T) {
	d := newDiffModel()
	// Viewport smaller than the patch so SetYOffset isn't clamped.
	d.SetPatchViewportSize(80, 5)
	d.BeginPatchLoad("h", 1)
	d.ApplyPatchLoaded(1, "h", threeFilePatch)

	// hunks sit at lines 4 (alpha), 11 (beta), 18 (gamma) — crossing file
	// boundaries, unlike {/} which stops at each diff --git header.
	d.MoveHunk(1)
	if d.viewport.YOffset != 11 {
		t.Errorf("MoveHunk(1) YOffset = %d, want 11 (beta hunk)", d.viewport.YOffset)
	}
	// No wrap past the last hunk.
	d.MoveHunk(1)
	d.MoveHunk(1)
	if i, n := d.CurrentHunk(); i != 3 || n != 3 {
		t.Errorf("MoveHunk past end = hunk %d/%d, want 3/3", i, n)
	}
	d.MoveHunk(-2)
	if d.viewport.YOffset != 4 {
		t.Errorf("MoveHunk(-2) YOffset = %d, want 4 (alpha hunk)", d.viewport.YOffset)
	}
}

func TestDiffJumpToPrevFile(t *testing.T) {
	d := newDiffModel()
	d.SetPatchViewportSize(80, 5)
	d.BeginPatchLoad("h", 1)
	d.ApplyPatchLoaded(1, "h", threeFilePatch)
	// Walk to last file then back.
	d.JumpToNextFile()
	d.JumpToNextFile()
	if d.activeFile != 2 {
		t.Fatalf("setup: failed to reach gamma, active=%d", d.activeFile)
	}
	d.JumpToPrevFile()
	if d.activeFile != 1 || d.viewport.YOffset != 7 {
		t.Errorf("[ from gamma, active=%d YOffset=%d, want 1 / 7 (beta)", d.activeFile, d.viewport.YOffset)
	}
	d.JumpToPrevFile()
	if d.activeFile != 0 || d.viewport.YOffset != 0 {
		t.Errorf("[ from beta, active=%d YOffset=%d, want 0 / 0 (alpha)", d.activeFile, d.viewport.YOffset)
	}
	// At first file — [ is a no-op.
	d.JumpToPrevFile()
	if d.activeFile != 0 {
		t.Errorf("[ at first file leaked: active=%d, want 0", d.activeFile)
	}
}

// TestDiffJumpWorksOnPatchSmallerThanViewport guards the bug we shipped
// in PR 3 first iteration: when the patch fits the viewport entirely
// (MaxYOffset = 0), viewport.SetYOffset clamps to 0 and the bracket
// keys looked like dead keys. activeFile must still advance so the
// hint indicator updates even though the visible scroll can't move.
func TestDiffJumpWorksOnPatchSmallerThanViewport(t *testing.T) {
	d := newDiffModel()
	// Viewport (height 50) bigger than patch (22 lines) → MaxYOffset = 0.
	d.SetPatchViewportSize(80, 50)
	d.BeginPatchLoad("h", 1)
	d.ApplyPatchLoaded(1, "h", threeFilePatch)
	d.JumpToNextFile()
	if d.activeFile != 1 {
		t.Errorf("active didn't advance on small-patch ], got %d, want 1", d.activeFile)
	}
	path, idx, total := d.CurrentFile()
	if path != "beta.go" || idx != 2 || total != 3 {
		t.Errorf("CurrentFile after ] = (%q, %d, %d), want (beta.go, 2, 3)", path, idx, total)
	}
}

func TestDiffJumpEmptyPatchIsNoop(t *testing.T) {
	d := newDiffModel()
	d.SetPatchViewportSize(80, 5)
	d.BeginPatchLoad("h", 1)
	d.ApplyPatchLoaded(1, "h", "")
	d.JumpToNextFile()
	d.JumpToPrevFile()
	if d.activeFile != -1 {
		t.Errorf("empty patch active should stay -1, got %d", d.activeFile)
	}
	if got := d.viewport.YOffset; got != 0 {
		t.Errorf("empty patch jumps should be no-ops, YOffset = %d", got)
	}
}

func TestDiffScrollSyncsActiveFile(t *testing.T) {
	d := newDiffModel()
	d.SetPatchViewportSize(80, 5)
	d.BeginPatchLoad("h", 1)
	d.ApplyPatchLoaded(1, "h", threeFilePatch)
	// Simulate the user mashing j past the beta header. We can't easily
	// fake a KeyMsg without sending it via viewport.Update, so just call
	// SetYOffset directly and then invoke the sync.
	d.viewport.SetYOffset(10) // inside beta
	d.syncActiveFileFromYOffset()
	if d.activeFile != 1 {
		t.Errorf("scroll into beta did not sync active, got %d, want 1", d.activeFile)
	}
	d.viewport.SetYOffset(17) // inside gamma
	d.syncActiveFileFromYOffset()
	if d.activeFile != 2 {
		t.Errorf("scroll into gamma did not sync active, got %d, want 2", d.activeFile)
	}
	d.viewport.SetYOffset(3) // back inside alpha
	d.syncActiveFileFromYOffset()
	if d.activeFile != 0 {
		t.Errorf("scroll back to alpha did not sync active, got %d, want 0", d.activeFile)
	}
}

func TestDiffCurrentFileEmpty(t *testing.T) {
	d := newDiffModel()
	d.SetPatchViewportSize(80, 5)
	d.BeginPatchLoad("h", 1)
	d.ApplyPatchLoaded(1, "h", "")
	path, idx, total := d.CurrentFile()
	if path != "" || idx != 0 || total != 0 {
		t.Errorf("empty patch CurrentFile = (%q, %d, %d), want all zero/empty", path, idx, total)
	}
}

func TestDiffClosePatchResetsFiles(t *testing.T) {
	d := newDiffModel()
	d.SetPatchViewportSize(80, 5)
	d.BeginPatchLoad("h", 1)
	d.ApplyPatchLoaded(1, "h", threeFilePatch)
	if len(d.files) != 3 {
		t.Fatalf("setup: files len = %d, want 3", len(d.files))
	}
	d.ClosePatch()
	if d.files != nil {
		t.Errorf("ClosePatch must release files, got %v", d.files)
	}
	if d.activeFile != -1 {
		t.Errorf("ClosePatch must reset activeFile, got %d", d.activeFile)
	}
}
