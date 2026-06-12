package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// --- push (`P`) ---

func TestPushKeyDispatchesForHeadBranch(t *testing.T) {
	prev := pushExec
	t.Cleanup(func() { pushExec = prev })
	var gotBranch string
	pushExec = func(_ context.Context, _, branch string) error {
		gotBranch = branch
		return nil
	}

	m := rebaseFixture(t) // HEAD on main, cursor on cursor99
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'P'}})
	m = updated.(Model)
	if !m.pushInFlight {
		t.Fatal("P should latch pushInFlight")
	}
	if !strings.Contains(m.status, "pushing main") {
		t.Errorf("status = %q, want pushing main", m.status)
	}
	if cmd == nil {
		t.Fatal("P should dispatch pushCmd")
	}
	if msg := cmd(); msg != (pushSucceededMsg{branch: "main"}) {
		t.Fatalf("stubbed push should succeed, got %#v", msg)
	}
	if gotBranch != "main" {
		t.Errorf("push branch = %q, want main", gotBranch)
	}

	updated, _ = m.Update(pushSucceededMsg{branch: "main"})
	m = updated.(Model)
	if m.pushInFlight || !strings.Contains(m.status, "push: done") {
		t.Errorf("success should clear gate + report, status=%q", m.status)
	}
}

func TestPushKeyRejectsDetachedAndInFlight(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m = seedRefs(t, m, []git.Ref{{ShortName: "main", Kind: git.RefKindLocal, ObjectName: "x"}})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'P'}})
	m = updated.(Model)
	if cmd != nil || !strings.Contains(m.status, "detached") {
		t.Errorf("detached HEAD should reject push, status=%q", m.status)
	}

	m2 := rebaseFixture(t)
	m2.pushInFlight = true
	updated, cmd = m2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'P'}})
	if cmd != nil {
		t.Error("in-flight push should swallow a second P")
	}
	_ = updated
}

// --- cherry-pick (`c`) ---

func TestCherryPickConfirmFlow(t *testing.T) {
	prev := cherryPickExec
	t.Cleanup(func() { cherryPickExec = prev })
	var gotHash string
	cherryPickExec = func(_ context.Context, _, hash string) error {
		gotHash = hash
		return nil
	}

	m := rebaseFixture(t)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	m = updated.(Model)
	if m.mode != viewModeCherryPickConfirm {
		t.Fatalf("c should arm the confirm, mode=%v", m.mode)
	}
	if !strings.Contains(m.renderCherryPickConfirmInner(), "onto main?") {
		t.Errorf("confirm dialog should name the head branch: %q", m.renderCherryPickConfirmInner())
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = updated.(Model)
	if !m.cherryPickInFlight || cmd == nil {
		t.Fatal("y should latch the gate and dispatch")
	}
	_ = cmd()
	if gotHash != "cursor99" {
		t.Errorf("cherry-pick hash = %q, want cursor99", gotHash)
	}

	updated, _ = m.Update(cherryPickConflictMsg{err: errors.New("CONFLICT")})
	m = updated.(Model)
	if m.cherryPickInFlight || !strings.Contains(m.status, "resolve in your terminal") {
		t.Errorf("conflict should delegate to terminal, status=%q", m.status)
	}
}

// --- branch create (`n`) ---

func TestBranchCreateFlow(t *testing.T) {
	prev := branchCreateExec
	t.Cleanup(func() { branchCreateExec = prev })
	var gotName, gotStart string
	branchCreateExec = func(_ context.Context, _, name, start string) error {
		gotName, gotStart = name, start
		return nil
	}

	m := rebaseFixture(t)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = updated.(Model)
	if m.mode != viewModeBranchCreateInput {
		t.Fatalf("n should open the input modal, mode=%v", m.mode)
	}
	if m.branchCreate.startPoint != "cursor99" {
		t.Errorf("startPoint = %q, want cursor hash", m.branchCreate.startPoint)
	}

	// Empty name → inline error, modal stays.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.branchCreate.inlineErr == "" || m.mode != viewModeBranchCreateInput {
		t.Fatal("empty name should set inline error and stay in the modal")
	}

	// Type a name and dispatch.
	for _, r := range "feat/x" {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(Model)
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if !m.branchCreate.inFlight || cmd == nil {
		t.Fatal("enter with a name should dispatch branchCreateCmd")
	}
	msg := cmd()
	if gotName != "feat/x" || gotStart != "cursor99" {
		t.Errorf("create args = (%q, %q), want (feat/x, cursor99)", gotName, gotStart)
	}

	updated, reload := m.Update(msg)
	m = updated.(Model)
	if m.mode != viewModeNormal || !strings.Contains(m.status, "branch created: feat/x") || reload == nil {
		t.Errorf("success should close modal + report + reload, mode=%v status=%q", m.mode, m.status)
	}
}

func TestBranchCreateFailureStaysInModal(t *testing.T) {
	m := rebaseFixture(t)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = updated.(Model)
	m.branchCreate.inFlight = true
	updated, _ = m.Update(branchCreateFailedMsg{reqID: m.branchCreate.reqID, err: errors.New("fatal: a branch named 'x' already exists")})
	m = updated.(Model)
	if m.mode != viewModeBranchCreateInput || !strings.Contains(m.branchCreate.inlineErr, "already exists") {
		t.Errorf("failure should stay in modal with inline error, mode=%v err=%q", m.mode, m.branchCreate.inlineErr)
	}
}

// --- browse (`o`) ---

func TestBrowseKeyOpensCursorCommit(t *testing.T) {
	prev := browseExec
	t.Cleanup(func() { browseExec = prev })
	var gotHash string
	browseExec = func(_ context.Context, _, hash string) error {
		gotHash = hash
		return nil
	}

	m := rebaseFixture(t)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("o should dispatch browseCmd")
	}
	msg := cmd()
	if gotHash != "cursor99" {
		t.Errorf("browse hash = %q, want cursor99", gotHash)
	}
	updated, _ = m.Update(msg)
	m = updated.(Model)
	if !strings.Contains(m.status, "opened") {
		t.Errorf("opened status expected, got %q", m.status)
	}
}
