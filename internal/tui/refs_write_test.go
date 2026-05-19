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

// pressRune dispatches a single-rune key message.
func pressRune(t *testing.T, m Model, r rune) (Model, tea.Cmd) {
	t.Helper()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	return updated.(Model), cmd
}

// TestDKeyOnGraphFocusOpensPatchOverlay locks in the existing behavior so
// the refs-focus override doesn't leak into other panes.
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

// TestDKeyOnRefsFocusOpensInlineConfirm is the gating regression: refs
// focus reinterprets `d` as the inline delete confirm.
func TestDKeyOnRefsFocusOpensInlineConfirm(t *testing.T) {
	m := initSized(t)
	m = seedRefs(t, m, []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, IsHead: true},
		{ShortName: "feat/foo", Kind: git.RefKindLocal},
	})
	m.focused = paneRefs
	m.refs.cursor = 1 // feat/foo

	m, _ = pressRune(t, m, 'd')
	if m.mode != viewModeRefDeleteConfirm {
		t.Errorf("refs-focus d should enter viewModeRefDeleteConfirm, got %v", m.mode)
	}
	if m.pendingRefDelete.localName != "feat/foo" {
		t.Errorf("pendingRefDelete.localName = %q, want feat/foo", m.pendingRefDelete.localName)
	}
}

func TestDKeyOnRefsFocusHEADBranchRejected(t *testing.T) {
	m := initSized(t)
	m = seedRefs(t, m, []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, IsHead: true},
	})
	m.focused = paneRefs
	m.refs.cursor = 0
	m, _ = pressRune(t, m, 'd')
	if m.mode == viewModeRefDeleteConfirm {
		t.Error("inline confirm should not open for HEAD branch")
	}
	if !strings.Contains(m.status, "current branch") {
		t.Errorf("status = %q, want 'current branch'", m.status)
	}
}

func TestDKeyOnRefsFocusTagRejected(t *testing.T) {
	m := initSized(t)
	m = seedRefs(t, m, []git.Ref{
		{ShortName: "v1.0", Kind: git.RefKindTag},
	})
	m.focused = paneRefs
	m.refs.cursor = 0
	m, _ = pressRune(t, m, 'd')
	if m.mode == viewModeRefDeleteConfirm {
		t.Error("inline confirm should not open for a tag")
	}
	if !strings.Contains(m.status, "local branches only") {
		t.Errorf("status = %q, want 'local branches only'", m.status)
	}
}

func TestDKeyOnRefsFocusRemoteRejected(t *testing.T) {
	m := initSized(t)
	m = seedRefs(t, m, []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, IsHead: true},
		{ShortName: "origin/feat", Kind: git.RefKindRemote},
	})
	m.focused = paneRefs
	m.refs.cursor = 1
	m, _ = pressRune(t, m, 'd')
	if m.mode == viewModeRefDeleteConfirm {
		t.Error("inline confirm should not open for a remote-tracking ref")
	}
	if !strings.Contains(m.status, "local branches only") {
		t.Errorf("status = %q, want 'local branches only'", m.status)
	}
}

// E6 regression: the inline delete confirm accepts only y / Y / esc.
// The keystroke sequence below is the documented user flow — without it,
// a future re-introduction of `f` / `F` / arbitrary keys would silently
// dispatch (or be silently no-op) instead of being caught by review.
func TestDeleteInlineConfirmKeystrokeSequence(t *testing.T) {
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
		m.focused = paneRefs
		m.refs.cursor = 1
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
		// Prompt stays visible during dispatch — the success msg is what
		// closes it. The user sees a stable "deleting…" status until the
		// reload kicks in.
		if m.mode != viewModeRefDeleteConfirm {
			t.Errorf("y should keep confirm visible during dispatch, mode = %v", m.mode)
		}
		if !m.refActionInFlight {
			t.Error("y should arm refActionInFlight")
		}
		if !strings.Contains(m.status, "deleting 'feat/foo'") {
			t.Errorf("status = %q, want 'deleting feat/foo…'", m.status)
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
		if !strings.Contains(m.status, "(forced)") {
			t.Errorf("status = %q, want it to mention '(forced)'", m.status)
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

func TestDeleteInlineConfirmYRetryAfterNotMerged(t *testing.T) {
	// Press `y`, get back branchDeleteNotMergedMsg, then press `Y` to retry
	// with force. This mirrors the user-facing flow: the inline prompt
	// re-arms with a "press [Y] to force" hint after the not-merged sentinel.
	var attempts int
	var lastForce bool
	withRefsActionStubs(t, refsActionStubs{
		branchDelete: func(_ context.Context, _, _ string, force bool) error {
			attempts++
			lastForce = force
			if !force {
				return fmt.Errorf("git branch -d: %w", git.ErrBranchNotFullyMerged)
			}
			return nil
		},
	})
	m := initSized(t)
	m = seedRefs(t, m, []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, IsHead: true},
		{ShortName: "feat/foo", Kind: git.RefKindLocal},
	})
	m.focused = paneRefs
	m.refs.cursor = 1
	m, _ = pressRune(t, m, 'd')

	// First attempt: y → not merged, prompt re-arms with [Y] hint.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("y should dispatch")
	}
	updated, _ := m.Update(cmd())
	m = updated.(Model)
	if m.mode != viewModeRefDeleteConfirm {
		t.Errorf("not-merged should re-arm confirm, mode = %v", m.mode)
	}
	if m.pendingRefDelete.localName != "feat/foo" {
		t.Errorf("pendingRefDelete cleared early: %+v", m.pendingRefDelete)
	}
	if !strings.Contains(m.status, "press [Y] to force") {
		t.Errorf("status = %q, want 'press [Y] to force' hint", m.status)
	}

	// Second attempt: Y → force succeeds.
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Y'}})
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

func TestBranchDeleteSucceededReloadsAndArmsCursor(t *testing.T) {
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
	if m.pendingRefCursorAfterDelete.name != "feat/foo" {
		t.Errorf("pendingRefCursorAfterDelete = %+v, want feat/foo local", m.pendingRefCursorAfterDelete)
	}
	if m.pendingRefCursorAfterDelete.kind != git.RefKindLocal {
		t.Errorf("pendingRefCursorAfterDelete kind = %v, want local", m.pendingRefCursorAfterDelete.kind)
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
