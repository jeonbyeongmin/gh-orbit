package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

func makeRefs(localN, remoteN, tagN int) []git.Ref {
	out := make([]git.Ref, 0, localN+remoteN+tagN)
	for i := 0; i < localN; i++ {
		out = append(out, git.Ref{ShortName: fmt.Sprintf("local-%d", i), Kind: git.RefKindLocal})
	}
	for i := 0; i < remoteN; i++ {
		out = append(out, git.Ref{ShortName: fmt.Sprintf("remote-%d", i), Kind: git.RefKindRemote})
	}
	for i := 0; i < tagN; i++ {
		out = append(out, git.Ref{ShortName: fmt.Sprintf("tag-%d", i), Kind: git.RefKindTag})
	}
	return out
}

func pressKey(t *testing.T, r refModel, key string) refModel {
	t.Helper()
	out, _ := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	return out
}

func TestRefModelInitialView(t *testing.T) {
	r := newRefsModel()
	if got := r.View(); got != "loading…" {
		t.Errorf("initial view = %q, want %q", got, "loading…")
	}
}

func TestRefModelHandlesLoadFailure(t *testing.T) {
	r := newRefsModel()
	r, _ = r.Update(refsLoadFailedMsg{err: errSentinel})
	view := r.View()
	if !strings.Contains(view, "load error") {
		t.Errorf("error view should mention load error, got %q", view)
	}
}

func TestRefModelRendersAllSectionHeaders(t *testing.T) {
	r := newRefsModel()
	r.SetSize(40, 20)
	r, _ = r.Update(refsLoadedMsg{refs: []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, IsHead: true},
	}})
	view := ansi.Strip(r.View())
	for _, header := range []string{"Local branches", "Remote branches", "Tags"} {
		if !strings.Contains(view, header) {
			t.Errorf("view should contain %q, got %q", header, view)
		}
	}
}

func TestRefModelEmptySectionsShowPlaceholder(t *testing.T) {
	r := newRefsModel()
	r.SetSize(40, 20)
	r, _ = r.Update(refsLoadedMsg{refs: nil})
	view := ansi.Strip(r.View())
	if !strings.Contains(view, "(empty)") {
		t.Errorf("empty sections should render '(empty)' placeholder, got %q", view)
	}
	// All three section headers still present even with no refs.
	for _, header := range []string{"Local branches", "Remote branches", "Tags"} {
		if !strings.Contains(view, header) {
			t.Errorf("view should still contain %q when empty, got %q", header, view)
		}
	}
}

func TestRefModelHEADHasStarMarker(t *testing.T) {
	r := newRefsModel()
	r.SetSize(40, 10)
	r, _ = r.Update(refsLoadedMsg{refs: []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, IsHead: true},
		{ShortName: "feat/x", Kind: git.RefKindLocal},
	}})
	view := ansi.Strip(r.View())
	// Find the line for `main` and verify it has the `*` prefix.
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "main") && !strings.Contains(line, "Local") {
			if !strings.Contains(line, "*") {
				t.Errorf("HEAD line should have '*' marker, got %q", line)
			}
		}
		if strings.Contains(line, "feat/x") && strings.Contains(line, "*") {
			t.Errorf("non-HEAD line should not have '*' marker, got %q", line)
		}
	}
}

func TestRefModelTruncatesLongName(t *testing.T) {
	r := newRefsModel()
	r.SetSize(20, 10)
	r, _ = r.Update(refsLoadedMsg{refs: []git.Ref{
		{ShortName: "feat/very-long-branch-name-that-overflows", Kind: git.RefKindLocal},
	}})
	view := ansi.Strip(r.View())
	if !strings.Contains(view, "…") {
		t.Errorf("long name should be truncated with '…', got %q", view)
	}
}

func TestRefModelCursorSkipsHeadersAndEmpty(t *testing.T) {
	r := newRefsModel()
	r.SetSize(40, 20)
	r, _ = r.Update(refsLoadedMsg{refs: []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, IsHead: true},
		{ShortName: "feat/x", Kind: git.RefKindLocal},
		{ShortName: "v1.0", Kind: git.RefKindTag},
	}})
	// Initially cursor at 0 → first selectable = main.
	if got, ok := r.Selected(); !ok || got.ShortName != "main" {
		t.Errorf("Selected at cursor 0 = %+v ok=%v, want main", got, ok)
	}
	// j → feat/x (next local), j → v1.0 (skips Remote section header + empty placeholder + Tags header).
	r, _ = r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if got, _ := r.Selected(); got.ShortName != "feat/x" {
		t.Errorf("after j: Selected = %q, want feat/x", got.ShortName)
	}
	r, _ = r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if got, _ := r.Selected(); got.ShortName != "v1.0" {
		t.Errorf("after jj: Selected = %q, want v1.0 (skipping empty Remote section)", got.ShortName)
	}
	// j past end clamps.
	r, _ = r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if got, _ := r.Selected(); got.ShortName != "v1.0" {
		t.Errorf("j past end should clamp at v1.0, got %q", got.ShortName)
	}
	// G also goes to last.
	r, _ = r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	if r.cursor != 0 {
		t.Errorf("g should jump to first, cursor = %d", r.cursor)
	}
}

func TestRefModelEnterEmitsCheckoutRequestedMsg(t *testing.T) {
	r := newRefsModel()
	r.SetSize(40, 10)
	r, _ = r.Update(refsLoadedMsg{refs: []git.Ref{
		{ShortName: "main", FullName: "refs/heads/main", Kind: git.RefKindLocal, IsHead: true},
	}})
	_, cmd := r.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter on a ref should return a non-nil cmd")
	}
	msg := cmd()
	sel, ok := msg.(refCheckoutRequestedMsg)
	if !ok {
		t.Fatalf("cmd produced %T, want refCheckoutRequestedMsg", msg)
	}
	if sel.ref.FullName != "refs/heads/main" {
		t.Errorf("refCheckoutRequestedMsg.ref.FullName = %q, want refs/heads/main", sel.ref.FullName)
	}
}

func TestRefModelOEmitsSelectedMsg(t *testing.T) {
	r := newRefsModel()
	r.SetSize(40, 10)
	r, _ = r.Update(refsLoadedMsg{refs: []git.Ref{
		{ShortName: "main", FullName: "refs/heads/main", Kind: git.RefKindLocal, IsHead: true},
	}})
	_, cmd := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if cmd == nil {
		t.Fatal("'o' on a ref should return a non-nil cmd (jump-to-tip)")
	}
	msg := cmd()
	sel, ok := msg.(refSelectedMsg)
	if !ok {
		t.Fatalf("cmd produced %T, want refSelectedMsg", msg)
	}
	if sel.ref.FullName != "refs/heads/main" {
		t.Errorf("refSelectedMsg.ref.FullName = %q, want refs/heads/main", sel.ref.FullName)
	}
}

func TestRefModelEnterAndOOnEmptyDoNothing(t *testing.T) {
	r := newRefsModel()
	r.SetSize(40, 10)
	r, _ = r.Update(refsLoadedMsg{refs: nil})
	_, cmd := r.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Errorf("enter with no selectable ref should not emit a cmd, got %v", cmd())
	}
	_, cmd = r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if cmd != nil {
		t.Errorf("'o' with no selectable ref should not emit a cmd, got %v", cmd())
	}
}

func TestRefModelClipsToHeight(t *testing.T) {
	r := newRefsModel()
	const h = 10
	r.SetSize(40, h)
	r, _ = r.Update(refsLoadedMsg{refs: makeRefs(30, 0, 0)})
	view := ansi.Strip(r.View())
	lines := strings.Split(view, "\n")
	if len(lines) > h {
		t.Errorf("View should clip to %d lines, got %d", h, len(lines))
	}
}

func TestRefModelLazyScrollOnJK(t *testing.T) {
	r := newRefsModel()
	const h = 6
	r.SetSize(40, h)
	r, _ = r.Update(refsLoadedMsg{refs: makeRefs(20, 0, 0)})
	// flat-row layout: header(0), local-0(1) .. local-19(20). cursor=N
	// sits at flat-row N+1. The lazy edge bump fires when flat-row reaches
	// yOffset+h = 6, i.e. at cursor=5. So j×4 stays at yOffset=0; the 5th
	// j bumps to 1.
	for i := 0; i < 4; i++ {
		r = pressKey(t, r, "j")
	}
	if r.yOffset != 0 {
		t.Errorf("yOffset after j×4 = %d, want 0 (still inside window)", r.yOffset)
	}
	r = pressKey(t, r, "j")
	if r.yOffset != 1 {
		t.Errorf("yOffset after j×5 = %d, want 1 (lazy bump on edge)", r.yOffset)
	}
}

func TestRefModelLazyScrollOnK(t *testing.T) {
	r := newRefsModel()
	r.SetSize(40, 6)
	r, _ = r.Update(refsLoadedMsg{refs: makeRefs(20, 0, 0)})
	r = pressKey(t, r, "G")
	startOffset := r.yOffset
	// G lands cursor on local-19 (flat-row 20) and parks yOffset so the
	// cursor is at the bottom of the visible window. Walking the cursor up
	// keeps yOffset put until flat-row crosses yOffset-1 — that's cursor=N
	// with flat-row N+1 = yOffset-1, i.e. N = yOffset-2.
	triggerSteps := 19 - (startOffset - 2)
	for i := 0; i < triggerSteps-1; i++ {
		r = pressKey(t, r, "k")
	}
	if r.yOffset != startOffset {
		t.Errorf("yOffset after G then k×%d = %d, want %d (still inside window)", triggerSteps-1, r.yOffset, startOffset)
	}
	r = pressKey(t, r, "k")
	if r.yOffset != startOffset-1 {
		t.Errorf("yOffset after one more k = %d, want %d (lazy -1)", r.yOffset, startOffset-1)
	}
}

func TestRefModelGGoesToTopAndResetsOffset(t *testing.T) {
	r := newRefsModel()
	r.SetSize(40, 6)
	r, _ = r.Update(refsLoadedMsg{refs: makeRefs(20, 0, 0)})
	r = pressKey(t, r, "G")
	if r.yOffset == 0 {
		t.Fatal("G should advance yOffset above 0 when there are enough refs")
	}
	r = pressKey(t, r, "g")
	if r.cursor != 0 {
		t.Errorf("after g: cursor = %d, want 0", r.cursor)
	}
	if r.yOffset != 0 {
		t.Errorf("after g: yOffset = %d, want 0", r.yOffset)
	}
}

func TestRefModelYOffsetResetsAfterReload(t *testing.T) {
	r := newRefsModel()
	r.SetSize(40, 6)
	r, _ = r.Update(refsLoadedMsg{refs: makeRefs(20, 0, 0)})
	r = pressKey(t, r, "G")
	if r.yOffset == 0 {
		t.Fatal("G should advance yOffset above 0 when there are enough refs")
	}
	r.ResetForReload()
	r, _ = r.Update(refsLoadedMsg{refs: makeRefs(20, 0, 0)})
	if r.yOffset != 0 {
		t.Errorf("yOffset should reset to 0 after reload, got %d", r.yOffset)
	}
}

func TestRefModelCursorVisibleAtSmallHeight(t *testing.T) {
	r := newRefsModel()
	r.SetSize(40, 3)
	r, _ = r.Update(refsLoadedMsg{refs: makeRefs(10, 0, 0)})
	r = pressKey(t, r, "G")
	view := ansi.Strip(r.View())
	last := "local-9"
	if !strings.Contains(view, last) {
		t.Errorf("at small height, cursor (%s) must remain visible; got %q", last, view)
	}
}

// assertCursorVisible checks the cursor's flat-row sits inside the visible
// viewport [yOffset, yOffset+height). Used by the section-boundary regression
// suite where exact yOffset values depend on flatRows() layout details.
func assertCursorVisible(t *testing.T, r refModel) {
	t.Helper()
	rows := r.flatRows()
	cursorRow, ok := r.cursorFlatRow(rows)
	if !ok {
		t.Fatalf("no cursor row found")
	}
	if cursorRow < r.yOffset || cursorRow >= r.yOffset+r.height {
		t.Errorf("cursor flat-row %d outside viewport [%d, %d)", cursorRow, r.yOffset, r.yOffset+r.height)
	}
}

func TestRefModelScrollAcrossLocalRemoteBoundary(t *testing.T) {
	r := newRefsModel()
	r.SetSize(40, 4)
	r, _ = r.Update(refsLoadedMsg{refs: makeRefs(3, 3, 0)})
	for i := 0; i < 3; i++ {
		r = pressKey(t, r, "j")
	}
	got, ok := r.Selected()
	if !ok || got.ShortName != "remote-0" {
		t.Fatalf("Selected after j×3 = %+v ok=%v, want remote-0", got, ok)
	}
	assertCursorVisible(t, r)
	r = pressKey(t, r, "k")
	if got, ok := r.Selected(); !ok || got.ShortName != "local-2" {
		t.Fatalf("Selected after k×1 = %+v ok=%v, want local-2", got, ok)
	}
	assertCursorVisible(t, r)
}

func TestRefModelScrollAcrossLocalTagsBoundaryWhenRemoteEmpty(t *testing.T) {
	r := newRefsModel()
	r.SetSize(40, 4)
	r, _ = r.Update(refsLoadedMsg{refs: makeRefs(3, 0, 3)})
	// cursor +1 이지만 flat-row 는 gap+header(R)+empty(R)+gap+header(T) 만큼 +6 점프.
	for i := 0; i < 3; i++ {
		r = pressKey(t, r, "j")
	}
	got, ok := r.Selected()
	if !ok || got.ShortName != "tag-0" {
		t.Fatalf("Selected after j×3 = %+v ok=%v, want tag-0", got, ok)
	}
	assertCursorVisible(t, r)
}

func TestRefModelCursorStaysVisibleAtSmallHeightAcrossBoundary(t *testing.T) {
	r := newRefsModel()
	r.SetSize(40, 3)
	r, _ = r.Update(refsLoadedMsg{refs: makeRefs(2, 2, 0)})
	for i := 0; i < 3; i++ {
		r = pressKey(t, r, "j")
		assertCursorVisible(t, r)
	}
	if got, ok := r.Selected(); !ok || got.ShortName != "remote-1" {
		t.Fatalf("Selected after j×3 = %+v ok=%v, want remote-1", got, ok)
	}
}

func TestRefModelScrollKeepsSectionHeaderAboveCursor(t *testing.T) {
	r := newRefsModel()
	r.SetSize(40, 4)
	r, _ = r.Update(refsLoadedMsg{refs: makeRefs(5, 5, 0)})
	r = pressKey(t, r, "G")
	for i := 0; i < 4; i++ {
		r = pressKey(t, r, "k")
	}
	if got, ok := r.Selected(); !ok || got.ShortName != "remote-0" {
		t.Fatalf("Selected after G then k×4 = %+v ok=%v, want remote-0", got, ok)
	}
	if view := ansi.Strip(r.View()); !strings.Contains(view, "Remote branches") {
		t.Errorf("Remote branches header must be visible when cursor on remote-0; got:\n%s", view)
	}
	for i := 0; i < 5; i++ {
		r = pressKey(t, r, "k")
	}
	if got, ok := r.Selected(); !ok || got.ShortName != "local-0" {
		t.Fatalf("Selected after G then k×9 = %+v ok=%v, want local-0", got, ok)
	}
	if view := ansi.Strip(r.View()); !strings.Contains(view, "Local branches") {
		t.Errorf("Local branches header must be visible when cursor on local-0; got:\n%s", view)
	}
}

func TestRefModelKBackwardAcrossBoundariesPreservesLazyAndJumps(t *testing.T) {
	r := newRefsModel()
	r.SetSize(40, 4)
	r, _ = r.Update(refsLoadedMsg{refs: makeRefs(3, 3, 3)})
	r = pressKey(t, r, "G")
	startOffset := r.yOffset
	// 처음 두 k 는 Tags 섹션 내 — lazy invariant.
	for i := 0; i < 2; i++ {
		r = pressKey(t, r, "k")
		if r.yOffset != startOffset {
			t.Errorf("yOffset after G then k×%d = %d, want %d (lazy in-section)", i+1, r.yOffset, startOffset)
		}
		assertCursorVisible(t, r)
	}
	beforeOffset := r.yOffset
	r = pressKey(t, r, "k")
	if r.yOffset >= beforeOffset {
		t.Errorf("yOffset after boundary-crossing k did not decrease: before=%d after=%d", beforeOffset, r.yOffset)
	}
	assertCursorVisible(t, r)
	for i := 0; i < 5; i++ {
		r = pressKey(t, r, "k")
		assertCursorVisible(t, r)
	}
	if got, ok := r.Selected(); !ok || got.ShortName != "local-0" {
		t.Fatalf("Selected after G then k all the way = %+v ok=%v, want local-0", got, ok)
	}
}
