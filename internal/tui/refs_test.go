package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

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
	view := stripANSI(r.View())
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
	view := stripANSI(r.View())
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
	view := stripANSI(r.View())
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
	view := stripANSI(r.View())
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

func TestRefModelEnterEmitsSelectedMsg(t *testing.T) {
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
	sel, ok := msg.(refSelectedMsg)
	if !ok {
		t.Fatalf("cmd produced %T, want refSelectedMsg", msg)
	}
	if sel.ref.FullName != "refs/heads/main" {
		t.Errorf("refSelectedMsg.ref.FullName = %q, want refs/heads/main", sel.ref.FullName)
	}
}

func TestRefModelEnterOnEmptyDoesNothing(t *testing.T) {
	r := newRefsModel()
	r.SetSize(40, 10)
	r, _ = r.Update(refsLoadedMsg{refs: nil})
	_, cmd := r.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Errorf("enter with no selectable ref should not emit a cmd, got %v", cmd())
	}
}

// stripANSI removes ANSI escape sequences so tests can search rendered text
// without worrying about lipgloss color codes.
func stripANSI(s string) string {
	var b strings.Builder
	in := false
	for _, c := range s {
		if c == 0x1b {
			in = true
			continue
		}
		if in {
			if c == 'm' {
				in = false
			}
			continue
		}
		b.WriteRune(c)
	}
	return b.String()
}
