package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// stubRebase swaps rebaseExec for one test, recording the onto argument.
func stubRebase(t *testing.T, ret error) *string {
	t.Helper()
	prev := rebaseExec
	var onto string
	rebaseExec = func(_ context.Context, _, o string) error {
		onto = o
		return ret
	}
	t.Cleanup(func() { rebaseExec = prev })
	return &onto
}

func rebaseFixture(t *testing.T) Model {
	t.Helper()
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m = seedGraphCursor(t, m, "cursor99")
	m = seedRefs(t, m, []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, ObjectName: "headhash", IsHead: true},
		{ShortName: "develop", Kind: git.RefKindLocal, ObjectName: "cursor99"},
	})
	return m
}

func TestRebaseKeyArmsConfirmWithChipLabel(t *testing.T) {
	m := rebaseFixture(t)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	m = updated.(Model)
	if m.mode != viewModeRebaseConfirm {
		t.Fatalf("R should arm viewModeRebaseConfirm, mode = %v", m.mode)
	}
	if m.pendingRebase.onto != "cursor99" || m.pendingRebase.branch != "main" {
		t.Errorf("pendingRebase = %+v, want onto=cursor99 branch=main", m.pendingRebase)
	}
	if m.pendingRebase.label != "develop" {
		t.Errorf("label should prefer the local chip, got %q", m.pendingRebase.label)
	}
	dialog := m.renderRebaseConfirmInner()
	if !strings.Contains(dialog, "rebase main onto develop?") {
		t.Errorf("confirm dialog should name the chain, got %q", dialog)
	}
}

func TestRebaseKeyRejectsDetachedAndHeadCursor(t *testing.T) {
	// Detached: refs without IsHead.
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m = seedGraphCursor(t, m, "cursor99")
	m = seedRefs(t, m, []git.Ref{{ShortName: "main", Kind: git.RefKindLocal, ObjectName: "x"}})
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	m = updated.(Model)
	if m.mode == viewModeRebaseConfirm {
		t.Error("detached HEAD should not arm the confirm")
	}
	if !strings.Contains(m.status, "detached") {
		t.Errorf("status should explain detached rejection, got %q", m.status)
	}

	// Cursor on HEAD itself: no-op with status.
	m2 := New()
	updated, _ = m2.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m2 = updated.(Model)
	m2 = seedGraphCursor(t, m2, "headhash")
	m2 = seedRefs(t, m2, []git.Ref{{ShortName: "main", Kind: git.RefKindLocal, ObjectName: "headhash", IsHead: true}})
	updated, _ = m2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	m2 = updated.(Model)
	if m2.mode == viewModeRebaseConfirm {
		t.Error("cursor==HEAD should not arm the confirm")
	}
}

func TestRebaseConfirmYDispatchesAndEscCancels(t *testing.T) {
	onto := stubRebase(t, nil)
	m := rebaseFixture(t)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	m = updated.(Model)

	// esc cancels.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	esc := updated.(Model)
	if esc.mode != viewModeNormal || esc.pendingRebase != (pendingRebase{}) {
		t.Errorf("esc should cancel and reset, mode=%v pending=%+v", esc.mode, esc.pendingRebase)
	}

	// y dispatches rebaseCmd against the cursor hash.
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = updated.(Model)
	if !m.rebaseInFlight {
		t.Error("y should latch rebaseInFlight")
	}
	if cmd == nil {
		t.Fatal("y should return rebaseCmd")
	}
	if msg := cmd(); msg != (rebaseSucceededMsg{}) {
		t.Fatalf("stubbed success should yield rebaseSucceededMsg, got %T", msg)
	}
	if *onto != "cursor99" {
		t.Errorf("rebase should target the cursor hash, got %q", *onto)
	}
}

func TestRebaseOutcomeHandlers(t *testing.T) {
	m := rebaseFixture(t)
	m.rebaseInFlight = true
	m.pendingRebase = pendingRebase{onto: "cursor99", label: "develop", branch: "main"}

	updated, cmd := m.Update(rebaseSucceededMsg{})
	ok := updated.(Model)
	if ok.rebaseInFlight || ok.pendingRebase != (pendingRebase{}) {
		t.Error("success should clear in-flight + pending state")
	}
	if !strings.Contains(ok.status, "rebase: done") || cmd == nil {
		t.Errorf("success should report + reload, status=%q", ok.status)
	}

	m.rebaseInFlight = true
	updated, cmd = m.Update(rebaseConflictMsg{err: errors.New("CONFLICT")})
	conflict := updated.(Model)
	if !strings.Contains(conflict.status, "resolve in Local Changes") || cmd == nil {
		t.Errorf("conflict should point to Local Changes + reload, status=%q", conflict.status)
	}

	m.rebaseInFlight = true
	updated, _ = m.Update(rebaseFailedMsg{err: errors.New("cannot rebase: unstaged changes")})
	failed := updated.(Model)
	if !strings.Contains(failed.status, "rebase failed") {
		t.Errorf("failure should surface git's reason, status=%q", failed.status)
	}
}

func TestRebaseCmdClassifiesConflict(t *testing.T) {
	stubRebase(t, git.ErrRebaseConflict)
	cmd := rebaseCmd("/r", "abc")
	if _, ok := cmd().(rebaseConflictMsg); !ok {
		t.Error("ErrRebaseConflict should map to rebaseConflictMsg")
	}
	stubRebase(t, errors.New("boom"))
	cmd = rebaseCmd("/r", "abc")
	if _, ok := cmd().(rebaseFailedMsg); !ok {
		t.Error("generic error should map to rebaseFailedMsg")
	}
}
