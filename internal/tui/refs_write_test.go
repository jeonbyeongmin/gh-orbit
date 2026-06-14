package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// initSized returns a Model with the window size pre-set so paneSizes
// produces non-zero dimensions (some handlers early-return on width==0).
func initSized(t *testing.T) Model {
	t.Helper()
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	return updated.(Model)
}

// pressRune dispatches a single-rune key message into the Model layer.
func pressRune(t *testing.T, m Model, r rune) (Model, tea.Cmd) {
	t.Helper()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	return updated.(Model), cmd
}

// pressTab / pressShiftTab dispatch the page-cycle keys. From the graph,
// tab advances to the Worktree page and shift+tab to Local Changes.
func pressTab(t *testing.T, m Model) (Model, tea.Cmd) {
	t.Helper()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	return updated.(Model), cmd
}

func pressShiftTab(t *testing.T, m Model) (Model, tea.Cmd) {
	t.Helper()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	return updated.(Model), cmd
}

// TestDKeyOnGraphFocusOpensPatchOverlay locks in the graph-focus `d`
// override: instead of triggering a delete, it loads the patch overlay
// for the commit under the graph cursor. The branches modal's `d` is a
// completely separate code path (see branches_test.go).
func TestDKeyOnGraphFocusOpensPatchOverlay(t *testing.T) {
	m := initSized(t)
	m = seedRefs(t, m, []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, IsHead: true},
	})
	m = seedGraphCursor(t, m, "abc1234")
	m.focused = paneGraph

	m, _ = pressRune(t, m, 'd')
	if m.mode != viewModeDiffWindow {
		t.Errorf("graph-focus d should open patch overlay, mode = %v", m.mode)
	}
}

// TestDeleteConfirmKeystrokeSequence pins the user-facing keystroke
// matrix of the branch-delete confirm dialog. After the refs-LIST subtract
// the only entry into this confirm is the branches modal — open it with
// `b`, move to a non-HEAD branch, and press `d` to arm.
func TestDeleteConfirmKeystrokeSequence(t *testing.T) {
	open := func(t *testing.T) (Model, *bool, *string) {
		t.Helper()
		called, callForce := false, false
		var seenName string
		withRefsActionStubs(t, refsActionStubs{
			branchDelete: func(_ context.Context, _, name string, force bool) error {
				called, callForce = true, force
				seenName = name
				return nil
			},
		})
		m := initSized(t)
		m = seedRefs(t, m, []git.Ref{
			{ShortName: "main", Kind: git.RefKindLocal, IsHead: true},
			{ShortName: "feat/foo", Kind: git.RefKindLocal},
		})
		// b → modal opens at HEAD (main, cursor=0). j → feat/foo. d → arm.
		m, _ = pressRune(t, m, 'b')
		m, _ = pressRune(t, m, 'j')
		m, _ = pressRune(t, m, 'd')
		_ = called
		_ = callForce
		return m, &callForce, &seenName
	}

	t.Run("esc cancels", func(t *testing.T) {
		m, _, _ := open(t)
		updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		m = updated.(Model)
		if cmd != nil {
			t.Errorf("esc should not dispatch a cmd, got %v", cmd)
		}
		if m.mode != viewModeNormal {
			t.Errorf("esc should exit confirm, mode = %v", m.mode)
		}
		if !strings.Contains(m.status, "cancelled") {
			t.Errorf("status = %q, want 'cancelled'", m.status)
		}
		if (m.pendingRefDelete != refDeleteState{}) {
			t.Errorf("pendingRefDelete should clear on cancel, got %+v", m.pendingRefDelete)
		}
	})

	t.Run("y dispatches safe delete", func(t *testing.T) {
		m, force, seenName := open(t)
		updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
		m = updated.(Model)
		if m.mode != viewModeRefDeleteConfirm {
			t.Errorf("y should keep confirm visible during dispatch, mode = %v", m.mode)
		}
		if !m.refActionInFlight {
			t.Error("y should arm refActionInFlight")
		}
		if !strings.Contains(m.renderRefDeleteConfirmInner(), "deleting…") {
			t.Errorf("dialog = %q, want in-flight 'deleting…' hint", m.renderRefDeleteConfirmInner())
		}
		if cmd == nil {
			t.Fatal("y should dispatch branchDeleteCmd")
		}
		cmd()
		if *force {
			t.Error("y should pass force=false to branchDeleteExec")
		}
		if *seenName != "feat/foo" {
			t.Errorf("delete called with %q, want feat/foo", *seenName)
		}
	})

	t.Run("Y dispatches force delete", func(t *testing.T) {
		m, force, seenName := open(t)
		updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Y'}})
		m = updated.(Model)
		if m.mode != viewModeRefDeleteConfirm {
			t.Errorf("Y should keep confirm visible during dispatch, mode = %v", m.mode)
		}
		if !m.refActionInFlight {
			t.Error("Y should arm refActionInFlight")
		}
		if cmd == nil {
			t.Fatal("Y should dispatch branchDeleteCmd")
		}
		cmd()
		if !*force {
			t.Error("Y should pass force=true to branchDeleteExec")
		}
		if *seenName != "feat/foo" {
			t.Errorf("delete called with %q, want feat/foo", *seenName)
		}
	})

	t.Run("keys gated while delete in flight", func(t *testing.T) {
		m, _, _ := open(t)
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
		m = updated.(Model)
		// In flight: a second y must not re-dispatch, esc must not close.
		for _, k := range []tea.KeyMsg{
			{Type: tea.KeyRunes, Runes: []rune{'y'}},
			{Type: tea.KeyRunes, Runes: []rune{'Y'}},
			{Type: tea.KeyEsc},
		} {
			updated, cmd := m.Update(k)
			m = updated.(Model)
			if cmd != nil {
				t.Errorf("key %v should be gated in flight, dispatched a cmd", k)
			}
			if m.mode != viewModeRefDeleteConfirm {
				t.Errorf("key %v should keep the dialog open, mode = %v", k, m.mode)
			}
		}
	})

	t.Run("other keys swallowed", func(t *testing.T) {
		m, _, _ := open(t)
		for _, k := range []tea.KeyMsg{
			{Type: tea.KeyRunes, Runes: []rune{'f'}},
			{Type: tea.KeyRunes, Runes: []rune{'F'}},
			{Type: tea.KeyRunes, Runes: []rune{'n'}},
			{Type: tea.KeyRunes, Runes: []rune{'m'}},
			{Type: tea.KeyTab},
			{Type: tea.KeyEnter},
		} {
			updated, cmd := m.Update(k)
			m = updated.(Model)
			if m.mode != viewModeRefDeleteConfirm {
				t.Errorf("key %v should stay in confirm, mode = %v", k, m.mode)
			}
			if cmd != nil {
				t.Errorf("key %v should not dispatch a cmd, got %v", k, cmd)
			}
		}
	})
}

func TestDeleteConfirmYRetryAfterNotMerged(t *testing.T) {
	// Press `y`, get back branchDeleteNotMergedMsg, then press `Y` to retry
	// with force. Mirrors the user-facing flow: the confirm dialog re-arms
	// with a "press [Y] to force" row after the not-merged sentinel.
	var attempts int
	var lastForce bool
	withRefsActionStubs(t, refsActionStubs{
		branchDelete: func(_ context.Context, _, _ string, force bool) error {
			attempts++
			lastForce = force
			if !force {
				return fmt.Errorf("wrapped: %w", git.ErrBranchNotFullyMerged)
			}
			return nil
		},
	})
	m := initSized(t)
	m = seedRefs(t, m, []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, IsHead: true},
		{ShortName: "feat/foo", Kind: git.RefKindLocal},
	})
	m, _ = pressRune(t, m, 'b')
	m, _ = pressRune(t, m, 'j')
	m, _ = pressRune(t, m, 'd')

	// First y: safe-delete attempt → not-merged.
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("first y should dispatch")
	}
	updated, _ = m.Update(cmd())
	m = updated.(Model)
	if m.mode != viewModeRefDeleteConfirm {
		t.Errorf("after not-merged the dialog should stay open, mode = %v", m.mode)
	}
	if !m.pendingRefDelete.notMerged {
		t.Error("not-merged reply should flag pendingRefDelete.notMerged")
	}
	if !strings.Contains(m.renderRefDeleteConfirmInner(), "not fully merged") {
		t.Errorf("dialog = %q, want 'not fully merged' row", m.renderRefDeleteConfirmInner())
	}

	// Second Y: force-delete.
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Y'}})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("Y should dispatch")
	}
	cmd()
	if attempts != 2 {
		t.Errorf("attempts = %d, want 2 (one safe + one force)", attempts)
	}
	if !lastForce {
		t.Error("second attempt should be force=true")
	}
}

func TestBranchDeleteSucceededReloadsAndClearsState(t *testing.T) {
	m := initSized(t)
	m = seedRefs(t, m, []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, IsHead: true},
	})
	m.refActionInFlight = true
	updated, cmd := m.Update(branchDeleteSucceededMsg{localName: "feat/foo"})
	m = updated.(Model)
	if m.refActionInFlight {
		t.Error("refActionInFlight should clear on success")
	}
	if !strings.Contains(m.status, "deleted 'feat/foo'") {
		t.Errorf("status = %q", m.status)
	}
	if cmd == nil {
		t.Error("success should dispatch reloadCmd")
	}
}

func TestBranchDeleteForcedSucceededShowsForcedSuffix(t *testing.T) {
	m := initSized(t)
	updated, _ := m.Update(branchDeleteSucceededMsg{localName: "x", forced: true})
	m = updated.(Model)
	if !strings.Contains(m.status, "(forced)") {
		t.Errorf("status = %q, want '(forced)' suffix", m.status)
	}
}

func TestBranchDeleteFailedSurfacesError(t *testing.T) {
	m := initSized(t)
	updated, _ := m.Update(branchDeleteFailedMsg{
		err:       errors.New("lock held"),
		localName: "feat/foo",
	})
	m = updated.(Model)
	if !strings.Contains(m.status, "delete failed") {
		t.Errorf("status = %q, want 'delete failed'", m.status)
	}
	if !strings.Contains(m.status, "lock held") {
		t.Errorf("status = %q, want it to include stderr", m.status)
	}
}
