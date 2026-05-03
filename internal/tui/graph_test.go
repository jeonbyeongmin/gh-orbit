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
	line := renderCommitLine(c, 40, false)
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
	// Width less than prefix(2)+hash(7)+space(1)+time(6)+space(1) = 17.
	line := renderCommitLine(c, 10, false)
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
	line := renderCommitLine(c, 80, true)
	if !strings.Contains(line, "›") {
		t.Errorf("selected line should contain cursor marker, got %q", line)
	}
	unselected := renderCommitLine(c, 80, false)
	if strings.Contains(unselected, "›") {
		t.Errorf("unselected line should not contain cursor marker, got %q", unselected)
	}
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
