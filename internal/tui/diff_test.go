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

func TestDiffJumpToNextFile(t *testing.T) {
	d := newDiffModel()
	d.SetPatchViewportSize(80, 5)
	d.BeginPatchLoad("h", 1)
	d.ApplyPatchLoaded(1, "h", threeFilePatch)
	// Start at top (line 0 = alpha header).
	d.JumpToNextFile()
	if got := d.viewport.YOffset; got != 7 {
		t.Errorf("after first ], YOffset = %d, want 7 (beta header)", got)
	}
	d.JumpToNextFile()
	if got := d.viewport.YOffset; got != 14 {
		t.Errorf("after second ], YOffset = %d, want 14 (gamma header)", got)
	}
	// At last file — ] is a no-op (no wrap).
	d.JumpToNextFile()
	if got := d.viewport.YOffset; got != 14 {
		t.Errorf("after ] on last file, YOffset = %d, want 14 (stay put)", got)
	}
}

func TestDiffJumpToPrevFile(t *testing.T) {
	d := newDiffModel()
	d.SetPatchViewportSize(80, 5)
	d.BeginPatchLoad("h", 1)
	d.ApplyPatchLoaded(1, "h", threeFilePatch)
	// Park inside file 3 (line 16, two lines past the gamma header).
	d.viewport.SetYOffset(16)
	d.JumpToPrevFile()
	if got := d.viewport.YOffset; got != 14 {
		t.Errorf("[ from inside gamma should land on gamma header, got %d, want 14", got)
	}
	d.JumpToPrevFile()
	if got := d.viewport.YOffset; got != 7 {
		t.Errorf("[ #2 should land on beta header, got %d, want 7", got)
	}
	d.JumpToPrevFile()
	if got := d.viewport.YOffset; got != 0 {
		t.Errorf("[ #3 should land on alpha header, got %d, want 0", got)
	}
	// At first file — [ is a no-op.
	d.JumpToPrevFile()
	if got := d.viewport.YOffset; got != 0 {
		t.Errorf("[ at first file, YOffset = %d, want 0 (stay put)", got)
	}
}

func TestDiffJumpEmptyPatchIsNoop(t *testing.T) {
	d := newDiffModel()
	d.SetPatchViewportSize(80, 5)
	d.BeginPatchLoad("h", 1)
	d.ApplyPatchLoaded(1, "h", "")
	d.JumpToNextFile()
	d.JumpToPrevFile()
	if got := d.viewport.YOffset; got != 0 {
		t.Errorf("empty patch jumps should be no-ops, YOffset = %d", got)
	}
}

func TestDiffCurrentFileTracksYOffset(t *testing.T) {
	d := newDiffModel()
	d.SetPatchViewportSize(80, 5)
	d.BeginPatchLoad("h", 1)
	d.ApplyPatchLoaded(1, "h", threeFilePatch)

	cases := []struct {
		yOffset   int
		wantPath  string
		wantIndex int
		wantTotal int
	}{
		{0, "alpha.go", 1, 3},
		{3, "alpha.go", 1, 3}, // inside alpha
		{7, "beta.go", 2, 3},  // on beta header
		{10, "beta.go", 2, 3}, // inside beta
		{14, "gamma.go", 3, 3},
		{20, "gamma.go", 3, 3}, // past last header — still last file
	}
	for _, tc := range cases {
		d.viewport.SetYOffset(tc.yOffset)
		path, idx, total := d.CurrentFile()
		if path != tc.wantPath || idx != tc.wantIndex || total != tc.wantTotal {
			t.Errorf("YOffset=%d → (%q, %d, %d), want (%q, %d, %d)",
				tc.yOffset, path, idx, total, tc.wantPath, tc.wantIndex, tc.wantTotal)
		}
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
}
