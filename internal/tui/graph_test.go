package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

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
	line := renderCommitLine(c, nil, "", 0, 0, 40, false, false)
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
	// Width less than cursor(2)+time(8)+space(1)+subject(1) = 12.
	line := renderCommitLine(c, nil, "", 0, 0, 10, false, false)
	if strings.Contains(line, "should-not-appear") {
		t.Errorf("subject should be hidden at narrow width, got %q", line)
	}
	// Time should still be visible (right-anchored tail).
	if !strings.Contains(line, "just now") {
		t.Errorf("time should remain visible, got %q", line)
	}
}

func TestRenderCommitLineSelectedHasCursor(t *testing.T) {
	c := git.Commit{
		Hash:       "abcdef1234567",
		Subject:    "selected commit",
		AuthorTime: time.Now(),
	}
	line := renderCommitLine(c, nil, "", 0, 0, 80, true, false)
	if !strings.Contains(line, "›") {
		t.Errorf("selected line should contain cursor marker, got %q", line)
	}
	unselected := renderCommitLine(c, nil, "", 0, 0, 80, false, false)
	if strings.Contains(unselected, "›") {
		t.Errorf("unselected line should not contain cursor marker, got %q", unselected)
	}
}

func TestRenderCommitLineOrderingGraphSubjectTime(t *testing.T) {
	c := git.Commit{
		Hash:       "abcdef1234567",
		Subject:    "graph layout",
		AuthorTime: time.Now(),
	}
	line := renderCommitLine(c, nil, "* ", 2, 2, 80, false, false)
	stripped := ansi.Strip(line)
	starIdx := strings.Index(stripped, "*")
	subjectIdx := strings.Index(stripped, "graph layout")
	relIdx := strings.Index(stripped, "just now")
	if starIdx < 0 || subjectIdx < 0 || relIdx < 0 {
		t.Fatalf("expected star, subject, and rel in %q", stripped)
	}
	// Layout: graph < message (subject) < rel-time. Time is right-anchored;
	// subject sits in the message column and absorbs truncation when the
	// row is narrow.
	if starIdx >= subjectIdx || subjectIdx >= relIdx {
		t.Errorf("expected order graph < subject < rel, got star=%d subject=%d rel=%d in %q",
			starIdx, subjectIdx, relIdx, stripped)
	}
}

func TestRenderCommitLineTimeAnchoredToRightEdge(t *testing.T) {
	c := git.Commit{
		Hash:       "abcdef1234567",
		Subject:    "right edge",
		AuthorTime: time.Now(),
	}
	line := renderCommitLine(c, nil, "* ", 2, 2, 80, false, false)
	stripped := ansi.Strip(line)
	// The visible width must still equal the requested width.
	if w := len(stripped); w != 80 {
		t.Errorf("rendered width = %d, want 80 (full row)", w)
	}
	// Time sits at the right edge: width - timeColWidth. "just now" fills
	// the whole timeColWidth=8 column, so it starts at width - 8 = 72.
	relIdx := strings.Index(stripped, "just now")
	wantRelIdx := 80 - timeColWidth
	if relIdx != wantRelIdx {
		t.Errorf("time starts at %d, want %d (anchored to right edge)", relIdx, wantRelIdx)
	}
	// Subject sits in the message column, between the graph and the time.
	subjectIdx := strings.Index(stripped, "right edge")
	wantSubjectMin := 2 + 2 // cursor + graph
	if subjectIdx < wantSubjectMin || subjectIdx >= relIdx {
		t.Errorf("subject not in the message column; subjectIdx=%d relIdx=%d", subjectIdx, relIdx)
	}
}

func TestRenderCommitLinePadsShortGraphPrefix(t *testing.T) {
	c := git.Commit{
		Hash:       "abcdef1234567",
		Subject:    "padded",
		AuthorTime: time.Now(),
	}
	// graphPrefix "*" is 1 column wide but graphColWidth=4 — the cell must
	// be left-aligned and padded out to the full 4-column width so columns
	// line up across rows.
	line := renderCommitLine(c, nil, "*", 1, 4, 80, false, false)
	stripped := ansi.Strip(line)
	// cursor 2 + graph 4 = 6.
	if got := stripped[2:6]; got != "*   " {
		t.Errorf("graph cell = %q, want %q (left-aligned, right-padded)", got, "*   ")
	}
}

func TestRenderCommitLineNoChipAreaWhenNoRefs(t *testing.T) {
	c := git.Commit{
		Hash:       "abcdef1234567",
		Subject:    "no refs",
		AuthorTime: time.Now(),
	}
	line := renderCommitLine(c, nil, "* ", 2, 2, 80, false, false)
	stripped := ansi.Strip(line)
	// With no refs, no chip cluster appears. The substring " no refs" should
	// follow the time column directly with one separator space.
	if !strings.Contains(stripped, "no refs") {
		t.Fatalf("subject missing from %q", stripped)
	}
}

func TestRenderCommitLineWithLocalChip(t *testing.T) {
	c := git.Commit{
		Hash:       "abcdef1234567",
		Subject:    "with chip",
		AuthorTime: time.Now(),
		RefNames:   []string{"main"},
	}
	line := renderCommitLine(c, nil, "* ", 2, 2, 80, false, false)
	stripped := ansi.Strip(line)
	// Chip "main" attaches to the front of the subject in the message
	// column — both sit between the graph and the time.
	mainIdx := strings.Index(stripped, "main")
	subjectIdx := strings.Index(stripped, "with chip")
	relIdx := strings.Index(stripped, "just now")
	if mainIdx < 0 || subjectIdx < 0 || relIdx < 0 {
		t.Fatalf("expected chip, subject, time in %q", stripped)
	}
	if mainIdx >= subjectIdx || subjectIdx >= relIdx {
		t.Errorf("expected order chip < subject < rel; got chip=%d subject=%d rel=%d in %q",
			mainIdx, subjectIdx, relIdx, stripped)
	}
}

func TestRenderCommitLineWithPairedChip(t *testing.T) {
	c := git.Commit{
		Hash:       "abcdef1234567",
		Subject:    "paired",
		AuthorTime: time.Now(),
		RefNames:   []string{"HEAD -> main", "origin/main"},
	}
	line := renderCommitLine(c, nil, "* ", 2, 2, 80, false, false)
	stripped := ansi.Strip(line)
	if strings.Count(stripped, "main") != 1 {
		t.Errorf("paired chip should render 'main' exactly once, got %q", stripped)
	}
	if strings.Contains(stripped, "↑") {
		t.Errorf("paired chip should not carry stale ↑ marker, got %q", stripped)
	}
	if strings.Count(stripped, "☁") != 1 {
		t.Errorf("paired chip should carry exactly one '☁' sync prefix, got %q", stripped)
	}
	if strings.Contains(stripped, "HEAD") {
		t.Errorf("HEAD chip removed (boundary signaled by graph dim now), got %q", stripped)
	}
}

func TestRenderCommitLineWithDetachedHead(t *testing.T) {
	c := git.Commit{
		Hash:       "abcdef1234567",
		Subject:    "detached",
		AuthorTime: time.Now(),
		RefNames:   []string{"HEAD"},
	}
	line := renderCommitLine(c, nil, "* ", 2, 2, 80, false, false)
	stripped := ansi.Strip(line)
	if strings.Contains(stripped, "HEAD") {
		t.Errorf("detached HEAD must NOT render a chip — boundary lives on the graph dim pass, got %q", stripped)
	}
	if !strings.Contains(stripped, "detached") {
		t.Errorf("subject must still render even when only HEAD token is present, got %q", stripped)
	}
}

func TestRenderCommitLineWithTagChipStripsPrefix(t *testing.T) {
	c := git.Commit{
		Hash:       "abcdef1234567",
		Subject:    "tag commit",
		AuthorTime: time.Now(),
		RefNames:   []string{"tag: v0.0.1"},
	}
	line := renderCommitLine(c, nil, "* ", 2, 2, 80, false, false)
	stripped := ansi.Strip(line)
	if !strings.Contains(stripped, "v0.0.1") {
		t.Errorf("tag chip should display version, got %q", stripped)
	}
	if strings.Contains(stripped, "tag:") {
		t.Errorf("tag prefix should not appear in chip text, got %q", stripped)
	}
}

func TestRenderCommitLineChipOverflowShowsPlusN(t *testing.T) {
	c := git.Commit{
		Hash:       "abcdef1234567",
		Subject:    "many",
		AuthorTime: time.Now(),
		RefNames:   []string{"a", "b", "c", "d", "e"},
	}
	line := renderCommitLine(c, nil, "* ", 2, 2, 100, false, false)
	stripped := ansi.Strip(line)
	if !strings.Contains(stripped, "+3") {
		t.Errorf("overflow indicator '+3' missing from %q", stripped)
	}
}

func TestRenderCommitLineChipDroppedWhenSubjectWouldStarve(t *testing.T) {
	c := git.Commit{
		Hash:       "abcdef1234567",
		Subject:    "x",
		AuthorTime: time.Now(),
		// Pile up a long ref name so the chip cluster can't coexist with
		// even a 1-cell subject at narrow widths.
		RefNames: []string{"this-is-a-very-long-branch-name-that-cannot-fit"},
	}
	// width = cursor 2 + graph 2 + subject 2 + space 1 + time 8 = 15
	line := renderCommitLine(c, nil, "* ", 2, 2, 15, false, false)
	stripped := ansi.Strip(line)
	if strings.Contains(stripped, "this-is-a-very-long") {
		t.Errorf("chip should be dropped when subject can't fit alongside it, got %q", stripped)
	}
	if !strings.Contains(stripped, "just now") {
		t.Errorf("time must remain visible, got %q", stripped)
	}
}

func TestRenderCommitLineSelectedRecolorsChipBackground(t *testing.T) {
	c := git.Commit{
		Hash:       "abcdef1234567",
		Subject:    "sel",
		AuthorTime: time.Now(),
		RefNames:   []string{"main"},
	}
	unselected := renderCommitLine(c, nil, "* ", 2, 2, 80, false, false)
	selected := renderCommitLine(c, nil, "* ", 2, 2, 80, true, false)
	if unselected == selected {
		t.Fatalf("selected line should differ from unselected")
	}
	if !strings.Contains(selected, "48;5;205") {
		t.Errorf("selected line should set chip background to colorSelected (205), got %q", selected)
	}
}

// TestRenderCommitLineWithAuthorName — author sits between the message
// column and the right-anchored time. Order: graph < subject <
// author < rel-time.
func TestRenderCommitLineWithAuthorName(t *testing.T) {
	c := git.Commit{
		Hash:       "abcdef1234567",
		Subject:    "subj",
		AuthorName: "Byeongmin Jeon",
		AuthorTime: time.Now(),
	}
	line := renderCommitLine(c, nil, "* ", 2, 2, 80, false, false)
	stripped := ansi.Strip(line)
	subjectIdx := strings.Index(stripped, "subj")
	authorIdx := strings.Index(stripped, "Byeongmin")
	relIdx := strings.Index(stripped, "just now")
	if subjectIdx < 0 || authorIdx < 0 || relIdx < 0 {
		t.Fatalf("expected subject, author, time in %q", stripped)
	}
	if subjectIdx >= authorIdx || authorIdx >= relIdx {
		t.Errorf("expected order subject < author < rel; got subject=%d author=%d rel=%d in %q",
			subjectIdx, authorIdx, relIdx, stripped)
	}
}

// TestRenderCommitLineEmptyAuthorOmitsColumn — when AuthorName is empty
// the author segment is skipped entirely so the message column reclaims
// the column budget that author would have used.
func TestRenderCommitLineEmptyAuthorOmitsColumn(t *testing.T) {
	c := git.Commit{
		Hash:       "abcdef1234567",
		Subject:    "no author",
		AuthorTime: time.Now(),
		// AuthorName left empty.
	}
	line := renderCommitLine(c, nil, "* ", 2, 2, 80, false, false)
	stripped := ansi.Strip(line)
	if w := len(stripped); w != 80 {
		t.Errorf("rendered width = %d, want 80", w)
	}
	// Subject ends right before the time, separated by a single space.
	relIdx := strings.Index(stripped, "just now")
	if relIdx <= 0 {
		t.Fatalf("time missing in %q", stripped)
	}
	// One char before the time should be a space (separator), not a digit
	// from the author column.
	if got := stripped[relIdx-1]; got != ' ' {
		t.Errorf("expected single-space separator before time; got %q in %q", got, stripped)
	}
}

// TestRenderCommitLineDropsAuthorWhenNarrow — when the row can't fit
// author + subject + time, author drops first so the subject remains
// visible.
func TestRenderCommitLineDropsAuthorWhenNarrow(t *testing.T) {
	c := git.Commit{
		Hash:       "abcdef1234567",
		Subject:    "Sx",
		AuthorName: "alice",
		AuthorTime: time.Now(),
	}
	// width = cursor 2 + graph 2 + subject 2 + sep 1 + rel 8 = 15
	line := renderCommitLine(c, nil, "* ", 2, 2, 15, false, false)
	stripped := ansi.Strip(line)
	if strings.Contains(stripped, "alice") {
		t.Errorf("author should be dropped at narrow width, got %q", stripped)
	}
	if !strings.Contains(stripped, "Sx") {
		t.Errorf("subject must remain visible, got %q", stripped)
	}
	if !strings.Contains(stripped, "just now") {
		t.Errorf("time must remain visible, got %q", stripped)
	}
}

func TestRenderCommitLineGraphTruncatedAtNarrowWidth(t *testing.T) {
	c := git.Commit{
		Hash:       "abcdef1234567",
		Subject:    "narrow",
		AuthorTime: time.Now(),
	}
	// width 11, cursor 2 + time 8 = 10 → at most 1 column for graph.
	line := renderCommitLine(c, nil, "| | * ", 6, 6, 11, false, false)
	if !strings.Contains(line, "just now") {
		t.Errorf("time must remain visible even when graph is wider than budget, got %q", line)
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
	g, _ = g.Update(commitsStreamDoneMsg{reqID: 1, err: errSentinel})
	view := g.View()
	if !strings.Contains(view, "load error") {
		t.Errorf("error view should mention load error, got %q", view)
	}
}

func TestGraphModelResetForReloadReturnsToLoading(t *testing.T) {
	g := newGraphModel()
	g.SetSize(40, 10)
	g, _ = g.Update(commitsAppendedMsg{reqID: 1, done: true, rows: []graphRow{
		{commit: git.Commit{Hash: "abc1234", Subject: "first", AuthorTime: time.Now()}, commitPrefix: "* ", commitWidth: 2},
	}})
	if !g.loaded {
		t.Fatalf("graph should be loaded after commitsAppendedMsg")
	}
	if want := laneColCap(40); g.graphWidth != want {
		t.Errorf("graphWidth = %d, want %d", g.graphWidth, want)
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

func TestGraphModelGraphWidthIsLaneColCapNotMaxRowWidth(t *testing.T) {
	// Per-row tight: graphWidth is the hard cap derived from pane width
	// (laneColCap), not the widest prefix among rows. The delegate uses
	// it only to truncate rows whose prefix exceeds the cap.
	g := newGraphModel()
	g.SetSize(80, 10)
	now := time.Now()
	g, _ = g.Update(commitsAppendedMsg{reqID: 1, done: true, rows: []graphRow{
		{commit: git.Commit{Hash: "a", Subject: "s1", AuthorTime: now}, commitPrefix: "* ", commitWidth: 2},
		{commit: git.Commit{Hash: "b", Subject: "s2", AuthorTime: now}, commitPrefix: "| | * ", commitWidth: 6},
		{commit: git.Commit{Hash: "c", Subject: "s3", AuthorTime: now}, commitPrefix: "|/ ", commitWidth: 3},
	}})
	want := laneColCap(80)
	if g.graphWidth != want {
		t.Errorf("graphWidth = %d, want %d (laneColCap(80))", g.graphWidth, want)
	}
	if g.delegate.graphWidth != want {
		t.Errorf("delegate.graphWidth = %d, want %d (must match graphModel.graphWidth)", g.delegate.graphWidth, want)
	}
}

func TestGraphModelGraphWidthZeroBeforeSetSize(t *testing.T) {
	// Without SetSize the model has no pane width to compute a cap from,
	// so graphWidth stays at zero — even after a row is appended.
	g := newGraphModel()
	g, _ = g.Update(commitsAppendedMsg{reqID: 1, done: true, rows: []graphRow{
		{commit: git.Commit{Hash: "a", Subject: "s", AuthorTime: time.Now()}},
	}})
	if g.graphWidth != 0 {
		t.Errorf("graphWidth = %d, want 0", g.graphWidth)
	}
}

func TestLaneColCapClampsAtMax(t *testing.T) {
	// Very wide terminal — graph must not grow past maxLaneCap × cellWidth
	// (16 lanes × 2 cols = 32 cols).
	if got := laneColCap(500); got != maxLaneCap*cellWidth {
		t.Errorf("laneColCap(500) = %d, want %d", got, maxLaneCap*cellWidth)
	}
}

func TestLaneColCapClampsAtMin(t *testing.T) {
	// Pathologically narrow — minimum 2 lanes still reserved so the graph
	// stays meaningful even if subject is almost gone.
	if got := laneColCap(12); got != minLaneCap*cellWidth {
		t.Errorf("laneColCap(12) = %d, want %d", got, minLaneCap*cellWidth)
	}
}

func TestApplyGraphCapTracksLaneColCapOnly(t *testing.T) {
	// graphWidth tracks laneColCap(width) only — row prefix width is
	// irrelevant. A wide-prefix row exceeding the cap will be truncated
	// with "…" by buildGraphCell at render time, but doesn't move the cap.
	g := newGraphModel()
	g.SetSize(40, 10)
	g, _ = g.Update(commitsAppendedMsg{reqID: 1, done: true, rows: []graphRow{
		{commit: git.Commit{Hash: "a", Subject: "s", AuthorTime: time.Now()},
			commitPrefix: strings.Repeat("│", 20), commitWidth: 20},
	}})
	wantCap := laneColCap(40)
	if g.graphWidth != wantCap {
		t.Errorf("graphWidth = %d, want %d (cap for width 40)", g.graphWidth, wantCap)
	}
}

func TestBuildGraphCellEllipsisOnTruncate(t *testing.T) {
	cell, w := buildGraphCell(strings.Repeat("│", 12), 12, 6)
	if w != 6 {
		t.Errorf("width = %d, want 6", w)
	}
	if !strings.Contains(cell, "…") {
		t.Errorf("expected ellipsis in truncated cell, got %q", cell)
	}
}

func TestGraphModelJumpToHashMovesCursor(t *testing.T) {
	g := newGraphModel()
	g.SetSize(80, 10)
	now := time.Now()
	g, _ = g.Update(commitsAppendedMsg{reqID: 1, done: true, rows: []graphRow{
		{commit: git.Commit{Hash: "aaa1111", Subject: "first", AuthorTime: now}},
		{commit: git.Commit{Hash: "bbb2222", Subject: "second", AuthorTime: now}},
		{commit: git.Commit{Hash: "ccc3333", Subject: "third", AuthorTime: now}},
	}})

	if !g.JumpToHash("ccc3333") {
		t.Fatal("JumpToHash should report success when the hash matches a row")
	}
	if got := g.list.Index(); got != 2 {
		t.Errorf("cursor index after jump = %d, want 2", got)
	}
}

func TestGraphModelJumpToHashReturnsFalseWhenMissing(t *testing.T) {
	g := newGraphModel()
	g.SetSize(80, 10)
	g, _ = g.Update(commitsAppendedMsg{reqID: 1, done: true, rows: []graphRow{
		{commit: git.Commit{Hash: "aaa1111", Subject: "first", AuthorTime: time.Now()}},
	}})

	if g.JumpToHash("deadbeef") {
		t.Error("JumpToHash should report false when no row matches")
	}
	if got := g.list.Index(); got != 0 {
		t.Errorf("cursor should not move on miss, got index %d", got)
	}
}

func TestGraphModelJumpToHashEmptyHashIsFalse(t *testing.T) {
	g := newGraphModel()
	g.SetSize(80, 10)
	g, _ = g.Update(commitsAppendedMsg{reqID: 1, done: true, rows: []graphRow{
		{commit: git.Commit{Hash: "aaa1111", Subject: "first", AuthorTime: time.Now()}},
	}})
	if g.JumpToHash("") {
		t.Error("JumpToHash on empty hash should not match a row")
	}
}

type sentinelErr struct{}

func (sentinelErr) Error() string { return "sentinel" }

var errSentinel = sentinelErr{}

// TestGraphModelTailFollowRequiresUserMoved guards the PR #14 회귀: a
// single-row first batch leaves cursor at index 0 == len(prev)-1, so without
// the userHasMoved gate the second batch would auto-follow the tail and
// drag cursor down with every batch even though the user never asked.
func TestGraphModelTailFollowRequiresUserMoved(t *testing.T) {
	g := newGraphModel()
	g.SetSize(80, 10)
	now := time.Now()

	g, _ = g.Update(commitsAppendedMsg{
		reqID: 1, done: false,
		rows: []graphRow{
			{commit: git.Commit{Hash: "aaa1111", Subject: "1", AuthorTime: now}},
		},
	})
	if g.list.Index() != 0 {
		t.Fatalf("cursor after first batch = %d, want 0", g.list.Index())
	}
	if g.userHasMoved {
		t.Fatal("userHasMoved must remain false until the user presses a movement key")
	}

	g, _ = g.Update(commitsAppendedMsg{
		reqID: 1, done: true,
		rows: []graphRow{
			{commit: git.Commit{Hash: "bbb2222", Subject: "2", AuthorTime: now}},
			{commit: git.Commit{Hash: "ccc3333", Subject: "3", AuthorTime: now}},
		},
	})
	if g.list.Index() != 0 {
		t.Errorf("cursor after second batch (no user keypress) = %d, want 0 — PR #14 회귀!",
			g.list.Index())
	}
}

func TestGraphModelTailFollowAppendsCursorAfterUserMoved(t *testing.T) {
	g := newGraphModel()
	g.SetSize(80, 10)
	now := time.Now()

	g, _ = g.Update(commitsAppendedMsg{
		reqID: 1, done: false,
		rows: []graphRow{
			{commit: git.Commit{Hash: "aaa1111", Subject: "1", AuthorTime: now}},
			{commit: git.Commit{Hash: "bbb2222", Subject: "2", AuthorTime: now}},
			{commit: git.Commit{Hash: "ccc3333", Subject: "3", AuthorTime: now}},
		},
	})
	// Move cursor to last row of prev (index 2).
	g, _ = g.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	g, _ = g.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if !g.userHasMoved {
		t.Fatal("userHasMoved should be true after j keypress")
	}
	if g.list.Index() != 2 {
		t.Fatalf("cursor before second batch = %d, want 2", g.list.Index())
	}

	g, _ = g.Update(commitsAppendedMsg{
		reqID: 1, done: true,
		rows: []graphRow{
			{commit: git.Commit{Hash: "ddd4444", Subject: "4", AuthorTime: now}},
			{commit: git.Commit{Hash: "eee5555", Subject: "5", AuthorTime: now}},
		},
	})
	if g.list.Index() != 4 {
		t.Errorf("tail-follow after user-moved keypress: cursor = %d, want 4", g.list.Index())
	}
}

func TestGraphModelTailFollowStaysWhenCursorNotOnTail(t *testing.T) {
	g := newGraphModel()
	g.SetSize(80, 10)
	now := time.Now()

	g, _ = g.Update(commitsAppendedMsg{
		reqID: 1, done: false,
		rows: []graphRow{
			{commit: git.Commit{Hash: "aaa1111", Subject: "1", AuthorTime: now}},
			{commit: git.Commit{Hash: "bbb2222", Subject: "2", AuthorTime: now}},
			{commit: git.Commit{Hash: "ccc3333", Subject: "3", AuthorTime: now}},
		},
	})
	// Move to middle row (index 1) — NOT the last row.
	g, _ = g.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if g.list.Index() != 1 {
		t.Fatalf("cursor after j = %d, want 1", g.list.Index())
	}

	g, _ = g.Update(commitsAppendedMsg{
		reqID: 1, done: true,
		rows: []graphRow{
			{commit: git.Commit{Hash: "ddd4444", Subject: "4", AuthorTime: now}},
			{commit: git.Commit{Hash: "eee5555", Subject: "5", AuthorTime: now}},
		},
	})
	if g.list.Index() != 1 {
		t.Errorf("cursor not on tail of prev: stayed at %d, want 1", g.list.Index())
	}
}

func TestGraphModelStreamDoneClearsLoadingOnEmpty(t *testing.T) {
	g := newGraphModel()
	g.SetSize(80, 10)
	if g.View() != "loading…" {
		t.Fatalf("initial View = %q, want %q", g.View(), "loading…")
	}
	g, _ = g.Update(commitsStreamDoneMsg{reqID: 1})
	if !g.loaded {
		t.Error("commitsStreamDoneMsg should mark graph loaded")
	}
	if got := g.View(); got != "(no commits)" {
		t.Errorf("after empty done, View = %q, want %q", got, "(no commits)")
	}
}

// renderDelegateRow builds a list.Model around the given items and renders
// the requested index through commitDelegate. Returns the ANSI-stripped
// commit line (the second of the connector+commit pair).
func renderDelegateRow(t *testing.T, items []list.Item, idx, outerWidth, capW int) string {
	t.Helper()
	d := commitDelegate{graphWidth: capW}
	l := list.New(items, d, outerWidth, 10)
	var buf strings.Builder
	d.Render(&buf, l, idx, items[idx])
	parts := strings.SplitN(buf.String(), "\n", 2)
	if len(parts) != 2 {
		t.Fatalf("expected connector + commit lines, got %q", buf.String())
	}
	return ansi.Strip(parts[1])
}

// visibleColOf returns the visible column where sub starts in s, or -1 if
// sub is not present. Unlike strings.Index it counts cells (handles wide
// runes and multi-byte ASCII glyphs like "›") rather than bytes.
func visibleColOf(s, sub string) int {
	i := strings.Index(s, sub)
	if i < 0 {
		return -1
	}
	return runewidth.StringWidth(s[:i])
}

func TestCommitDelegateRendersGraphPerRowTight(t *testing.T) {
	// Two rows with very different lane counts. With per-row tight the
	// subject starts immediately after each row's own graph width — so
	// the two rows have *different* subject start columns. That is the
	// whole point of dropping the max-align padding.
	now := time.Now()
	rowA := commitItem{
		c:                git.Commit{Hash: "aaaa111", Subject: "row-a", AuthorTime: now},
		commitPrefix:     "* ",
		commitGraphWidth: 2,
	}
	rowB := commitItem{
		c:                git.Commit{Hash: "bbbb222", Subject: "row-b", AuthorTime: now},
		commitPrefix:     "| | | * ",
		commitGraphWidth: 8,
	}
	items := []list.Item{rowA, rowB}

	saStripped := renderDelegateRow(t, items, 0, 80, maxLaneCap*cellWidth)
	sbStripped := renderDelegateRow(t, items, 1, 80, maxLaneCap*cellWidth)

	saSubject := visibleColOf(saStripped, "row-a")
	sbSubject := visibleColOf(sbStripped, "row-b")
	if saSubject < 0 || sbSubject < 0 {
		t.Fatalf("subject missing; A=%q B=%q", saStripped, sbStripped)
	}
	// No chips and no author here — subject sits at cursor + own graph width.
	if want := cursorColWidth + rowA.commitGraphWidth; saSubject != want {
		t.Errorf("row A subject col = %d, want %d", saSubject, want)
	}
	if want := cursorColWidth + rowB.commitGraphWidth; sbSubject != want {
		t.Errorf("row B subject col = %d, want %d", sbSubject, want)
	}
	if saSubject == sbSubject {
		t.Errorf("per-row tight: rows must not share the same subject column, both at %d", saSubject)
	}
}

func TestCommitDelegateRightAnchorStaysAcrossRows(t *testing.T) {
	// Right-anchor invariant: time lives at width - timeColWidth regardless
	// of how wide each row's graph is. Per-row tight must not break this.
	now := time.Now()
	rowA := commitItem{
		c:                git.Commit{Hash: "aaaa111", Subject: "row-a", AuthorTime: now},
		commitPrefix:     "* ",
		commitGraphWidth: 2,
	}
	rowB := commitItem{
		c:                git.Commit{Hash: "bbbb222", Subject: "row-b", AuthorTime: now},
		commitPrefix:     "| | | * ",
		commitGraphWidth: 8,
	}
	items := []list.Item{rowA, rowB}
	const width = 80

	saStripped := renderDelegateRow(t, items, 0, width, maxLaneCap*cellWidth)
	sbStripped := renderDelegateRow(t, items, 1, width, maxLaneCap*cellWidth)

	saRel := visibleColOf(saStripped, "just now")
	sbRel := visibleColOf(sbStripped, "just now")
	if saRel < 0 || sbRel < 0 {
		t.Fatalf("time missing; A=%q B=%q", saStripped, sbStripped)
	}
	// Right anchor is what matters: both rows must share the same time
	// column regardless of how wide their graph cell is. We don't pin
	// the absolute column because list applies its own outer padding.
	if saRel != sbRel {
		t.Errorf("row A time col %d != row B time col %d (right anchor must hold across rows)", saRel, sbRel)
	}
}

// renderDelegateRowRaw is renderDelegateRow but returns the ANSI-bearing
// (commit) line so callers can assert on the SGR codes used by the
// HEAD-as-dim-boundary pass. cursorIdx pins the list cursor so the row
// under test isn't accidentally rendered as selected (selected wins over
// dim, which would mask the assertion).
func renderDelegateRowRaw(t *testing.T, d commitDelegate, items []list.Item, idx, cursorIdx, outerWidth int) string {
	t.Helper()
	l := list.New(items, d, outerWidth, 10)
	l.Select(cursorIdx)
	var buf strings.Builder
	d.Render(&buf, l, idx, items[idx])
	parts := strings.SplitN(buf.String(), "\n", 2)
	if len(parts) != 2 {
		t.Fatalf("expected connector + commit lines, got %q", buf.String())
	}
	return parts[1]
}

// dimSGR is the ANSI substring that the dim pass emits — Foreground 240
// in 256-color form. dimLine strips inner ANSI then re-renders with the
// grey foreground, so this is the marker tests look for to confirm a
// row is in the "above HEAD, not an ancestor" band.
const dimSGR = "38;5;240"

func TestCommitDelegateDimAppliedAboveHEAD(t *testing.T) {
	now := time.Now()
	// Three rows; index 1 is HEAD. index 0 is "above HEAD" → dim.
	items := []list.Item{
		commitItem{c: git.Commit{Hash: "above01", Subject: "above-head", AuthorTime: now}, commitPrefix: "* ", commitGraphWidth: 2},
		commitItem{c: git.Commit{Hash: "head002", Subject: "the-head", AuthorTime: now}, commitPrefix: "* ", commitGraphWidth: 2},
		commitItem{c: git.Commit{Hash: "below03", Subject: "ancestor-of-head", AuthorTime: now}, commitPrefix: "* ", commitGraphWidth: 2},
	}
	d := commitDelegate{graphWidth: maxLaneCap * cellWidth, headRowIndex: 1, headAncestors: map[string]struct{}{
		"head002": {},
		"below03": {},
	}}

	// Park cursor on the HEAD row so the rows being tested aren't selected
	// (selected wins over dim).
	above := renderDelegateRowRaw(t, d, items, 0, 1, 80)
	if !strings.Contains(above, dimSGR) {
		t.Errorf("row above HEAD should carry dim SGR; got %q", above)
	}

	head := renderDelegateRowRaw(t, d, items, 1, 1, 80)
	if strings.Contains(head, dimSGR) {
		t.Errorf("HEAD row itself must not be dimmed; got %q", head)
	}

	below := renderDelegateRowRaw(t, d, items, 2, 1, 80)
	if strings.Contains(below, dimSGR) {
		t.Errorf("row below HEAD must not be dimmed; got %q", below)
	}
}

func TestCommitDelegateDimSkipsAncestorAboveHEAD(t *testing.T) {
	// In an --all view, an ancestor of HEAD (e.g. an older common base) can
	// appear *above* HEAD when topo-order interleaves another branch's tip.
	// shouldDim must keep the ancestor row bright even though it sits above.
	now := time.Now()
	items := []list.Item{
		commitItem{c: git.Commit{Hash: "sibl001", Subject: "sibling-tip", AuthorTime: now}, commitPrefix: "* ", commitGraphWidth: 2},
		commitItem{c: git.Commit{Hash: "anc0002", Subject: "head-ancestor", AuthorTime: now}, commitPrefix: "* ", commitGraphWidth: 2},
		commitItem{c: git.Commit{Hash: "head003", Subject: "the-head", AuthorTime: now}, commitPrefix: "* ", commitGraphWidth: 2},
	}
	d := commitDelegate{graphWidth: maxLaneCap * cellWidth, headRowIndex: 2, headAncestors: map[string]struct{}{
		"head003": {},
		"anc0002": {},
	}}

	sibling := renderDelegateRowRaw(t, d, items, 0, 2, 80)
	if !strings.Contains(sibling, dimSGR) {
		t.Errorf("non-ancestor sibling above HEAD should be dimmed; got %q", sibling)
	}
	ancestor := renderDelegateRowRaw(t, d, items, 1, 2, 80)
	if strings.Contains(ancestor, dimSGR) {
		t.Errorf("HEAD ancestor above HEAD must stay bright; got %q", ancestor)
	}
}

func TestCommitDelegateDimSuppressedWhenHeadOutOfWindow(t *testing.T) {
	// HEAD never showed up in the loaded window → headRowIndex stays at -1
	// and the whole graph stays bright. This is the explicit policy from
	// the interview: no signal beyond what the user already gets via
	// status messages on ref-tip jumps.
	now := time.Now()
	items := []list.Item{
		commitItem{c: git.Commit{Hash: "aaa0001", Subject: "row-a", AuthorTime: now}, commitPrefix: "* ", commitGraphWidth: 2},
		commitItem{c: git.Commit{Hash: "bbb0002", Subject: "row-b", AuthorTime: now}, commitPrefix: "* ", commitGraphWidth: 2},
	}
	d := commitDelegate{graphWidth: maxLaneCap * cellWidth, headRowIndex: -1}
	// Cursor parked off-rows (-1 acts as no-selection in bubbles/list).
	for i, it := range items {
		raw := renderDelegateRowRaw(t, d, items, i, len(items), 80)
		if strings.Contains(raw, dimSGR) {
			t.Errorf("row %d (%v) must not be dimmed when HEAD is out of window; got %q", i, it, raw)
		}
	}
}

func TestCommitDelegateDimFallbackBeforeAncestorsArrive(t *testing.T) {
	// Before `git rev-list HEAD` lands the delegate has headRowIndex but no
	// ancestors set. Fallback: dim every row above HEAD so the boundary
	// reads on first paint (precision arrives a moment later).
	now := time.Now()
	items := []list.Item{
		commitItem{c: git.Commit{Hash: "above01", Subject: "above", AuthorTime: now}, commitPrefix: "* ", commitGraphWidth: 2},
		commitItem{c: git.Commit{Hash: "head002", Subject: "head", AuthorTime: now}, commitPrefix: "* ", commitGraphWidth: 2},
	}
	d := commitDelegate{graphWidth: maxLaneCap * cellWidth, headRowIndex: 1, headAncestors: nil}

	above := renderDelegateRowRaw(t, d, items, 0, 1, 80)
	if !strings.Contains(above, dimSGR) {
		t.Errorf("fallback dim should apply when ancestors not yet loaded; got %q", above)
	}
	head := renderDelegateRowRaw(t, d, items, 1, 1, 80)
	if strings.Contains(head, dimSGR) {
		t.Errorf("HEAD row itself must stay bright in fallback; got %q", head)
	}
}

func TestGraphModelCaptureHeadRowFromDecoration(t *testing.T) {
	// captureHeadRow must read both forms of HEAD encoding from %D:
	// "HEAD -> main" (named) and bare "HEAD" (detached).
	g := newGraphModel()
	rows := []graphRow{
		{commit: git.Commit{Hash: "aaa", RefNames: []string{"feature/x"}}},
		{commit: git.Commit{Hash: "bbb", RefNames: []string{"HEAD -> main", "origin/main"}}},
		{commit: git.Commit{Hash: "ccc"}},
	}
	g.captureHeadRow(rows, 0)
	if g.headRowIndex != 1 {
		t.Errorf("named HEAD: got index=%d, want 1", g.headRowIndex)
	}
	if !g.headDimDirty {
		t.Errorf("captureHeadRow must mark dim dirty when HEAD is found")
	}

	g2 := newGraphModel()
	rowsDet := []graphRow{
		{commit: git.Commit{Hash: "ddd", RefNames: []string{"HEAD"}}},
		{commit: git.Commit{Hash: "eee"}},
	}
	g2.captureHeadRow(rowsDet, 5) // baseIndex 5 simulates batch midstream
	if g2.headRowIndex != 5 {
		t.Errorf("detached HEAD: got index=%d, want 5", g2.headRowIndex)
	}

	// A second batch must not overwrite once HEAD is captured.
	g2.captureHeadRow([]graphRow{
		{commit: git.Commit{Hash: "fff", RefNames: []string{"HEAD -> main"}}},
	}, 10)
	if g2.headRowIndex != 5 {
		t.Errorf("second-batch HEAD must not overwrite first-batch capture; got index=%d", g2.headRowIndex)
	}
}
