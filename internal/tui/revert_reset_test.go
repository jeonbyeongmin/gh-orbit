package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// --- revert (`v`) ---

func TestRevertConfirmFlow(t *testing.T) {
	prev := revertExec
	t.Cleanup(func() { revertExec = prev })
	var gotHash string
	revertExec = func(_ context.Context, _, hash string) error {
		gotHash = hash
		return nil
	}

	m := rebaseFixture(t) // HEAD on main, cursor on cursor99
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	m = updated.(Model)
	if m.mode != viewModeRevertConfirm {
		t.Fatalf("v should arm the confirm, mode=%v", m.mode)
	}
	if !strings.Contains(m.renderRevertConfirmInner(), "on main?") {
		t.Errorf("confirm should name the head branch: %q", m.renderRevertConfirmInner())
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = updated.(Model)
	if !m.revertInFlight || cmd == nil {
		t.Fatal("y should latch the gate and dispatch")
	}
	_ = cmd()
	if gotHash != "cursor99" {
		t.Errorf("revert hash = %q, want cursor99", gotHash)
	}

	updated, _ = m.Update(revertConflictMsg{err: errors.New("CONFLICT")})
	m = updated.(Model)
	if m.revertInFlight || !strings.Contains(m.status, "resolve in Local Changes") {
		t.Errorf("conflict should point to Local Changes, status=%q", m.status)
	}
}

func TestRevertRejectsDetached(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m = seedGraphCursor(t, m, "cursor99")
	// No IsHead ref → detached HEAD.
	m = seedRefs(t, m, []git.Ref{{ShortName: "main", Kind: git.RefKindLocal, ObjectName: "x"}})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	m = updated.(Model)
	if cmd != nil || m.mode == viewModeRevertConfirm || !strings.Contains(m.status, "detached") {
		t.Errorf("detached HEAD should reject revert, status=%q mode=%v", m.status, m.mode)
	}
}

// While a revert/reset confirm is open the status row must blank out like
// every other modal — otherwise the `? help` hint bleeds under the box.
func TestRevertResetConfirmsBlankStatusRow(t *testing.T) {
	m := rebaseFixture(t)
	for _, mode := range []viewMode{viewModeRevertConfirm, viewModeResetConfirm} {
		m.mode = mode
		if got := m.renderHelpStatus(); got != " " {
			t.Errorf("mode %v: status row = %q, want blank ' ' while modal open", mode, got)
		}
	}
}

// --- reset (`x`) ---

// resetFixtureModel seeds HEAD on main (headhash) with cursor on cursor99
// (develop), optionally giving main an upstream for the pushed-history guard.
func resetFixtureModel(t *testing.T, upstream string) Model {
	t.Helper()
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m = seedGraphCursor(t, m, "cursor99")
	m = seedRefs(t, m, []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, ObjectName: "headhash", IsHead: true, Upstream: upstream},
		{ShortName: "develop", Kind: git.RefKindLocal, ObjectName: "cursor99"},
	})
	return m
}

func TestResetEvalArmsConfirm(t *testing.T) {
	prev := countAheadExec
	t.Cleanup(func() { countAheadExec = prev })
	countAheadExec = func(_ context.Context, _, ancestor, descendant string) (int, error) {
		switch {
		case ancestor == "headhash" && descendant == "cursor99":
			return 0, nil // cursor is behind HEAD → valid discard
		case ancestor == "cursor99" && descendant == "headhash":
			return 2, nil // dropping 2 commits
		}
		return 0, nil
	}

	m := resetFixtureModel(t, "")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m = updated.(Model)
	if !m.resetInFlight || cmd == nil {
		t.Fatal("x should latch the gate and dispatch the evaluator")
	}
	updated, _ = m.Update(cmd())
	m = updated.(Model)
	if m.mode != viewModeResetConfirm || m.resetInFlight {
		t.Fatalf("eval ok should arm confirm + clear gate, mode=%v inflight=%v", m.mode, m.resetInFlight)
	}
	if m.pendingReset.discard != 2 {
		t.Errorf("discard = %d, want 2", m.pendingReset.discard)
	}
	inner := m.renderResetConfirmInner()
	if !strings.Contains(inner, "reset main to develop?") || !strings.Contains(inner, "discards 2 commits") {
		t.Errorf("confirm dialog = %q", inner)
	}
}

func TestResetEvalRejectsCursorAhead(t *testing.T) {
	prev := countAheadExec
	t.Cleanup(func() { countAheadExec = prev })
	countAheadExec = func(_ context.Context, _, ancestor, descendant string) (int, error) {
		if ancestor == "headhash" && descendant == "cursor99" {
			return 1, nil // cursor ahead of HEAD → nothing to discard
		}
		return 0, nil
	}
	m := resetFixtureModel(t, "")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m = updated.(Model)
	updated, _ = m.Update(cmd())
	m = updated.(Model)
	if m.mode == viewModeResetConfirm || !strings.Contains(m.status, "ahead of HEAD") {
		t.Errorf("cursor ahead should reject, status=%q mode=%v", m.status, m.mode)
	}
}

func TestResetEvalRejectsPushedHistory(t *testing.T) {
	prev := countAheadExec
	t.Cleanup(func() { countAheadExec = prev })
	countAheadExec = func(_ context.Context, _, ancestor, descendant string) (int, error) {
		switch {
		case ancestor == "headhash" && descendant == "cursor99":
			return 0, nil
		case ancestor == "cursor99" && descendant == "headhash":
			return 2, nil
		case ancestor == "cursor99" && descendant == "origin/main":
			return 1, nil // upstream holds a commit the reset would drop
		}
		return 0, nil
	}
	m := resetFixtureModel(t, "origin/main")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m = updated.(Model)
	updated, _ = m.Update(cmd())
	m = updated.(Model)
	if m.mode == viewModeResetConfirm || !strings.Contains(m.status, "use revert") {
		t.Errorf("pushed history should reject → revert, status=%q mode=%v", m.status, m.mode)
	}
}

func TestResetConfirmDispatchesHardMode(t *testing.T) {
	prev := resetExec
	t.Cleanup(func() { resetExec = prev })
	var gotMode git.ResetMode
	var gotHash string
	resetExec = func(_ context.Context, _ string, mode git.ResetMode, hash string) error {
		gotMode, gotHash = mode, hash
		return nil
	}
	m := resetFixtureModel(t, "")
	m.pendingReset = pendingReset{target: "cursor99", label: "develop", branch: "main", discard: 2}
	m.mode = viewModeResetConfirm

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	m = updated.(Model)
	if !m.resetInFlight || cmd == nil {
		t.Fatal("h should latch the gate and dispatch a hard reset")
	}
	msg := cmd()
	if gotMode != git.ResetHard || gotHash != "cursor99" {
		t.Errorf("dispatch = (%v, %q), want (hard, cursor99)", gotMode, gotHash)
	}
	updated, reload := m.Update(msg)
	m = updated.(Model)
	if m.resetInFlight || reload == nil || !strings.Contains(m.status, "reset: main → develop (hard -2)") {
		t.Errorf("success should clear gate + report + reload, status=%q", m.status)
	}
}
