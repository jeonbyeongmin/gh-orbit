package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// seedGraphCursor parks the graph cursor at one commit so handleKey paths
// that depend on m.graph.Selected() ok=true stop short-circuiting. The
// hash threads through tests as the "cursor commit" they act on.
func seedGraphCursor(t *testing.T, m Model, hash string) Model {
	t.Helper()
	m.focused = paneGraph
	updated, _ := m.Update(commitsAppendedMsg{
		reqID: 1,
		done:  true,
		rows: []graphRow{
			{commit: git.Commit{Hash: hash, Subject: "first", AuthorTime: time.Now()}},
		},
	})
	m = updated.(Model)
	updated, _ = m.Update(commitsStreamDoneMsg{reqID: 1})
	return updated.(Model)
}

// seedRefs feeds the model a refsLoadedMsg so handlers that rely on
// m.refs.byKind[0] (e.g., the graph Enter evaluator dispatch path) see a
// non-empty locals slice.
func seedRefs(t *testing.T, m Model, refs []git.Ref) Model {
	t.Helper()
	updated, _ := m.Update(refsLoadedMsg{refs: refs})
	return updated.(Model)
}

func TestGraphEnterDispatchesEvaluator(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m = seedGraphCursor(t, m, "abc1234")
	m = seedRefs(t, m, []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, ObjectName: "abc1234", IsHead: true},
	})

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	if !m.actionInFlight {
		t.Error("graph Enter should latch actionInFlight")
	}
	if !strings.Contains(m.status, "resolving") {
		t.Errorf("status = %q, want it to mention resolving", m.status)
	}
	if cmd == nil {
		t.Error("graph Enter should return a cmd (the evaluator)")
	}
}

func TestGraphEnterSwallowedWhileActionInFlight(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m = seedGraphCursor(t, m, "abc1234")
	m = seedRefs(t, m, []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, ObjectName: "abc1234", IsHead: true},
	})
	m.actionInFlight = true
	m.status = "→ resolving…"

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Error("second Enter while resolving should return nil cmd")
	}
}

func TestGraphEnterRefusesWithoutRefsLoaded(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m = seedGraphCursor(t, m, "abc1234")
	// No seedRefs — refs.byKind[0] is empty.

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.actionInFlight {
		t.Error("Enter should not latch actionInFlight without refs")
	}
	if !strings.Contains(m.status, "refs not loaded") {
		t.Errorf("status = %q, want 'refs not loaded'", m.status)
	}
	if cmd != nil {
		t.Error("Enter should return nil cmd without refs")
	}
}

func TestGraphActionMsgNoOpStaysOnBranch(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m = seedGraphCursor(t, m, "abc1234")
	m.actionInFlight = true

	updated, cmd := m.Update(graphActionMsg{
		hash: "abc1234", kind: graphActionNoOp, branch: "main",
	})
	m = updated.(Model)
	if m.actionInFlight {
		t.Error("graphActionMsg should release actionInFlight")
	}
	if !strings.Contains(m.status, "already on main") {
		t.Errorf("status = %q, want 'already on main'", m.status)
	}
	if cmd != nil {
		t.Error("NoOp should not dispatch a cmd")
	}
}

func TestGraphActionMsgCheckoutCallsCheckout(t *testing.T) {
	getRef, _, _ := stubCheckout(t)
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m = seedGraphCursor(t, m, "abc1234")
	m.actionInFlight = true

	updated, cmd := m.Update(graphActionMsg{
		hash: "abc1234", kind: graphActionCheckout, branch: "feat",
	})
	m = updated.(Model)
	if !m.checkoutInFlight {
		t.Error("graphActionCheckout should latch checkoutInFlight")
	}
	if m.pendingCheckout.ref != "feat" {
		t.Errorf("pendingCheckout.ref = %q, want feat", m.pendingCheckout.ref)
	}
	if cmd == nil {
		t.Fatal("graphActionCheckout should return a cmd")
	}
	_ = cmd()
	if got, ok := getRef(); !ok || got != "feat" {
		t.Errorf("checkoutExec ref = %q ok=%v, want feat", got, ok)
	}
}

func TestGraphActionMsgFFDispatchesFFOnly(t *testing.T) {
	var ffHash string
	withChainStubs(t, chainStubs{
		mergeFFOnly: func(_ context.Context, _, hash string) error {
			ffHash = hash
			return nil
		},
		countAhead: func(context.Context, string, string, string) (int, error) {
			return 3, nil
		},
	})
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m = seedGraphCursor(t, m, "abc1234")
	m.actionInFlight = true

	updated, cmd := m.Update(graphActionMsg{
		hash: "abc1234", kind: graphActionFF, branch: "main", advance: 3,
	})
	m = updated.(Model)
	if !m.ffInFlight {
		t.Error("graphActionFF should latch ffInFlight")
	}
	if !strings.Contains(m.status, "fast-forward: main +3") {
		t.Errorf("status = %q, want 'fast-forward: main +3'", m.status)
	}
	if cmd == nil {
		t.Fatal("FF should return a cmd")
	}
	_ = cmd()
	if ffHash != "abc1234" {
		t.Errorf("merge --ff-only hash = %q, want abc1234", ffHash)
	}
}

func TestGraphActionMsgDetachCallsCheckoutDetached(t *testing.T) {
	_, _, getDetached := stubCheckout(t)
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m = seedGraphCursor(t, m, "abc1234")
	m.actionInFlight = true

	updated, cmd := m.Update(graphActionMsg{
		hash: "abc1234", kind: graphActionDetach,
	})
	m = updated.(Model)
	if !m.checkoutInFlight {
		t.Error("graphActionDetach should latch checkoutInFlight")
	}
	if !m.pendingCheckout.detached {
		t.Errorf("pendingCheckout = %+v, want detached=true", m.pendingCheckout)
	}
	if cmd == nil {
		t.Fatal("Detach should return a cmd")
	}
	_ = cmd()
	if got, ok := getDetached(); !ok || got != "abc1234" {
		t.Errorf("checkoutDetachedExec ref = %q ok=%v, want abc1234", got, ok)
	}
}

func TestGraphActionMsgPickerEntersBranchPickerMode(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m = seedGraphCursor(t, m, "abc1234")
	m.actionInFlight = true

	updated, cmd := m.Update(graphActionMsg{
		hash: "abc1234", kind: graphActionPicker,
		candidates: []string{"feat-a", "feat-b"},
	})
	m = updated.(Model)
	if m.mode != viewModeBranchPicker {
		t.Errorf("mode = %v, want viewModeBranchPicker", m.mode)
	}
	if len(m.branchPicker.candidates) != 2 {
		t.Errorf("candidates = %v, want 2 entries", m.branchPicker.candidates)
	}
	if m.branchPicker.cursor != 0 {
		t.Errorf("cursor = %d, want 0 (initial)", m.branchPicker.cursor)
	}
	if cmd != nil {
		t.Error("Picker entry should not dispatch a cmd")
	}
}

func TestGraphActionMsgStaleHashIsDropped(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m = seedGraphCursor(t, m, "current")
	m.actionInFlight = true

	updated, cmd := m.Update(graphActionMsg{
		hash: "stale", kind: graphActionFF, branch: "main", advance: 3,
	})
	m = updated.(Model)
	if m.actionInFlight {
		t.Error("stale msg should still release actionInFlight")
	}
	if m.ffInFlight {
		t.Error("stale msg should not arm ffInFlight")
	}
	if cmd != nil {
		t.Error("stale msg should not dispatch a cmd")
	}
}

func TestBranchPickerEnterDispatchesCheckout(t *testing.T) {
	getRef, _, _ := stubCheckout(t)
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.mode = viewModeBranchPicker
	m.branchPicker = branchPickerState{
		candidates: []string{"feat-a", "feat-b"},
		cursor:     1,
		hash:       "abc1234",
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.mode != viewModeNormal {
		t.Errorf("mode = %v, want viewModeNormal after enter", m.mode)
	}
	if !m.checkoutInFlight {
		t.Error("picker enter should latch checkoutInFlight via beginCheckout")
	}
	if m.pendingCheckout.ref != "feat-b" {
		t.Errorf("pendingCheckout.ref = %q, want feat-b (cursor=1)", m.pendingCheckout.ref)
	}
	if cmd == nil {
		t.Fatal("picker enter should return a cmd")
	}
	_ = cmd()
	if got, ok := getRef(); !ok || got != "feat-b" {
		t.Errorf("checkoutExec ref = %q ok=%v, want feat-b", got, ok)
	}
}

func TestBranchPickerEscRestoresNormalMode(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.mode = viewModeBranchPicker
	m.branchPicker = branchPickerState{
		candidates: []string{"a", "b"},
		cursor:     0,
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.mode != viewModeNormal {
		t.Errorf("mode = %v, want viewModeNormal after esc", m.mode)
	}
	if len(m.branchPicker.candidates) != 0 {
		t.Errorf("branchPicker should be cleared, got %+v", m.branchPicker)
	}
	if !strings.Contains(m.status, "cancelled") {
		t.Errorf("status = %q, want it to mention cancelled", m.status)
	}
	if cmd != nil {
		t.Error("esc should not dispatch a cmd")
	}
}

func TestBranchPickerJKMovesCursorWithinClamp(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.mode = viewModeBranchPicker
	m.branchPicker = branchPickerState{
		candidates: []string{"a", "b", "c"},
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	m = updated.(Model)
	if m.branchPicker.cursor != 0 {
		t.Errorf("k at top: cursor = %d, want 0 (clamped)", m.branchPicker.cursor)
	}

	for range 5 {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		m = updated.(Model)
	}
	if m.branchPicker.cursor != 2 {
		t.Errorf("after 5×j on 3 candidates: cursor = %d, want 2 (clamped)", m.branchPicker.cursor)
	}
}

func TestFFSucceededReloadsAndJumpsHEAD(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.ffInFlight = true

	updated, cmd := m.Update(ffSucceededMsg{branch: "main", advance: 3})
	m = updated.(Model)
	if m.ffInFlight {
		t.Error("ffSucceededMsg should release ffInFlight")
	}
	if !strings.Contains(m.status, "fast-forward: main +3") {
		t.Errorf("status = %q, want 'fast-forward: main +3'", m.status)
	}
	if m.pendingHEADHash != pendingHEADSentinel {
		t.Errorf("pendingHEADHash = %q, want sentinel %q", m.pendingHEADHash, pendingHEADSentinel)
	}
	if cmd == nil {
		t.Fatal("ffSucceededMsg should dispatch reload cmd")
	}
}

func TestFFFailedSurfacesError(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.ffInFlight = true

	boom := errors.New("merge --ff-only: divergent")
	updated, cmd := m.Update(ffFailedMsg{err: boom})
	m = updated.(Model)
	if m.ffInFlight {
		t.Error("ffFailedMsg should release ffInFlight")
	}
	if !strings.Contains(m.status, "fast-forward failed") {
		t.Errorf("status = %q, want 'fast-forward failed'", m.status)
	}
	if cmd != nil {
		t.Error("ffFailedMsg should not trigger reload")
	}
}

func TestFFNeedsCleanTreeEntersConfirmModal(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.ffInFlight = true

	updated, cmd := m.Update(ffNeedsCleanTreeMsg{branch: "main", hash: "abc1234"})
	m = updated.(Model)
	if m.ffInFlight {
		t.Error("ffNeedsCleanTreeMsg should release ffInFlight")
	}
	if m.mode != viewModeCheckoutConfirm {
		t.Errorf("mode = %v, want viewModeCheckoutConfirm", m.mode)
	}
	if !m.pendingCheckout.withFF {
		t.Errorf("pendingCheckout = %+v, want withFF=true", m.pendingCheckout)
	}
	if m.pendingCheckout.ref != "main" || m.pendingCheckout.ffHash != "abc1234" {
		t.Errorf("pendingCheckout = %+v, want ref=main ffHash=abc1234", m.pendingCheckout)
	}
	if cmd != nil {
		t.Error("modal entry should not dispatch a cmd")
	}
}

func TestCheckoutConfirmStashWithFFRunsStashThenFFChain(t *testing.T) {
	var stashCalled, ffCalled bool
	withChainStubs(t, chainStubs{
		stash:       func(context.Context, string, string) error { stashCalled = true; return nil },
		mergeFFOnly: func(context.Context, string, string) error { ffCalled = true; return nil },
		countAhead:  func(context.Context, string, string, string) (int, error) { return 1, nil },
	})
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.mode = viewModeCheckoutConfirm
	m.pendingCheckout = pendingCheckout{ref: "main", withFF: true, ffHash: "abc1234"}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m = updated.(Model)
	if m.mode != viewModeNormal {
		t.Errorf("mode = %v, want viewModeNormal after s", m.mode)
	}
	if !m.ffInFlight {
		t.Error("modal s on withFF should latch ffInFlight (not checkoutInFlight)")
	}
	if m.checkoutInFlight {
		t.Error("modal s on withFF should NOT latch checkoutInFlight")
	}
	if cmd == nil {
		t.Fatal("modal s should return stashThenFFCmd")
	}
	_ = cmd()
	if !stashCalled || !ffCalled {
		t.Errorf("stash=%v ff=%v, want both true", stashCalled, ffCalled)
	}
}

func TestGraphActionMsgCheckoutAndFFDispatchesCheckoutThenFF(t *testing.T) {
	var coCalled, ffCalled bool
	withChainStubs(t, chainStubs{
		checkout:    func(context.Context, string, string) error { coCalled = true; return nil },
		mergeFFOnly: func(context.Context, string, string) error { ffCalled = true; return nil },
		countAhead:  func(context.Context, string, string, string) (int, error) { return 2, nil },
	})
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m = seedGraphCursor(t, m, "abc1234")
	m.actionInFlight = true

	updated, cmd := m.Update(graphActionMsg{
		hash: "abc1234", kind: graphActionCheckoutAndFF, branch: "develop",
	})
	m = updated.(Model)
	if !m.ffInFlight {
		t.Error("CheckoutAndFF should latch ffInFlight")
	}
	if !strings.Contains(m.status, "checkout + ff") {
		t.Errorf("status = %q, want it to mention 'checkout + ff'", m.status)
	}
	if cmd == nil {
		t.Fatal("CheckoutAndFF should return a cmd")
	}
	_ = cmd()
	if !coCalled || !ffCalled {
		t.Errorf("co=%v ff=%v, want both true", coCalled, ffCalled)
	}
}

func TestFFCheckoutNeedsCleanTreeEntersConfirmModalWithCheckoutFF(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.ffInFlight = true

	updated, cmd := m.Update(ffCheckoutNeedsCleanTreeMsg{branch: "develop", hash: "abc1234"})
	m = updated.(Model)
	if m.ffInFlight {
		t.Error("ffCheckoutNeedsCleanTreeMsg should release ffInFlight")
	}
	if m.mode != viewModeCheckoutConfirm {
		t.Errorf("mode = %v, want viewModeCheckoutConfirm", m.mode)
	}
	if !m.pendingCheckout.withCheckoutFF {
		t.Errorf("pendingCheckout = %+v, want withCheckoutFF=true", m.pendingCheckout)
	}
	if m.pendingCheckout.ref != "develop" || m.pendingCheckout.ffHash != "abc1234" {
		t.Errorf("pendingCheckout = %+v, want ref=develop ffHash=abc1234", m.pendingCheckout)
	}
	if cmd != nil {
		t.Error("modal entry should not dispatch a cmd")
	}
}

func TestCheckoutConfirmStashWithCheckoutFFRunsChain(t *testing.T) {
	var stCalled, coCalled, ffCalled bool
	withChainStubs(t, chainStubs{
		stash:       func(context.Context, string, string) error { stCalled = true; return nil },
		checkout:    func(context.Context, string, string) error { coCalled = true; return nil },
		mergeFFOnly: func(context.Context, string, string) error { ffCalled = true; return nil },
		countAhead:  func(context.Context, string, string, string) (int, error) { return 1, nil },
	})
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.mode = viewModeCheckoutConfirm
	m.pendingCheckout = pendingCheckout{ref: "develop", withCheckoutFF: true, ffHash: "abc1234"}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m = updated.(Model)
	if m.mode != viewModeNormal {
		t.Errorf("mode = %v, want viewModeNormal after s", m.mode)
	}
	if !m.ffInFlight {
		t.Error("modal s on withCheckoutFF should latch ffInFlight (not checkoutInFlight)")
	}
	if m.checkoutInFlight {
		t.Error("modal s on withCheckoutFF should NOT latch checkoutInFlight")
	}
	if cmd == nil {
		t.Fatal("modal s should return stashThenCheckoutThenFFCmd")
	}
	_ = cmd()
	if !stCalled || !coCalled || !ffCalled {
		t.Errorf("st=%v co=%v ff=%v, want all true", stCalled, coCalled, ffCalled)
	}
}

func TestCheckoutThenFFSucceededMsgReloadsAndJumpsHEAD(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.ffInFlight = true

	updated, cmd := m.Update(checkoutThenFFSucceededMsg{branch: "develop", advance: 2})
	m = updated.(Model)
	if m.ffInFlight {
		t.Error("checkoutThenFFSucceededMsg should release ffInFlight")
	}
	if !strings.Contains(m.status, "fast-forward: develop +2") {
		t.Errorf("status = %q, want '+2' advance", m.status)
	}
	if !strings.Contains(m.status, "after checkout") {
		t.Errorf("status = %q, want it to mention 'after checkout'", m.status)
	}
	if m.pendingHEADHash != pendingHEADSentinel {
		t.Errorf("pendingHEADHash = %q, want sentinel", m.pendingHEADHash)
	}
	if cmd == nil {
		t.Fatal("checkoutThenFFSucceededMsg should dispatch reload cmd")
	}
}

func TestStashThenFFMsgReloadsAndJumpsHEAD(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.ffInFlight = true

	updated, cmd := m.Update(stashThenFFMsg{
		branch: "main", advance: 2, stashLabel: "stash@{0}",
	})
	m = updated.(Model)
	if m.ffInFlight {
		t.Error("stashThenFFMsg should release ffInFlight")
	}
	if !strings.Contains(m.status, "fast-forward: main +2") {
		t.Errorf("status = %q, want '+2' advance reflected", m.status)
	}
	if !strings.Contains(m.status, "stash@{0}") {
		t.Errorf("status = %q, want it to surface the stash label", m.status)
	}
	if m.pendingHEADHash != pendingHEADSentinel {
		t.Errorf("pendingHEADHash = %q, want sentinel", m.pendingHEADHash)
	}
	if cmd == nil {
		t.Fatal("stashThenFFMsg should dispatch reload cmd")
	}
}
