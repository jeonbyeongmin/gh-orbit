package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// enter on a PR-badged cursor row opens the PR on the web (`gh pr view --web`)
// for the cursor PR — reviewing happens on GitHub, not in an overlay.
func TestPREnterOpensWebFromCursorRow(t *testing.T) {
	prev := prViewWebExec
	t.Cleanup(func() { prViewWebExec = prev })
	var gotNumber int
	prViewWebExec = func(_ context.Context, _ string, number int) error {
		gotNumber = number
		return nil
	}

	m := browsePRFixture(t)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	// enter must not enter any overlay/page — it stays on the graph.
	if m.mode != viewModeNormal {
		t.Fatalf("mode = %v, want viewModeNormal (enter just opens the web)", m.mode)
	}
	if cmd == nil {
		t.Fatal("enter should dispatch openPRWebCmd")
	}
	msg := cmd()
	if gotNumber != 42 {
		t.Errorf("gh pr view number = %d, want 42", gotNumber)
	}
	if _, ok := msg.(prWebOpenedMsg); !ok {
		t.Fatalf("want prWebOpenedMsg, got %T", msg)
	}
}

// enter without a PR-bearing chip reports instead of dispatching.
func TestPREnterWithoutPRReports(t *testing.T) {
	m := rebaseFixture(t)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd != nil {
		t.Fatal("enter without a PR-bearing chip should not dispatch")
	}
	if !strings.Contains(m.status, "no open PR") {
		t.Errorf("status = %q, want 'no open PR'", m.status)
	}
}

// `m` on a PR-badged cursor row arms the standalone merge confirm dialog for
// the cursor PR, recording the graph as the page to return to.
func TestPRMergeArmsConfirmFromCursorRow(t *testing.T) {
	m := browsePRFixture(t)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	m = updated.(Model)
	if m.mode != viewModeMergeConfirm {
		t.Fatalf("m should arm the merge confirm; mode=%v", m.mode)
	}
	if m.mergeConfirm.number != 42 {
		t.Errorf("mergeConfirm.number = %d, want 42", m.mergeConfirm.number)
	}
	if m.mergeReturnMode != viewModeNormal {
		t.Errorf("graph-launched merge should return to the graph, got %v", m.mergeReturnMode)
	}
	if m.currentPageIndex() != 0 {
		t.Errorf("graph-launched merge keeps the Graph tab, got page %d", m.currentPageIndex())
	}
	if cmd != nil {
		t.Error("arming the confirm should not dispatch yet")
	}
}

// `m` without a PR-bearing chip reports instead of arming an empty dialog.
func TestPRMergeWithoutPRReports(t *testing.T) {
	m := rebaseFixture(t)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	m = updated.(Model)
	if m.mode == viewModeMergeConfirm {
		t.Error("m with no PR on the cursor row must not arm the merge confirm")
	}
	if !strings.Contains(m.status, "no open PR") {
		t.Errorf("status = %q, want 'no open PR'", m.status)
	}
}

func TestMergeConfirmStrategies(t *testing.T) {
	orig := prMergeExec
	t.Cleanup(func() { prMergeExec = orig })
	cases := []struct {
		key  rune
		want string
	}{
		{'s', "squash"},
		{'m', "merge"},
		{'r', "rebase"},
	}
	for _, c := range cases {
		var gotStrategy string
		var gotNumber int
		prMergeExec = func(_ context.Context, _ string, number int, strategy string) error {
			gotNumber, gotStrategy = number, strategy
			return nil
		}
		m := browsePRFixture(t)
		m, _ = m.beginMergeFor(42)
		updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{c.key}})
		m = updated.(Model)
		if !m.mergeInFlight {
			t.Errorf("%c: should arm mergeInFlight", c.key)
		}
		if cmd == nil {
			t.Fatalf("%c: should dispatch mergePRCmd", c.key)
		}
		done, ok := cmd().(prMergeDoneMsg)
		if !ok || done.strategy != c.want || gotStrategy != c.want || gotNumber != 42 {
			t.Errorf("%c: strategy=%q number=%d, want %q/42", c.key, gotStrategy, gotNumber, c.want)
		}
	}
}

// esc cancels the merge confirm back to the launching page.
func TestMergeConfirmEscCancels(t *testing.T) {
	m := browsePRFixture(t)
	m, _ = m.beginMergeFor(42)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.mode != viewModeNormal {
		t.Errorf("esc should close the merge confirm to the graph; mode=%v", m.mode)
	}
	if m.mergeConfirm.number != 0 {
		t.Errorf("esc should clear mergeConfirm, got %d", m.mergeConfirm.number)
	}
}

func TestMergeConfirmInFlightSwallowsKeys(t *testing.T) {
	m := browsePRFixture(t)
	m, _ = m.beginMergeFor(42)
	m.mergeInFlight = true
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m = updated.(Model)
	if cmd != nil {
		t.Error("keys should be swallowed while a merge is in flight")
	}
}

func TestOpenPRWebCmdErrors(t *testing.T) {
	prev := prViewWebExec
	t.Cleanup(func() { prViewWebExec = prev })
	prViewWebExec = func(_ context.Context, _ string, _ int) error { return errors.New("no remote") }
	if _, ok := openPRWebCmd("/x", 1)().(prWebFailedMsg); !ok {
		t.Error("web open error should return prWebFailedMsg")
	}
}

func TestMergeCmdError(t *testing.T) {
	orig := prMergeExec
	t.Cleanup(func() { prMergeExec = orig })
	prMergeExec = func(_ context.Context, _ string, _ int, _ string) error { return errors.New("nope") }
	if _, ok := mergePRCmd("/x", 1, "squash")().(prMergeFailedMsg); !ok {
		t.Error("merge error should return prMergeFailedMsg")
	}
}

func TestUpdateWebOpened(t *testing.T) {
	m := browsePRFixture(t)
	m.setBusyStatus("opening #42…")
	updated, _ := m.Update(prWebOpenedMsg{number: 42})
	m = updated.(Model)
	if m.statusIsBusy() {
		t.Error("web opened should clear the busy status so the spinner stops")
	}
	if !strings.Contains(m.status, "opened #42") {
		t.Errorf("status = %q, want 'opened #42'", m.status)
	}
}

func TestUpdateWebFailedReports(t *testing.T) {
	m := browsePRFixture(t)
	m.setBusyStatus("opening #42…")
	updated, _ := m.Update(prWebFailedMsg{err: errors.New("gh: no auth")})
	m = updated.(Model)
	if !strings.Contains(m.status, "no auth") {
		t.Errorf("status = %q, want the gh error", m.status)
	}
}

func TestUpdateMergeDone(t *testing.T) {
	m := browsePRFixture(t)
	m, _ = m.beginMergeFor(42)
	m.mergeInFlight = true
	updated, _ := m.Update(prMergeDoneMsg{number: 42, strategy: "squash"})
	m = updated.(Model)
	if m.mode != viewModeNormal {
		t.Errorf("merge done should close the confirm to the graph; mode=%v", m.mode)
	}
	if m.mergeInFlight {
		t.Error("merge done should clear mergeInFlight")
	}
	if !strings.Contains(m.status, "merged #42 (squash)") {
		t.Errorf("status = %q, want merged #42 (squash)", m.status)
	}
}

func TestUpdateMergeFailedReports(t *testing.T) {
	m := browsePRFixture(t)
	m, _ = m.beginMergeFor(42)
	m.mergeInFlight = true
	updated, _ := m.Update(prMergeFailedMsg{err: errors.New("not mergeable")})
	m = updated.(Model)
	if m.mode != viewModeNormal {
		t.Errorf("merge failed should close the confirm; mode=%v", m.mode)
	}
	if m.mergeInFlight {
		t.Error("merge failed should clear mergeInFlight")
	}
	if !strings.Contains(m.status, "not mergeable") {
		t.Errorf("status = %q, want the gh error", m.status)
	}
}

func TestRenderMergeConfirmInner(t *testing.T) {
	m := browsePRFixture(t)
	m, _ = m.beginMergeFor(42)
	box := m.renderMergeConfirmInner()
	for _, want := range []string{"merge PR #42?", "squash", "merge", "rebase"} {
		if !strings.Contains(box, want) {
			t.Errorf("merge dialog missing %q: %q", want, box)
		}
	}
	// While merging, the dialog swaps to the busy line.
	m.mergeInFlight = true
	if busy := m.renderMergeConfirmInner(); !strings.Contains(busy, "merging") {
		t.Errorf("in-flight dialog = %q, want a merging line", busy)
	}
}

// While armed, View() composes the merge dialog over the graph base.
func TestMergeConfirmComposesDialog(t *testing.T) {
	m := browsePRFixture(t)
	m, _ = m.beginMergeFor(42)
	if !strings.Contains(m.View(), "merge PR #42?") {
		t.Error("armed merge should render a centered dialog over the page")
	}
}
