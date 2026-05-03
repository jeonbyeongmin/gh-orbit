package tui

import (
	"strings"
	"testing"

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

func TestRefModelEmptyAfterLoad(t *testing.T) {
	r := newRefsModel()
	r, _ = r.Update(refsLoadedMsg{refs: nil})
	if got := r.View(); got != "(no refs)" {
		t.Errorf("empty view = %q, want %q", got, "(no refs)")
	}
}

func TestRefModelFlatRenderListsShortNames(t *testing.T) {
	r := newRefsModel()
	r.SetSize(40, 10)
	r, _ = r.Update(refsLoadedMsg{refs: []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, IsHead: true},
		{ShortName: "origin/feat", Kind: git.RefKindRemote},
	}})
	view := r.View()
	if !strings.Contains(view, "main") || !strings.Contains(view, "origin/feat") {
		t.Errorf("view should list both ref short names, got %q", view)
	}
}
