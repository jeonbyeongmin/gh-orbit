package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

func TestCommitItemFilterValueIsSubject(t *testing.T) {
	ci := commitItem{c: git.Commit{Subject: "fix: parse refs"}}
	if got := ci.FilterValue(); got != "fix: parse refs" {
		t.Errorf("FilterValue = %q, want %q", got, "fix: parse refs")
	}
}

func TestRenderCommitLineTruncatesLongSubject(t *testing.T) {
	c := git.Commit{
		Hash:       "abcdef1234567",
		Subject:    "이것은 너비 검증을 위해 일부러 길게 적은 한국어 제목입니다",
		AuthorTime: time.Now(),
	}
	line := renderCommitLine(c, "", 0, 40, false)
	if !strings.Contains(line, "…") {
		t.Errorf("expected ellipsis when subject overflows, got %q", line)
	}
}

func TestRenderCommitLineHidesSubjectWhenTooNarrow(t *testing.T) {
	c := git.Commit{
		Hash:       "abcdef1234567",
		Subject:    "should-not-appear",
		AuthorTime: time.Now(),
	}
	// Width less than cursor(2)+hash(7)+space(1)+time(6)+space(1) = 17.
	line := renderCommitLine(c, "", 0, 10, false)
	if strings.Contains(line, "should-not-appear") {
		t.Errorf("subject should be hidden at narrow width, got %q", line)
	}
	// Hash should still be visible (truncated to short).
	if !strings.Contains(line, "abcdef1") {
		t.Errorf("hash should remain visible, got %q", line)
	}
}

func TestRenderCommitLineSelectedHasCursor(t *testing.T) {
	c := git.Commit{
		Hash:       "abcdef1234567",
		Subject:    "selected commit",
		AuthorTime: time.Now(),
	}
	line := renderCommitLine(c, "", 0, 80, true)
	if !strings.Contains(line, "›") {
		t.Errorf("selected line should contain cursor marker, got %q", line)
	}
	unselected := renderCommitLine(c, "", 0, 80, false)
	if strings.Contains(unselected, "›") {
		t.Errorf("unselected line should not contain cursor marker, got %q", unselected)
	}
}

func TestRenderCommitLineGraphPlacedBeforeHash(t *testing.T) {
	c := git.Commit{
		Hash:       "abcdef1234567",
		Subject:    "graph layout",
		AuthorTime: time.Now(),
	}
	line := renderCommitLine(c, "* ", 2, 80, false)
	hashIdx := strings.Index(line, "abcdef1")
	starIdx := strings.Index(line, "*")
	if hashIdx < 0 || starIdx < 0 {
		t.Fatalf("expected both star and hash in %q", line)
	}
	if starIdx >= hashIdx {
		t.Errorf("graph segment should appear before hash, got %q", line)
	}
}

func TestRenderCommitLinePadsShortGraphPrefix(t *testing.T) {
	c := git.Commit{
		Hash:       "abcdef1234567",
		Subject:    "padded",
		AuthorTime: time.Now(),
	}
	// graphPrefix "*" is 1 column wide but graphWidth=4 — expect 3 padding
	// columns between '*' and the hash so columns line up across rows.
	line := renderCommitLine(c, "*", 4, 80, false)
	starIdx := strings.Index(line, "*")
	hashIdx := strings.Index(line, "abcdef1")
	if starIdx < 0 || hashIdx < 0 {
		t.Fatalf("expected star and hash in %q", line)
	}
	between := line[starIdx+1 : hashIdx]
	// `between` may include ANSI reset codes from lipgloss; we only care
	// that the rendered visual width is 3 spaces, so check via runewidth on
	// the stripped form.
	stripped := stripANSI(between)
	if stripped != "   " {
		t.Errorf("expected 3 spaces between '*' and hash, got %q (raw %q)", stripped, between)
	}
}

func TestRenderCommitLineGraphTruncatedAtNarrowWidth(t *testing.T) {
	c := git.Commit{
		Hash:       "abcdef1234567",
		Subject:    "narrow",
		AuthorTime: time.Now(),
	}
	// width 10, cursor 2 + hash 7 = 9 → at most 1 column for graph.
	line := renderCommitLine(c, "| | * ", 6, 10, false)
	if !strings.Contains(line, "abcdef1") {
		t.Errorf("hash must remain visible even when graph is wider than budget, got %q", line)
	}
}

// stripANSI removes the most common SGR escape sequences so width assertions
// don't trip on lipgloss styling output.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			if j < len(s) {
				i = j
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func TestGraphModelInitialView(t *testing.T) {
	g := newGraphModel()
	if got := g.View(); got != "loading…" {
		t.Errorf("initial view = %q, want %q", got, "loading…")
	}
}

func TestGraphModelHandlesLoadFailure(t *testing.T) {
	g := newGraphModel()
	g, _ = g.Update(commitsLoadFailedMsg{err: errSentinel})
	view := g.View()
	if !strings.Contains(view, "load error") {
		t.Errorf("error view should mention load error, got %q", view)
	}
}

func TestGraphModelResetForReloadReturnsToLoading(t *testing.T) {
	g := newGraphModel()
	g.SetSize(40, 10)
	g, _ = g.Update(commitsLoadedMsg{rows: []graphRow{
		{commit: git.Commit{Hash: "abc1234", Subject: "first", AuthorTime: time.Now()}, graphPrefix: "* "},
	}})
	if !g.loaded {
		t.Fatalf("graph should be loaded after commitsLoadedMsg")
	}
	if g.graphWidth != 2 {
		t.Errorf("graphWidth = %d, want 2", g.graphWidth)
	}
	g.ResetForReload()
	if g.loaded {
		t.Errorf("ResetForReload should clear loaded flag")
	}
	if g.graphWidth != 0 {
		t.Errorf("ResetForReload should clear graphWidth, got %d", g.graphWidth)
	}
	if g.delegate.graphWidth != 0 {
		t.Errorf("ResetForReload should clear delegate.graphWidth, got %d", g.delegate.graphWidth)
	}
	if got := g.View(); got != "loading…" {
		t.Errorf("after reset, View = %q, want %q", got, "loading…")
	}
}

func TestGraphModelComputesGraphWidthFromLongestPrefix(t *testing.T) {
	g := newGraphModel()
	g.SetSize(80, 10)
	now := time.Now()
	g, _ = g.Update(commitsLoadedMsg{rows: []graphRow{
		{commit: git.Commit{Hash: "a", Subject: "s1", AuthorTime: now}, graphPrefix: "* "},
		{commit: git.Commit{Hash: "b", Subject: "s2", AuthorTime: now}, graphPrefix: "| | * "},
		{commit: git.Commit{Hash: "c", Subject: "s3", AuthorTime: now}, graphPrefix: "|/ "},
	}})
	if g.graphWidth != 6 {
		t.Errorf("graphWidth = %d, want 6 (width of '| | * ')", g.graphWidth)
	}
	if g.delegate.graphWidth != 6 {
		t.Errorf("delegate.graphWidth = %d, want 6 (must match graphModel.graphWidth)", g.delegate.graphWidth)
	}
}

func TestGraphModelGraphWidthZeroWhenNoPrefix(t *testing.T) {
	g := newGraphModel()
	g.SetSize(80, 10)
	g, _ = g.Update(commitsLoadedMsg{rows: []graphRow{
		{commit: git.Commit{Hash: "a", Subject: "s", AuthorTime: time.Now()}},
	}})
	if g.graphWidth != 0 {
		t.Errorf("graphWidth = %d, want 0", g.graphWidth)
	}
}

type sentinelErr struct{}

func (sentinelErr) Error() string { return "sentinel" }

var errSentinel = sentinelErr{}
