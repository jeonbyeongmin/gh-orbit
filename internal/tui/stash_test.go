package tui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// stashStubs swaps in stubs for the stash write seams. Same pattern as
// refsActionStubs in refsaction_test.go. pop shares stashPopExec with the
// dirty-tree checkout chain in checkout_test.go — tests that exercise both
// must coordinate the stub.
type stashStubs struct {
	apply func(ctx context.Context, dir, label string) error
	drop  func(ctx context.Context, dir, label string) error
	pop   func(ctx context.Context, dir, label string) error
}

func withStashStubs(t *testing.T, s stashStubs) {
	t.Helper()
	prevApply, prevDrop, prevPop := stashApplyExec, stashDropExec, stashPopExec
	t.Cleanup(func() {
		stashApplyExec = prevApply
		stashDropExec = prevDrop
		stashPopExec = prevPop
	})
	if s.apply != nil {
		stashApplyExec = s.apply
	}
	if s.drop != nil {
		stashDropExec = s.drop
	}
	if s.pop != nil {
		stashPopExec = s.pop
	}
}

func TestRefModelStashSectionRenders(t *testing.T) {
	r := newRefsModel()
	r.SetSize(40, 20)
	r, _ = r.Update(refsLoadedMsg{refs: []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, IsHead: true},
		{ShortName: "stash@{0}", Kind: git.RefKindStash, ObjectName: "deadbeef"},
	}})
	view := r.View()
	if !strings.Contains(view, "Stashes") {
		t.Errorf("view should contain 'Stashes' header, got %q", view)
	}
	if !strings.Contains(view, "stash@{0}") {
		t.Errorf("view should contain 'stash@{0}' row, got %q", view)
	}
}

func TestRefModelStashRefsAccessor(t *testing.T) {
	r := newRefsModel()
	stash := git.Ref{ShortName: "stash@{0}", Kind: git.RefKindStash, ObjectName: "h0"}
	r, _ = r.Update(refsLoadedMsg{refs: []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal},
		stash,
	}})
	got := r.StashRefs()
	if len(got) != 1 || got[0].ShortName != "stash@{0}" {
		t.Errorf("StashRefs = %+v, want [stash@{0}]", got)
	}
}

func TestPartitionByKindAcceptsStash(t *testing.T) {
	got := partitionByKind([]git.Ref{
		{Kind: git.RefKindLocal, ShortName: "main"},
		{Kind: git.RefKindStash, ShortName: "stash@{0}"},
		{Kind: git.RefKindStash, ShortName: "stash@{1}"},
	})
	if len(got[3]) != 2 {
		t.Errorf("partition[3] (stash) len = %d, want 2", len(got[3]))
	}
}

func TestDiffStashRefsDetectsChange(t *testing.T) {
	// First call against empty prev → changed.
	stashes := []git.Ref{
		{Kind: git.RefKindStash, ShortName: "stash@{0}", ObjectName: "h0"},
	}
	hashes, byHash, changed := diffStashRefs(stashes, nil)
	if !changed {
		t.Error("empty → 1 entry should be changed")
	}
	if !slices.Equal(hashes, []string{"h0"}) {
		t.Errorf("hashes = %v", hashes)
	}
	if byHash["h0"] != "stash@{0}" {
		t.Errorf("byHash = %v", byHash)
	}
	// Same set against same prev → not changed.
	if _, _, c2 := diffStashRefs(stashes, hashes); c2 {
		t.Error("same set should not report changed")
	}
}

func TestGraphEvaluatorStashRowReturnsStashAction(t *testing.T) {
	hash := "stashhash"
	stashes := []git.Ref{
		{ShortName: "stash@{0}", Kind: git.RefKindStash, ObjectName: hash},
	}
	got := runGraphEvaluatorWithStashes(t, hash, nil, nil, stashes)
	if got.kind != graphActionStashAction {
		t.Errorf("kind = %v, want graphActionStashAction", got.kind)
	}
	if got.stashLabel != "stash@{0}" {
		t.Errorf("stashLabel = %q, want stash@{0}", got.stashLabel)
	}
}

func TestGraphEvaluatorStashRowBeatsChip(t *testing.T) {
	hash := "shared"
	stashes := []git.Ref{
		{ShortName: "stash@{0}", Kind: git.RefKindStash, ObjectName: hash},
	}
	// Even if a local chip somehow points at the same hash, the stash branch
	// must win so the user never gets checkout dispatched against a stash row.
	locals := []git.Ref{
		{ShortName: "weird", Kind: git.RefKindLocal, ObjectName: hash},
		{ShortName: "main", Kind: git.RefKindLocal, ObjectName: "elsewhere", IsHead: true},
	}
	got := runGraphEvaluatorWithStashes(t, hash, locals, nil, stashes)
	if got.kind != graphActionStashAction {
		t.Errorf("kind = %v, want graphActionStashAction (stash wins over chip)", got.kind)
	}
}

func TestGraphActionStashOpensPicker(t *testing.T) {
	m := initSized(t)
	m = seedGraphCursor(t, m, "stashhash")
	m.actionInFlight = true

	updated, cmd := m.Update(graphActionMsg{
		hash: "stashhash", kind: graphActionStashAction, stashLabel: "stash@{0}",
	})
	m = updated.(Model)
	if m.mode != viewModeStashActionPicker {
		t.Errorf("mode = %v, want viewModeStashActionPicker", m.mode)
	}
	if m.pendingStashAction.label != "stash@{0}" {
		t.Errorf("pendingStashAction.label = %q, want stash@{0}", m.pendingStashAction.label)
	}
	if cmd != nil {
		t.Error("entering the picker should not dispatch a cmd")
	}
}

func TestStashPickerPDispatchesPop(t *testing.T) {
	m := initSized(t)
	m.mode = viewModeStashActionPicker
	m.pendingStashAction = pendingStashAction{label: "stash@{0}", hash: "h"}
	var seenLabel string
	withStashStubs(t, stashStubs{
		pop: func(_ context.Context, _, label string) error {
			seenLabel = label
			return nil
		},
	})

	m, cmd := pressRune(t, m, 'p')
	if cmd == nil {
		t.Fatal("p should dispatch stashPopCmd")
	}
	if m.mode != viewModeNormal {
		t.Errorf("mode after dispatch = %v, want viewModeNormal", m.mode)
	}
	if !m.checkoutInFlight {
		t.Error("checkoutInFlight should latch while pop is in flight")
	}
	// Execute the dispatched cmd to verify the seam was called with the
	// pending label.
	if msg := cmd(); msg == nil {
		t.Error("popCmd returned nil msg")
	}
	if seenLabel != "stash@{0}" {
		t.Errorf("seam saw label %q, want stash@{0}", seenLabel)
	}
}

func TestStashPickerADispatchesApply(t *testing.T) {
	m := initSized(t)
	m.mode = viewModeStashActionPicker
	m.pendingStashAction = pendingStashAction{label: "stash@{0}"}
	withStashStubs(t, stashStubs{
		apply: func(context.Context, string, string) error { return nil },
	})

	m, cmd := pressRune(t, m, 'a')
	if cmd == nil {
		t.Fatal("a should dispatch stashApplyCmd")
	}
	if m.mode != viewModeNormal {
		t.Errorf("mode after dispatch = %v, want viewModeNormal", m.mode)
	}
	msg := cmd()
	if _, ok := msg.(stashApplySucceededMsg); !ok {
		t.Errorf("apply cmd returned %T, want stashApplySucceededMsg", msg)
	}
}

func TestStashPickerEscClosesModal(t *testing.T) {
	m := initSized(t)
	m.mode = viewModeStashActionPicker
	m.pendingStashAction = pendingStashAction{label: "stash@{0}"}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.mode != viewModeNormal {
		t.Errorf("mode after esc = %v, want viewModeNormal", m.mode)
	}
	if m.pendingStashAction != (pendingStashAction{}) {
		t.Errorf("pendingStashAction should reset, got %+v", m.pendingStashAction)
	}
	if !strings.Contains(m.status, "cancelled") {
		t.Errorf("status = %q, want it to mention cancelled", m.status)
	}
}

func TestStashPickerOtherKeysSwallowed(t *testing.T) {
	m := initSized(t)
	m.mode = viewModeStashActionPicker
	m.pendingStashAction = pendingStashAction{label: "stash@{0}"}

	for _, r := range []rune{'j', 'k', 'd', 'y', 'n'} {
		_, cmd := pressRune(t, m, r)
		if cmd != nil {
			t.Errorf("key %q should be swallowed (cmd != nil)", r)
		}
	}
}

func TestRefsDKeyOnStashOpensDropConfirm(t *testing.T) {
	m := initSized(t)
	m = seedRefs(t, m, []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, IsHead: true},
		{ShortName: "stash@{0}", Kind: git.RefKindStash, ObjectName: "h0"},
	})
	m.focused = paneRefs
	m.refs.cursor = 1 // stash row

	m, _ = pressRune(t, m, 'd')
	if m.mode != viewModeStashDropConfirm {
		t.Errorf("mode = %v, want viewModeStashDropConfirm", m.mode)
	}
	if m.pendingStashDrop.label != "stash@{0}" {
		t.Errorf("pendingStashDrop.label = %q, want stash@{0}", m.pendingStashDrop.label)
	}
}

func TestStashDropConfirmYDispatchesDrop(t *testing.T) {
	m := initSized(t)
	m.mode = viewModeStashDropConfirm
	m.pendingStashDrop = pendingStashDrop{label: "stash@{0}"}
	var seenLabel string
	withStashStubs(t, stashStubs{
		drop: func(_ context.Context, _, label string) error {
			seenLabel = label
			return nil
		},
	})

	m, cmd := pressRune(t, m, 'y')
	if cmd == nil {
		t.Fatal("y should dispatch stashDropCmd")
	}
	if m.mode != viewModeNormal {
		t.Errorf("mode = %v, want viewModeNormal", m.mode)
	}
	if !m.refActionInFlight {
		t.Error("refActionInFlight should latch")
	}
	if msg := cmd(); msg == nil {
		t.Error("dropCmd returned nil msg")
	}
	if seenLabel != "stash@{0}" {
		t.Errorf("seam saw label %q, want stash@{0}", seenLabel)
	}
}

func TestStashDropConfirmEscClosesModal(t *testing.T) {
	m := initSized(t)
	m.mode = viewModeStashDropConfirm
	m.pendingStashDrop = pendingStashDrop{label: "stash@{0}"}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.mode != viewModeNormal {
		t.Errorf("mode after esc = %v, want viewModeNormal", m.mode)
	}
	if !strings.Contains(m.status, "cancelled") {
		t.Errorf("status = %q, want it to mention cancelled", m.status)
	}
}

func TestStashPopConflictMentionsPreserved(t *testing.T) {
	m := initSized(t)
	m.checkoutInFlight = true
	m.pendingStashAction = pendingStashAction{label: "stash@{0}"}

	updated, cmd := m.Update(stashPopConflictMsg{
		label: "stash@{0}",
		err:   errors.New("git stash pop: stash pop conflict: CONFLICT"),
	})
	m = updated.(Model)
	if m.checkoutInFlight {
		t.Error("checkoutInFlight should release on conflict msg")
	}
	if !strings.Contains(m.status, "CONFLICT") {
		t.Errorf("status = %q, want it to include 'CONFLICT'", m.status)
	}
	if !strings.Contains(m.status, "preserved") {
		t.Errorf("status = %q, want it to mention preserved", m.status)
	}
	if cmd == nil {
		t.Error("conflict should still reload refs+graph")
	}
}

func TestStashDropSucceededArmsCursorAndReloads(t *testing.T) {
	m := initSized(t)
	m.refActionInFlight = true

	updated, cmd := m.Update(stashDropSucceededMsg{label: "stash@{0}"})
	m = updated.(Model)
	if m.refActionInFlight {
		t.Error("refActionInFlight should release")
	}
	if m.pendingRefCursorAfterDelete.name != "stash@{0}" {
		t.Errorf("pendingRefCursorAfterDelete.name = %q, want stash@{0}", m.pendingRefCursorAfterDelete.name)
	}
	if m.pendingRefCursorAfterDelete.kind != git.RefKindStash {
		t.Errorf("pendingRefCursorAfterDelete.kind = %v, want RefKindStash", m.pendingRefCursorAfterDelete.kind)
	}
	if cmd == nil {
		t.Error("success should dispatch reloadCmd")
	}
	if !strings.Contains(m.status, "dropped") {
		t.Errorf("status = %q, want it to mention dropped", m.status)
	}
}

func TestRefsLoadedMsgTriggersReloadOnStashSetChange(t *testing.T) {
	m := initSized(t)
	priorReqID := m.streamReqID

	updated, cmd := m.Update(refsLoadedMsg{refs: []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, IsHead: true, ObjectName: "h"},
		{ShortName: "stash@{0}", Kind: git.RefKindStash, ObjectName: "sh"},
	}})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("stash set change should dispatch reload cmd")
	}
	if !slices.Equal(m.currentStashHashes, []string{"sh"}) {
		t.Errorf("currentStashHashes = %v, want [sh]", m.currentStashHashes)
	}
	if m.streamReqID == priorReqID {
		t.Error("reload should advance streamReqID")
	}
}

func TestRefsLoadedMsgIdempotentOnSameStash(t *testing.T) {
	m := initSized(t)
	// Seed currentStashHashes so the next refsLoadedMsg with the same set
	// does NOT trigger reload (otherwise infinite loop).
	m.currentStashHashes = []string{"sh"}
	m.currentStashByHash = map[string]string{"sh": "stash@{0}"}
	priorReqID := m.streamReqID

	updated, _ := m.Update(refsLoadedMsg{refs: []git.Ref{
		{ShortName: "stash@{0}", Kind: git.RefKindStash, ObjectName: "sh"},
	}})
	m = updated.(Model)
	if m.streamReqID != priorReqID {
		t.Errorf("streamReqID changed from %d to %d on unchanged stash set",
			priorReqID, m.streamReqID)
	}
}
