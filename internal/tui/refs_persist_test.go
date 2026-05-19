package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// ---- helper unit tests -----------------------------------------------------

func TestSelectByNameKindHitsInOwnSection(t *testing.T) {
	r := newRefsModel()
	r.SetSize(40, 20)
	r, _ = r.Update(refsLoadedMsg{refs: []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal},
		{ShortName: "feat/foo", Kind: git.RefKindLocal},
		{ShortName: "origin/main", Kind: git.RefKindRemote},
	}})
	if !r.SelectByNameKind("feat/foo", git.RefKindLocal) {
		t.Fatal("hit lookup returned false")
	}
	got, ok := r.Selected()
	if !ok || got.ShortName != "feat/foo" || got.Kind != git.RefKindLocal {
		t.Errorf("Selected=%+v, want feat/foo local", got)
	}
}

func TestSelectByNameKindMissReturnsFalseAndLeavesCursor(t *testing.T) {
	r := newRefsModel()
	r.SetSize(40, 20)
	r, _ = r.Update(refsLoadedMsg{refs: []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal},
	}})
	r = pressKey(t, r, "j") // cursor moves; ensure miss doesn't reset it
	before := r.cursor
	if r.SelectByNameKind("nope", git.RefKindLocal) {
		t.Error("miss returned true")
	}
	if r.cursor != before {
		t.Errorf("miss moved cursor: %d → %d", before, r.cursor)
	}
}

func TestSelectByNameKindIgnoresSameNameInOtherSection(t *testing.T) {
	// Regression for the cross-section bleed: a tag named "main" must not
	// pull the cursor onto it when the caller asked for a local "main".
	r := newRefsModel()
	r.SetSize(40, 20)
	r, _ = r.Update(refsLoadedMsg{refs: []git.Ref{
		{ShortName: "alpha", Kind: git.RefKindLocal},
		{ShortName: "main", Kind: git.RefKindTag},
	}})
	if r.SelectByNameKind("main", git.RefKindLocal) {
		t.Error("returned true even though no local 'main' exists")
	}
	got, _ := r.Selected()
	if got.Kind == git.RefKindTag {
		t.Errorf("cursor bled into tag section: %+v", got)
	}
}

func TestSelectByNameKindOrNeighborFallsBackToSection(t *testing.T) {
	r := newRefsModel()
	r.SetSize(40, 20)
	r, _ = r.Update(refsLoadedMsg{refs: []git.Ref{
		{ShortName: "alpha", Kind: git.RefKindLocal},
		{ShortName: "gamma", Kind: git.RefKindLocal}, // beta was the missing target
		{ShortName: "origin/zzz", Kind: git.RefKindRemote},
	}})
	r.SelectByNameKindOrNeighbor("beta", git.RefKindLocal)
	got, _ := r.Selected()
	if got.ShortName != "gamma" || got.Kind != git.RefKindLocal {
		t.Errorf("neighbor fallback landed on %+v, want gamma local", got)
	}
}

func TestSelectByNameKindOrNeighborMissLastFallsBackPrev(t *testing.T) {
	r := newRefsModel()
	r.SetSize(40, 20)
	r, _ = r.Update(refsLoadedMsg{refs: []git.Ref{
		{ShortName: "alpha", Kind: git.RefKindLocal},
		{ShortName: "beta", Kind: git.RefKindLocal}, // missing target sorts after beta
		{ShortName: "origin/zzz", Kind: git.RefKindRemote},
	}})
	r.SelectByNameKindOrNeighbor("zzz", git.RefKindLocal)
	got, _ := r.Selected()
	if got.ShortName != "beta" || got.Kind != git.RefKindLocal {
		t.Errorf("neighbor fallback landed on %+v, want beta local (last in section)", got)
	}
}

// ---- Model-layer integration tests ----------------------------------------

// refsCursorPersistSetup builds a Model with a ref list, focuses paneRefs,
// and parks the refs cursor on the given target ref. It returns the model
// ready to receive a reload-trigger msg.
func refsCursorPersistSetup(t *testing.T, refs []git.Ref, targetName string, targetKind git.RefKind) Model {
	t.Helper()
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	updated, _ = m.Update(refsLoadedMsg{refs: refs})
	m = updated.(Model)
	if !m.refs.SelectByNameKind(targetName, targetKind) {
		t.Fatalf("setup: SelectByNameKind(%q,%v) returned false", targetName, targetKind)
	}
	return m
}

// simulateReload runs `trigger` through Update, then asserts the persist
// snapshot was captured, then injects a fresh refsLoadedMsg as the reload's
// loadRefsCmd would have produced. Returns the post-reload Model.
func simulateReload(t *testing.T, m Model, trigger tea.Msg, newRefs []git.Ref) Model {
	t.Helper()
	updated, _ := m.Update(trigger)
	m = updated.(Model)
	if m.pendingRefCursorPersist.name == "" {
		t.Fatalf("trigger %T did not arm pendingRefCursorPersist", trigger)
	}
	updated, _ = m.Update(refsLoadedMsg{refs: newRefs})
	m = updated.(Model)
	return m
}

func assertSelected(t *testing.T, m Model, wantName string, wantKind git.RefKind) {
	t.Helper()
	got, ok := m.refs.Selected()
	if !ok {
		t.Fatalf("Selected() returned !ok after reload")
	}
	if got.ShortName != wantName || got.Kind != wantKind {
		t.Errorf("Selected=%+v, want %s (%v)", got, wantName, wantKind)
	}
}

// basicLocalRefs returns refs in alphabetical order, matching what
// for-each-ref produces. SelectAfterDeleted's insertion-point walk
// requires sorted section contents.
func basicLocalRefs() []git.Ref {
	return []git.Ref{
		{ShortName: "feat/a", Kind: git.RefKindLocal, ObjectName: "0022"},
		{ShortName: "feat/b", Kind: git.RefKindLocal, ObjectName: "0033"},
		{ShortName: "main", Kind: git.RefKindLocal, IsHead: true, ObjectName: "0011"},
	}
}

func TestPersistCursorAcrossCheckoutReload(t *testing.T) {
	m := refsCursorPersistSetup(t, basicLocalRefs(), "feat/b", git.RefKindLocal)
	m = simulateReload(t, m, checkoutSucceededMsg{ref: "feat/b"}, basicLocalRefs())
	assertSelected(t, m, "feat/b", git.RefKindLocal)
}

func TestPersistCursorAcrossPullReload(t *testing.T) {
	m := refsCursorPersistSetup(t, basicLocalRefs(), "feat/a", git.RefKindLocal)
	m = simulateReload(t, m, pullSucceededMsg{}, basicLocalRefs())
	assertSelected(t, m, "feat/a", git.RefKindLocal)
}

func TestPersistCursorAcrossFetchReload(t *testing.T) {
	m := refsCursorPersistSetup(t, basicLocalRefs(), "feat/b", git.RefKindLocal)
	m = simulateReload(t, m, fetchSucceededMsg{}, basicLocalRefs())
	assertSelected(t, m, "feat/b", git.RefKindLocal)
}

func TestPersistCursorAcrossFFReload(t *testing.T) {
	m := refsCursorPersistSetup(t, basicLocalRefs(), "feat/b", git.RefKindLocal)
	m = simulateReload(t, m, ffSucceededMsg{branch: "feat/b", advance: 1}, basicLocalRefs())
	assertSelected(t, m, "feat/b", git.RefKindLocal)
}

func TestPersistCursorAcrossCheckoutThenFFReload(t *testing.T) {
	m := refsCursorPersistSetup(t, basicLocalRefs(), "feat/a", git.RefKindLocal)
	m = simulateReload(t, m, checkoutThenFFSucceededMsg{branch: "feat/a", advance: 1}, basicLocalRefs())
	assertSelected(t, m, "feat/a", git.RefKindLocal)
}

func TestPersistCursorAcrossManualReload(t *testing.T) {
	// 'r' on paneRefs triggers reloadCmd via the focused-pane key path. The
	// snapshot capture inside reloadCmd is what we're after, so we call it
	// directly to keep the test independent of which pane owns 'r'.
	m := refsCursorPersistSetup(t, basicLocalRefs(), "feat/b", git.RefKindLocal)
	m.reloadCmd()
	if m.pendingRefCursorPersist.name != "feat/b" {
		t.Fatalf("reloadCmd did not capture feat/b: %+v", m.pendingRefCursorPersist)
	}
	updated, _ := m.Update(refsLoadedMsg{refs: basicLocalRefs()})
	m = updated.(Model)
	assertSelected(t, m, "feat/b", git.RefKindLocal)
}

func TestPersistDroppedWhenPendingDeleteWins(t *testing.T) {
	m := refsCursorPersistSetup(t, basicLocalRefs(), "feat/a", git.RefKindLocal)
	m.reloadCmd() // snapshots feat/a
	m.pendingRefCursorAfterDelete = deletedRefHandle{name: "feat/a", kind: git.RefKindLocal}
	postRefs := []git.Ref{
		{ShortName: "feat/b", Kind: git.RefKindLocal},
		{ShortName: "main", Kind: git.RefKindLocal, IsHead: true},
	}
	updated, _ := m.Update(refsLoadedMsg{refs: postRefs})
	m = updated.(Model)
	assertSelected(t, m, "feat/b", git.RefKindLocal)
	if m.pendingRefCursorPersist.name != "" {
		t.Errorf("persist not cleared after delete branch fired: %+v", m.pendingRefCursorPersist)
	}
}

func TestPersistMissFallsBackToNeighbor(t *testing.T) {
	// Cursor on feat/b. Between snapshot and reload, feat/b is deleted by
	// an external git command. The reload's ref list omits it. Restore
	// must land in the same section's neighbor (feat/a is what survives).
	m := refsCursorPersistSetup(t, basicLocalRefs(), "feat/b", git.RefKindLocal)
	m.reloadCmd()
	postRefs := []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, IsHead: true},
		{ShortName: "feat/a", Kind: git.RefKindLocal},
	}
	updated, _ := m.Update(refsLoadedMsg{refs: postRefs})
	m = updated.(Model)
	got, _ := m.refs.Selected()
	if got.Kind != git.RefKindLocal {
		t.Errorf("neighbor fallback bled out of local section: %+v", got)
	}
}

func TestPersistEmptyRefSetDoesNotCrash(t *testing.T) {
	// Detached HEAD / empty ref set: Selected() returns false, persist stays
	// zero, post-reload restore is a no-op, cursor=0 baseline survives.
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	updated, _ = m.Update(refsLoadedMsg{refs: nil})
	m = updated.(Model)
	m.reloadCmd()
	if m.pendingRefCursorPersist.name != "" {
		t.Errorf("persist captured non-zero on empty ref set: %+v", m.pendingRefCursorPersist)
	}
	updated, _ = m.Update(refsLoadedMsg{refs: nil})
	m = updated.(Model)
	if m.refs.cursor != 0 {
		t.Errorf("cursor=%d after no-op restore, want 0", m.refs.cursor)
	}
}

// TestE8PersistCursorWithStickyWorktreeFrame asserts that the
// PR #37 b14719a "cursor persist across reload" invariant survives the
// sticky-worktree-frame refactor. The worktree section sits ABOVE the
// ref rows in flatRows, so the cursor's "n-th selectable ref" position
// is decoupled from any flat index that shifts when the worktree count
// changes — but a regression in flatRows / cursorFlatRow could easily
// reintroduce the bug, so this is the iron-rule guard.
func TestE8PersistCursorWithStickyWorktreeFrame(t *testing.T) {
	// Setup: refs cursor on feat/b, sidebar has two worktrees rendered
	// above the ref rows (sticky frame).
	m := refsCursorPersistSetup(t, basicLocalRefs(), "feat/b", git.RefKindLocal)
	m.refs.SetWorktrees([]git.Worktree{
		{Path: "/r/main", Branch: "main", IsMain: true},
		{Path: "/r/feat", Branch: "feat"},
	}, "/r/main")

	// Trigger a reload via a stand-in success msg, then deliver the same
	// ref list back — sticky frame should not perturb cursor restore.
	m = simulateReload(t, m, checkoutSucceededMsg{ref: "feat/b"}, basicLocalRefs())
	assertSelected(t, m, "feat/b", git.RefKindLocal)

	// And again with a different worktree count on the post-reload side
	// (entries appear / vanish between snapshots) — the cursor still
	// lands on feat/b because the persist path stores name+kind, not a
	// flat index.
	m = refsCursorPersistSetup(t, basicLocalRefs(), "feat/a", git.RefKindLocal)
	m.refs.SetWorktrees([]git.Worktree{
		{Path: "/r/main", Branch: "main", IsMain: true},
	}, "/r/main")
	m.reloadCmd()
	// Simulate a post-reload with more worktrees (a new tree appeared
	// during the reload window).
	m.refs.SetWorktrees([]git.Worktree{
		{Path: "/r/main", Branch: "main", IsMain: true},
		{Path: "/r/feat", Branch: "feat"},
		{Path: "/r/other", Branch: "other"},
	}, "/r/main")
	updated, _ := m.Update(refsLoadedMsg{refs: basicLocalRefs()})
	m = updated.(Model)
	assertSelected(t, m, "feat/a", git.RefKindLocal)
}
