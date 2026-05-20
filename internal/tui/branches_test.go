package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// TestBKeyOpensBranchesModal locks in the global b entry into the modal
// regardless of focused pane.
func TestBKeyOpensBranchesModal(t *testing.T) {
	m := initSized(t)
	m = seedRefs(t, m, []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, IsHead: true},
		{ShortName: "feat/foo", Kind: git.RefKindLocal},
	})
	m.focused = paneGraph // any pane works

	m, _ = pressRune(t, m, 'b')
	if m.mode != viewModeBranchesModal {
		t.Errorf("b should enter viewModeBranchesModal, got %v", m.mode)
	}
}

// TestBranchesModalCursorLandsOnHEAD verifies the modal opens with cursor
// on the current branch so `d` immediately on entry is the obvious
// rejection case.
func TestBranchesModalCursorLandsOnHEAD(t *testing.T) {
	m := initSized(t)
	m = seedRefs(t, m, []git.Ref{
		{ShortName: "feat/foo", Kind: git.RefKindLocal},
		{ShortName: "main", Kind: git.RefKindLocal, IsHead: true},
		{ShortName: "feat/bar", Kind: git.RefKindLocal},
	})

	m, _ = pressRune(t, m, 'b')
	if m.branchesModal.cursor != 1 {
		t.Errorf("cursor = %d, want 1 (HEAD index)", m.branchesModal.cursor)
	}
}

// TestBranchesModalRejectsEmptyLocals — no local branches means there is
// nothing to act on; the modal must not open.
func TestBranchesModalRejectsEmptyLocals(t *testing.T) {
	m := initSized(t)
	m = seedRefs(t, m, []git.Ref{
		{ShortName: "origin/main", Kind: git.RefKindRemote},
	})

	m, _ = pressRune(t, m, 'b')
	if m.mode == viewModeBranchesModal {
		t.Errorf("empty locals should not open modal, mode = %v", m.mode)
	}
	if m.status == "" {
		t.Error("empty-locals rejection should set status")
	}
}

// TestBranchesModalCursorBounds — j past the end clamps to last index, k
// past 0 clamps to 0.
func TestBranchesModalCursorBounds(t *testing.T) {
	m := initSized(t)
	m = seedRefs(t, m, []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, IsHead: true},
		{ShortName: "feat/a", Kind: git.RefKindLocal},
		{ShortName: "feat/b", Kind: git.RefKindLocal},
	})
	m, _ = pressRune(t, m, 'b') // opens at HEAD (cursor=0)

	// j × 5: clamps at len-1 = 2
	for i := 0; i < 5; i++ {
		m, _ = pressRune(t, m, 'j')
	}
	if m.branchesModal.cursor != 2 {
		t.Errorf("after j×5 cursor = %d, want 2", m.branchesModal.cursor)
	}

	// k × 10: clamps at 0
	for i := 0; i < 10; i++ {
		m, _ = pressRune(t, m, 'k')
	}
	if m.branchesModal.cursor != 0 {
		t.Errorf("after k×10 cursor = %d, want 0", m.branchesModal.cursor)
	}
}

// TestBranchesModalDeleteOnHEADRejected — d on HEAD inside the modal must
// match refs-pane d's HEAD rejection (the modal is the new entry, same rule).
func TestBranchesModalDeleteOnHEADRejected(t *testing.T) {
	m := initSized(t)
	m = seedRefs(t, m, []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, IsHead: true},
		{ShortName: "feat/foo", Kind: git.RefKindLocal},
	})
	m, _ = pressRune(t, m, 'b') // cursor = 0 (HEAD)
	m, _ = pressRune(t, m, 'd')

	if m.mode == viewModeRefDeleteConfirm {
		t.Error("d on HEAD should not enter delete-confirm")
	}
	if m.status == "" || m.pendingRefDelete.localName != "" {
		t.Errorf("expected HEAD rejection: status=%q pendingRefDelete=%+v",
			m.status, m.pendingRefDelete)
	}
}

// TestBranchesModalDeleteArmsConfirm — d on a non-HEAD local branch arms
// pendingRefDelete + enters viewModeRefDeleteConfirm. This is the integration
// proof that the modal shares the delete-branch chain with refs-pane d.
func TestBranchesModalDeleteArmsConfirm(t *testing.T) {
	m := initSized(t)
	m = seedRefs(t, m, []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, IsHead: true},
		{ShortName: "feat/foo", Kind: git.RefKindLocal},
	})
	m, _ = pressRune(t, m, 'b')
	m, _ = pressRune(t, m, 'j') // cursor → feat/foo
	m, _ = pressRune(t, m, 'd')

	if m.mode != viewModeRefDeleteConfirm {
		t.Errorf("mode = %v, want viewModeRefDeleteConfirm", m.mode)
	}
	if m.pendingRefDelete.localName != "feat/foo" {
		t.Errorf("pendingRefDelete.localName = %q, want feat/foo",
			m.pendingRefDelete.localName)
	}
}

// TestBranchesModalEscClosesModal — esc returns to viewModeNormal and
// resets the modal state (cursor 0).
func TestBranchesModalEscClosesModal(t *testing.T) {
	m := initSized(t)
	m = seedRefs(t, m, []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, IsHead: true},
		{ShortName: "feat/a", Kind: git.RefKindLocal},
	})
	m, _ = pressRune(t, m, 'b')
	m, _ = pressRune(t, m, 'j')
	if m.branchesModal.cursor != 1 {
		t.Fatalf("setup: cursor = %d, want 1", m.branchesModal.cursor)
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)

	if m.mode != viewModeNormal {
		t.Errorf("after esc mode = %v, want viewModeNormal", m.mode)
	}
	if m.branchesModal.cursor != 0 {
		t.Errorf("after esc cursor = %d, want 0 (reset)", m.branchesModal.cursor)
	}
}

// TestBranchesModalRenderHEADMarker — view shows the HEAD branch with the
// `←` glyph appended. Lightweight smoke test of the renderer.
func TestBranchesModalRenderHEADMarker(t *testing.T) {
	m := initSized(t)
	m = seedRefs(t, m, []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, IsHead: true},
		{ShortName: "feat/a", Kind: git.RefKindLocal},
	})
	m, _ = pressRune(t, m, 'b')

	inner := m.renderBranchesModalInner()
	if !strings.Contains(inner, "main") || !strings.Contains(inner, "←") {
		t.Errorf("modal view missing HEAD marker: %q", inner)
	}
	if !strings.Contains(inner, "feat/a") {
		t.Errorf("modal view missing non-HEAD branch: %q", inner)
	}
}
