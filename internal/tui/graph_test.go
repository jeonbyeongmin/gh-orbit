package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

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
	line := renderCommitLine(c, "", 0, 0, 40, false)
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
	// Width less than cursor(2)+hash(7)+space(1)+time(8)+space(1) = 19.
	line := renderCommitLine(c, "", 0, 0, 10, false)
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
	line := renderCommitLine(c, "", 0, 0, 80, true)
	if !strings.Contains(line, "›") {
		t.Errorf("selected line should contain cursor marker, got %q", line)
	}
	unselected := renderCommitLine(c, "", 0, 0, 80, false)
	if strings.Contains(unselected, "›") {
		t.Errorf("unselected line should not contain cursor marker, got %q", unselected)
	}
}

func TestRenderCommitLineOrderingGraphSubjectHash(t *testing.T) {
	c := git.Commit{
		Hash:       "abcdef1234567",
		Subject:    "graph layout",
		AuthorTime: time.Now(),
	}
	line := renderCommitLine(c, "* ", 2, 2, 80, false)
	stripped := ansi.Strip(line)
	starIdx := strings.Index(stripped, "*")
	subjectIdx := strings.Index(stripped, "graph layout")
	hashIdx := strings.Index(stripped, "abcdef1")
	relIdx := strings.Index(stripped, "just now")
	if starIdx < 0 || subjectIdx < 0 || hashIdx < 0 || relIdx < 0 {
		t.Fatalf("expected star, subject, hash, and rel in %q", stripped)
	}
	// Layout: graph < message (subject) < hash < rel-time. Hash and time
	// are right-anchored; subject sits in the message column and absorbs
	// truncation when the row is narrow.
	if starIdx >= subjectIdx || subjectIdx >= hashIdx || hashIdx >= relIdx {
		t.Errorf("expected order graph < subject < hash < rel, got star=%d subject=%d hash=%d rel=%d in %q",
			starIdx, subjectIdx, hashIdx, relIdx, stripped)
	}
}

func TestRenderCommitLineHashAnchoredToRightEdge(t *testing.T) {
	c := git.Commit{
		Hash:       "abcdef1234567",
		Subject:    "right edge",
		AuthorTime: time.Now(),
	}
	line := renderCommitLine(c, "* ", 2, 2, 80, false)
	stripped := ansi.Strip(line)
	// The visible width must still equal the requested width.
	if w := len(stripped); w != 80 {
		t.Errorf("rendered width = %d, want 80 (full row)", w)
	}
	// Hash + time sit at the right edge: width - rightTail. With
	// shortHashLen=7 and timeColWidth=8 separated by one space, the hash
	// starts at width - 7 - 1 - 8 = 64.
	hashIdx := strings.Index(stripped, "abcdef1")
	wantHashIdx := 80 - shortHashLen - 1 - timeColWidth
	if hashIdx != wantHashIdx {
		t.Errorf("hash starts at %d, want %d (anchored to right edge)", hashIdx, wantHashIdx)
	}
	// Subject sits in the message column, between the graph and the hash.
	subjectIdx := strings.Index(stripped, "right edge")
	wantSubjectMin := 2 + 2 // cursor + graph
	if subjectIdx < wantSubjectMin || subjectIdx >= hashIdx {
		t.Errorf("subject not in the message column; subjectIdx=%d hashIdx=%d", subjectIdx, hashIdx)
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
	line := renderCommitLine(c, "*", 1, 4, 80, false)
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
	line := renderCommitLine(c, "* ", 2, 2, 80, false)
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
	line := renderCommitLine(c, "* ", 2, 2, 80, false)
	stripped := ansi.Strip(line)
	// Chip "main" attaches to the front of the subject in the message
	// column — both sit between the graph and the hash.
	mainIdx := strings.Index(stripped, "main")
	subjectIdx := strings.Index(stripped, "with chip")
	hashIdx := strings.Index(stripped, "abcdef1")
	if mainIdx < 0 || subjectIdx < 0 || hashIdx < 0 {
		t.Fatalf("expected chip, subject, hash in %q", stripped)
	}
	if mainIdx >= subjectIdx || subjectIdx >= hashIdx {
		t.Errorf("expected order chip < subject < hash; got chip=%d subject=%d hash=%d in %q",
			mainIdx, subjectIdx, hashIdx, stripped)
	}
}

func TestRenderCommitLineWithPairedChip(t *testing.T) {
	c := git.Commit{
		Hash:       "abcdef1234567",
		Subject:    "paired",
		AuthorTime: time.Now(),
		RefNames:   []string{"HEAD -> main", "origin/main"},
	}
	line := renderCommitLine(c, "* ", 2, 2, 80, false)
	stripped := ansi.Strip(line)
	if strings.Count(stripped, "main") != 1 {
		t.Errorf("paired chip should render 'main' exactly once, got %q", stripped)
	}
	if !strings.Contains(stripped, "↑") {
		t.Errorf("paired chip should carry ↑ marker, got %q", stripped)
	}
	if !strings.Contains(stripped, "HEAD") {
		t.Errorf("HEAD chip should appear, got %q", stripped)
	}
}

func TestRenderCommitLineWithDetachedHead(t *testing.T) {
	c := git.Commit{
		Hash:       "abcdef1234567",
		Subject:    "detached",
		AuthorTime: time.Now(),
		RefNames:   []string{"HEAD"},
	}
	line := renderCommitLine(c, "* ", 2, 2, 80, false)
	stripped := ansi.Strip(line)
	if !strings.Contains(stripped, "HEAD") {
		t.Errorf("detached head should render standalone HEAD chip, got %q", stripped)
	}
}

func TestRenderCommitLineWithTagChipStripsPrefix(t *testing.T) {
	c := git.Commit{
		Hash:       "abcdef1234567",
		Subject:    "tag commit",
		AuthorTime: time.Now(),
		RefNames:   []string{"tag: v0.0.1"},
	}
	line := renderCommitLine(c, "* ", 2, 2, 80, false)
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
	line := renderCommitLine(c, "* ", 2, 2, 100, false)
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
	// width = cursor 2 + graph 2 + hash 7 + space 1 + time 8 + space 1 + subject 2 = 23
	line := renderCommitLine(c, "* ", 2, 2, 23, false)
	stripped := ansi.Strip(line)
	if strings.Contains(stripped, "this-is-a-very-long") {
		t.Errorf("chip should be dropped when subject can't fit alongside it, got %q", stripped)
	}
	if !strings.Contains(stripped, "abcdef1") {
		t.Errorf("hash must remain visible, got %q", stripped)
	}
}

func TestRenderCommitLineSelectedRecolorsChipBackground(t *testing.T) {
	c := git.Commit{
		Hash:       "abcdef1234567",
		Subject:    "sel",
		AuthorTime: time.Now(),
		RefNames:   []string{"main"},
	}
	unselected := renderCommitLine(c, "* ", 2, 2, 80, false)
	selected := renderCommitLine(c, "* ", 2, 2, 80, true)
	if unselected == selected {
		t.Fatalf("selected line should differ from unselected")
	}
	if !strings.Contains(selected, "48;5;205") {
		t.Errorf("selected line should set chip background to colorSelected (205), got %q", selected)
	}
}

// TestRenderCommitLineWithAuthorName — author sits between the message
// column and the right-anchored hash/time. Order: graph < subject <
// author < hash < rel-time.
func TestRenderCommitLineWithAuthorName(t *testing.T) {
	c := git.Commit{
		Hash:       "abcdef1234567",
		Subject:    "subj",
		AuthorName: "Byeongmin Jeon",
		AuthorTime: time.Now(),
	}
	line := renderCommitLine(c, "* ", 2, 2, 80, false)
	stripped := ansi.Strip(line)
	subjectIdx := strings.Index(stripped, "subj")
	authorIdx := strings.Index(stripped, "Byeongmin")
	hashIdx := strings.Index(stripped, "abcdef1")
	if subjectIdx < 0 || authorIdx < 0 || hashIdx < 0 {
		t.Fatalf("expected subject, author, hash in %q", stripped)
	}
	if subjectIdx >= authorIdx || authorIdx >= hashIdx {
		t.Errorf("expected order subject < author < hash; got subject=%d author=%d hash=%d in %q",
			subjectIdx, authorIdx, hashIdx, stripped)
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
	line := renderCommitLine(c, "* ", 2, 2, 80, false)
	stripped := ansi.Strip(line)
	if w := len(stripped); w != 80 {
		t.Errorf("rendered width = %d, want 80", w)
	}
	// Subject ends right before the hash, separated by a single space.
	hashIdx := strings.Index(stripped, "abcdef1")
	if hashIdx <= 0 {
		t.Fatalf("hash missing in %q", stripped)
	}
	// One char before the hash should be a space (separator), not a digit
	// from the author column.
	if got := stripped[hashIdx-1]; got != ' ' {
		t.Errorf("expected single-space separator before hash; got %q in %q", got, stripped)
	}
}

// TestRenderCommitLineDropsAuthorWhenNarrow — when the row can't fit
// author + subject + hash + time, author drops first so the subject
// remains visible.
func TestRenderCommitLineDropsAuthorWhenNarrow(t *testing.T) {
	c := git.Commit{
		Hash:       "abcdef1234567",
		Subject:    "Sx",
		AuthorName: "alice",
		AuthorTime: time.Now(),
	}
	// width = cursor 2 + graph 2 + subject 2 + sep 1 + hash 7 + sep 1 + rel 8 = 23
	line := renderCommitLine(c, "* ", 2, 2, 23, false)
	stripped := ansi.Strip(line)
	if strings.Contains(stripped, "alice") {
		t.Errorf("author should be dropped at narrow width, got %q", stripped)
	}
	if !strings.Contains(stripped, "Sx") {
		t.Errorf("subject must remain visible, got %q", stripped)
	}
	if !strings.Contains(stripped, "abcdef1") {
		t.Errorf("hash must remain visible, got %q", stripped)
	}
}

func TestRenderCommitLineGraphTruncatedAtNarrowWidth(t *testing.T) {
	c := git.Commit{
		Hash:       "abcdef1234567",
		Subject:    "narrow",
		AuthorTime: time.Now(),
	}
	// width 10, cursor 2 + hash 7 = 9 → at most 1 column for graph.
	line := renderCommitLine(c, "| | * ", 6, 6, 10, false)
	if !strings.Contains(line, "abcdef1") {
		t.Errorf("hash must remain visible even when graph is wider than budget, got %q", line)
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
	g, _ = g.Update(commitsAppendedMsg{reqID: 1, rows: []graphRow{
		{commit: git.Commit{Hash: "abc1234", Subject: "first", AuthorTime: time.Now()}, commitPrefix: "* ", commitWidth: 2},
	}})
	if !g.loaded {
		t.Fatalf("graph should be loaded after commitsAppendedMsg")
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
	g, _ = g.Update(commitsAppendedMsg{reqID: 1, rows: []graphRow{
		{commit: git.Commit{Hash: "a", Subject: "s1", AuthorTime: now}, commitPrefix: "* ", commitWidth: 2},
		{commit: git.Commit{Hash: "b", Subject: "s2", AuthorTime: now}, commitPrefix: "| | * ", commitWidth: 6},
		{commit: git.Commit{Hash: "c", Subject: "s3", AuthorTime: now}, commitPrefix: "|/ ", commitWidth: 3},
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
	g, _ = g.Update(commitsAppendedMsg{reqID: 1, rows: []graphRow{
		{commit: git.Commit{Hash: "a", Subject: "s", AuthorTime: time.Now()}},
	}})
	if g.graphWidth != 0 {
		t.Errorf("graphWidth = %d, want 0", g.graphWidth)
	}
}

func TestLaneColCapClampsAtMax(t *testing.T) {
	// Very wide terminal — graph must not grow past maxLaneCap × cellWidth.
	if got := laneColCap(500); got != maxLaneCap*cellWidth {
		t.Errorf("laneColCap(500) = %d, want %d", got, maxLaneCap*cellWidth)
	}
}

func TestLaneColCapClampsAtMin(t *testing.T) {
	// Pathologically narrow — minimum 2 lanes still reserved so the graph
	// stays meaningful even if subject is almost gone.
	if got := laneColCap(20); got != minLaneCap*cellWidth {
		t.Errorf("laneColCap(20) = %d, want %d", got, minLaneCap*cellWidth)
	}
}

func TestApplyGraphCapTruncatesWhenWidthBelowMaxVisual(t *testing.T) {
	g := newGraphModel()
	// Pretend many rows produced a 20-column graph in total.
	g.SetSize(40, 10)
	g, _ = g.Update(commitsAppendedMsg{reqID: 1, rows: []graphRow{
		{commit: git.Commit{Hash: "a", Subject: "s", AuthorTime: time.Now()},
			commitPrefix: strings.Repeat("│", 20), commitWidth: 20},
	}})
	if g.maxVisualWidth != 20 {
		t.Fatalf("maxVisualWidth = %d, want 20", g.maxVisualWidth)
	}
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
	g, _ = g.Update(commitsAppendedMsg{reqID: 1, rows: []graphRow{
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
	g, _ = g.Update(commitsAppendedMsg{reqID: 1, rows: []graphRow{
		{commit: git.Commit{Hash: "aaa1111", Subject: "first", AuthorTime: time.Now()}},
	}})

	if g.JumpToHash("deadbeef") {
		t.Error("JumpToHash should report false when no row matches")
	}
	if got := g.list.Index(); got != 0 {
		t.Errorf("cursor should not move on miss, got index %d", got)
	}
}

func TestGraphModelEmitsCommitSelectedAfterLoad(t *testing.T) {
	g := newGraphModel()
	g.SetSize(80, 10)
	now := time.Now()
	g, cmd := g.Update(commitsAppendedMsg{reqID: 1, rows: []graphRow{
		{commit: git.Commit{Hash: "aaa1111", Subject: "first", AuthorTime: now}},
		{commit: git.Commit{Hash: "bbb2222", Subject: "second", AuthorTime: now}},
	}})
	if cmd == nil {
		t.Fatal("first commitsAppendedMsg should batch a commitSelectedMsg cmd for the initial cursor row")
	}
	msg := cmd()
	sel, ok := msg.(commitSelectedMsg)
	if !ok {
		// tea.Batch's flattened cmd may itself be a BatchMsg containing the
		// SetItems cmd plus our emitter; walk it if so.
		if batch, isBatch := msg.(tea.BatchMsg); isBatch {
			for _, sub := range batch {
				if sub == nil {
					continue
				}
				if s, ok2 := sub().(commitSelectedMsg); ok2 {
					sel, ok = s, true
					break
				}
			}
		}
	}
	if !ok {
		t.Fatalf("expected commitSelectedMsg in batched cmd, got %T", msg)
	}
	if sel.hash != "aaa1111" {
		t.Errorf("initial commitSelectedMsg.hash = %q, want aaa1111", sel.hash)
	}
}

func TestGraphModelEmitsCommitSelectedOnCursorChange(t *testing.T) {
	g := newGraphModel()
	g.SetSize(80, 10)
	now := time.Now()
	g, _ = g.Update(commitsAppendedMsg{reqID: 1, rows: []graphRow{
		{commit: git.Commit{Hash: "aaa1111", Subject: "first", AuthorTime: now}},
		{commit: git.Commit{Hash: "bbb2222", Subject: "second", AuthorTime: now}},
	}})

	g, cmd := g.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if cmd == nil {
		t.Fatal("j should emit a batched cmd including commitSelectedMsg")
	}
	saw := false
	if batch, ok := cmd().(tea.BatchMsg); ok {
		for _, sub := range batch {
			if sub == nil {
				continue
			}
			if s, ok := sub().(commitSelectedMsg); ok && s.hash == "bbb2222" {
				saw = true
				break
			}
		}
	} else if s, ok := cmd().(commitSelectedMsg); ok && s.hash == "bbb2222" {
		saw = true
	}
	if !saw {
		t.Errorf("expected commitSelectedMsg{hash:bbb2222} after j, did not find it")
	}
}

func TestGraphModelDoesNotEmitWhenCursorUnchanged(t *testing.T) {
	g := newGraphModel()
	g.SetSize(80, 10)
	now := time.Now()
	g, _ = g.Update(commitsAppendedMsg{reqID: 1, rows: []graphRow{
		{commit: git.Commit{Hash: "aaa1111", Subject: "first", AuthorTime: now}},
	}})

	// Single-row list — k at the top is a no-op for the cursor.
	_, cmd := g.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if cmd != nil {
		// Even when the list returns a cmd of its own (e.g. for filtering),
		// it must not be a commitSelectedMsg.
		msg := cmd()
		if _, ok := msg.(commitSelectedMsg); ok {
			t.Errorf("commitSelectedMsg should not fire when cursor stays put")
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, sub := range batch {
				if sub == nil {
					continue
				}
				if _, isSel := sub().(commitSelectedMsg); isSel {
					t.Errorf("commitSelectedMsg should not appear in batch when cursor stays put")
				}
			}
		}
	}
}

func TestGraphModelJumpToHashEmptyHashIsFalse(t *testing.T) {
	g := newGraphModel()
	g.SetSize(80, 10)
	g, _ = g.Update(commitsAppendedMsg{reqID: 1, rows: []graphRow{
		{commit: git.Commit{Hash: "aaa1111", Subject: "first", AuthorTime: time.Now()}},
	}})
	if g.JumpToHash("") {
		t.Error("JumpToHash on empty hash should not match a row")
	}
}

type sentinelErr struct{}

func (sentinelErr) Error() string { return "sentinel" }

var errSentinel = sentinelErr{}

func TestGraphModelTailFollowAppendsCursor(t *testing.T) {
	g := newGraphModel()
	g.SetSize(80, 10)
	now := time.Now()

	// First batch lands one commit; cursor settles at index 0 (= the only,
	// = the tail row).
	g, _ = g.Update(commitsAppendedMsg{
		reqID: 1,
		rows: []graphRow{
			{commit: git.Commit{Hash: "a1", Subject: "first", AuthorTime: now}},
		},
	})
	if got := g.list.Index(); got != 0 {
		t.Fatalf("after first batch cursor index = %d, want 0", got)
	}

	// Cursor was on the tail when the second batch arrives. Tail-follow
	// should slide it down to the new last row.
	g, _ = g.Update(commitsAppendedMsg{
		reqID: 1,
		rows: []graphRow{
			{commit: git.Commit{Hash: "a2", Subject: "second", AuthorTime: now}},
			{commit: git.Commit{Hash: "a3", Subject: "third", AuthorTime: now}},
		},
	})
	if got, want := g.list.Index(), 2; got != want {
		t.Errorf("tail-follow cursor index = %d, want %d (last row of 3)", got, want)
	}
}

func TestGraphModelTailFollowStaysWhenCursorNotOnTail(t *testing.T) {
	g := newGraphModel()
	g.SetSize(80, 10)
	now := time.Now()

	// First batch lands three commits. Cursor stays at index 0 (newest /
	// head) which is *not* the tail (tail = index 2).
	g, _ = g.Update(commitsAppendedMsg{
		reqID: 1,
		rows: []graphRow{
			{commit: git.Commit{Hash: "a1", Subject: "first", AuthorTime: now}},
			{commit: git.Commit{Hash: "a2", Subject: "second", AuthorTime: now}},
			{commit: git.Commit{Hash: "a3", Subject: "third", AuthorTime: now}},
		},
	})
	if got := g.list.Index(); got != 0 {
		t.Fatalf("first batch cursor index = %d, want 0 (head)", got)
	}

	// Subsequent batch must not move the cursor — the user's deliberate
	// position takes priority over tail-follow.
	g, _ = g.Update(commitsAppendedMsg{
		reqID: 1,
		rows: []graphRow{
			{commit: git.Commit{Hash: "a4", Subject: "fourth", AuthorTime: now}},
		},
	})
	if got, want := g.list.Index(), 0; got != want {
		t.Errorf("non-tail cursor moved to %d, want stay at %d", got, want)
	}
}

func TestGraphModelStreamDoneClearsLoadingOnEmpty(t *testing.T) {
	// Empty repo path: LogStream produces zero commits and closes cleanly.
	// View must flip from "loading…" to "(no commits)" rather than spin
	// forever on the placeholder.
	g := newGraphModel()
	g.SetSize(80, 10)
	g, _ = g.Update(commitsStreamDoneMsg{reqID: 1})
	if !g.loaded {
		t.Errorf("commitsStreamDoneMsg should mark graph loaded even with zero rows")
	}
	if got := g.View(); got != "(no commits)" {
		t.Errorf("empty stream view = %q, want %q", got, "(no commits)")
	}
}
