package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// withStaged seeds one staged entry so StagedCount() passes the commit gate.
func withStaged(m Model) Model {
	m.localChanges.entries = []localChangesEntry{
		{Path: "f.txt", Section: sectionStaged, IndexState: 'M'},
	}
	return m
}

// TestCommitFlow walks the happy path: `c` opens the modal, an empty message is
// refused inline, and a typed message dispatches the commit + closes the modal
// on success with a graph reload.
func TestCommitFlow(t *testing.T) {
	prev := commitExec
	t.Cleanup(func() { commitExec = prev })
	var gotMsg string
	commitExec = func(_ context.Context, _, msg string) error {
		gotMsg = msg
		return nil
	}

	m := withStaged(enterLocalChanges(t, initSized(t)))

	m, _ = pressRune(t, m, 'c')
	if !m.commitInput.open {
		t.Fatal("c should open the commit modal")
	}
	if m.mode != viewModeLocalChanges {
		t.Fatalf("commit modal must stay in viewModeLocalChanges, got %v", m.mode)
	}

	// Empty message → inline error, modal stays.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.commitInput.inlineErr == "" || !m.commitInput.open {
		t.Fatal("empty message should set inline error and stay open")
	}

	// Type a message and dispatch.
	for _, r := range "fix bug" {
		m, _ = pressRune(t, m, r)
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if !m.commitInput.inFlight || cmd == nil {
		t.Fatal("enter with a message should dispatch commitCmd")
	}
	msg := cmd()
	if gotMsg != "fix bug" {
		t.Errorf("commit message = %q, want %q", gotMsg, "fix bug")
	}

	updated, reload := m.Update(msg)
	m = updated.(Model)
	if m.commitInput.open || m.commitInput.inFlight {
		t.Errorf("success should close the modal, open=%v inFlight=%v", m.commitInput.open, m.commitInput.inFlight)
	}
	if m.status != "committed" || reload == nil {
		t.Errorf("success should report + reload, status=%q reload=%v", m.status, reload != nil)
	}
	if m.pendingHEADHash != pendingHEADSentinel {
		t.Errorf("success should arm the HEAD-jump, pendingHEADHash=%q", m.pendingHEADHash)
	}
}

// TestCommitEmptyIndexRejected confirms `c` with nothing staged surfaces a
// status line instead of opening a modal that would let git fail.
func TestCommitEmptyIndexRejected(t *testing.T) {
	m := enterLocalChanges(t, initSized(t)) // no staged entries
	m, _ = pressRune(t, m, 'c')
	if m.commitInput.open {
		t.Fatal("c with an empty index should not open the modal")
	}
	if !strings.Contains(m.status, "nothing staged") {
		t.Errorf("status = %q, want 'nothing staged'", m.status)
	}
}

// TestCommitFailureStaysInModal confirms a git refusal (rejecting hook, signing
// failure) keeps the modal open with the message inline so the user can retry.
func TestCommitFailureStaysInModal(t *testing.T) {
	m := withStaged(enterLocalChanges(t, initSized(t)))
	m, _ = pressRune(t, m, 'c')
	m.commitInput.inFlight = true
	updated, _ := m.Update(commitFailedMsg{err: errors.New("hook declined the commit")})
	m = updated.(Model)
	if !m.commitInput.open || m.commitInput.inFlight {
		t.Fatalf("failure should stay open and idle, open=%v inFlight=%v", m.commitInput.open, m.commitInput.inFlight)
	}
	if !strings.Contains(m.commitInput.inlineErr, "hook declined") {
		t.Errorf("inline error = %q, want git's message", m.commitInput.inlineErr)
	}
}

// TestCommitEscCancels confirms esc backs out of the modal without committing.
func TestCommitEscCancels(t *testing.T) {
	prev := commitExec
	t.Cleanup(func() { commitExec = prev })
	called := false
	commitExec = func(_ context.Context, _, _ string) error { called = true; return nil }

	m := withStaged(enterLocalChanges(t, initSized(t)))
	m, _ = pressRune(t, m, 'c')
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.commitInput.open {
		t.Fatal("esc should close the modal")
	}
	if called {
		t.Fatal("esc must not dispatch a commit")
	}
}
