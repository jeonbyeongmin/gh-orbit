package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// TestLocalChangesQArmsQuit — q does not exit the local changes page (page nav
// is the tab cycle only); it stays put but arms the two-press quit, same as
// ctrl+c.
func TestLocalChangesQArmsQuit(t *testing.T) {
	m := initSized(t)
	m = enterLocalChanges(t, m)
	if m.mode != viewModeLocalChanges {
		t.Fatalf("setup: mode = %v, want viewModeLocalChanges", m.mode)
	}

	m, _ = pressRune(t, m, 'q')
	if m.mode != viewModeLocalChanges {
		t.Errorf("q should not leave the local changes page, got mode %v", m.mode)
	}
	if !m.quitArmed {
		t.Errorf("q should arm quit on the local changes page")
	}
}

// withChanges seeds one unstaged entry so the whole-tree stash / discard
// gates (HasChanges) pass. Same-package access to the private slice keeps the
// setup a one-liner — no fake git status round-trip needed.
func withChanges(m Model) Model {
	m.localChanges.entries = []localChangesEntry{
		{Path: "f.txt", Section: sectionUnstaged, WorktreeState: 'M'},
	}
	return m
}

func TestLocalChangesROpensDiscardDialog(t *testing.T) {
	m := withChanges(enterLocalChanges(t, initSized(t)))

	m, _ = pressRune(t, m, 'r')
	if !m.lcDiscardOpen {
		t.Fatal("r should open the discard confirm")
	}
	if m.mode != viewModeLocalChanges {
		t.Fatalf("discard confirm must stay in viewModeLocalChanges, got %v", m.mode)
	}

	// esc backs out without acting.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.lcDiscardOpen || m.lcActionInFlight {
		t.Fatalf("esc should cancel: open=%v inFlight=%v", m.lcDiscardOpen, m.lcActionInFlight)
	}
}

func TestLocalChangesDiscardTrackedDispatches(t *testing.T) {
	t.Cleanup(restoreLocalChangesExec(t))
	resetHardExec = func(ctx context.Context, dir string, mode git.ResetMode, hash string) error { return nil }
	cleanExec = func(ctx context.Context, dir string) error {
		t.Fatal("tracked-only discard must not clean")
		return nil
	}
	m := withChanges(enterLocalChanges(t, initSized(t)))

	m, _ = pressRune(t, m, 'r')
	m, cmd := pressRune(t, m, 't')
	if !m.lcActionInFlight || cmd == nil {
		t.Fatalf("t should dispatch the discard: inFlight=%v cmd=%v", m.lcActionInFlight, cmd)
	}
	if got, ok := cmd().(localChangesDiscardDoneMsg); !ok || got.includeUntracked {
		t.Fatalf("unexpected discard result: %T %+v", got, got)
	}
}

func TestLocalChangesStashAllDispatches(t *testing.T) {
	t.Cleanup(restoreLocalChangesExec(t))
	stashAllExec = func(ctx context.Context, dir string) error { return nil }
	m := withChanges(enterLocalChanges(t, initSized(t)))

	m, cmd := pressRune(t, m, 's')
	if !m.lcActionInFlight || cmd == nil {
		t.Fatalf("s should dispatch stash all: inFlight=%v cmd=%v", m.lcActionInFlight, cmd)
	}
	if _, ok := cmd().(localChangesStashAllDoneMsg); !ok {
		t.Fatalf("want localChangesStashAllDoneMsg from stash cmd")
	}
}

func TestLocalChangesStashAndDiscardNoOpWhenClean(t *testing.T) {
	m := enterLocalChanges(t, initSized(t)) // no entries seeded

	m, cmd := pressRune(t, m, 's')
	if m.lcActionInFlight || cmd != nil {
		t.Fatalf("stash on clean tree must be a no-op: inFlight=%v cmd=%v", m.lcActionInFlight, cmd)
	}
	m, _ = pressRune(t, m, 'r')
	if m.lcDiscardOpen {
		t.Fatal("discard on clean tree must not open the dialog")
	}
}

func TestClassifyStatusConflictGoesToConflicts(t *testing.T) {
	got := classifyStatus([]git.StatusEntry{
		{Path: "f.txt", IndexState: 'U', WorktreeState: 'U', Conflict: true},
	})
	if len(got) != 1 || got[0].Section != sectionConflicts || !got[0].Conflict {
		t.Fatalf("unexpected classify: %+v", got)
	}
}

func TestClassifyStatusUntrackedGoesToUnstaged(t *testing.T) {
	got := classifyStatus([]git.StatusEntry{
		{Path: "u.txt", IndexState: '?', WorktreeState: '?', Untracked: true},
	})
	if len(got) != 1 || got[0].Section != sectionUnstaged || !got[0].Untracked {
		t.Fatalf("unexpected classify: %+v", got)
	}
}

func TestClassifyStatusStagedAndModifiedYieldsTwoEntries(t *testing.T) {
	// Tracked file: modified in both index (M.) and worktree (.M), but in the
	// porcelain stream that arrives as one record with both bytes set. Split
	// into one Staged + one Unstaged entry.
	got := classifyStatus([]git.StatusEntry{
		{Path: "f.txt", IndexState: 'M', WorktreeState: 'M'},
	})
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d: %+v", len(got), got)
	}
	var sawStaged, sawUnstaged bool
	for _, e := range got {
		if e.Section == sectionStaged {
			sawStaged = true
		}
		if e.Section == sectionUnstaged {
			sawUnstaged = true
		}
	}
	if !sawStaged || !sawUnstaged {
		t.Fatalf("missing section coverage: %+v", got)
	}
}

func TestClassifyStatusIndexOnlyGoesToStagedOnly(t *testing.T) {
	got := classifyStatus([]git.StatusEntry{
		{Path: "f.txt", IndexState: 'M', WorktreeState: '.'},
	})
	if len(got) != 1 || got[0].Section != sectionStaged {
		t.Fatalf("unexpected classify: %+v", got)
	}
}

func TestClassifyStatusWorktreeOnlyGoesToUnstagedOnly(t *testing.T) {
	got := classifyStatus([]git.StatusEntry{
		{Path: "f.txt", IndexState: '.', WorktreeState: 'M'},
	})
	if len(got) != 1 || got[0].Section != sectionUnstaged {
		t.Fatalf("unexpected classify: %+v", got)
	}
}

func TestLocalChangesApplyStatusClampsCursor(t *testing.T) {
	m := newLocalChangesModel()
	m.SetSize(20, 10, 20, 10)
	m.ApplyStatusLoaded([]git.StatusEntry{
		{Path: "a.txt", IndexState: 'M'},
		{Path: "b.txt", IndexState: 'M'},
		{Path: "c.txt", IndexState: 'M'},
	})
	m.cursor = 2
	// Shrink the list — cursor should clamp.
	m.ApplyStatusLoaded([]git.StatusEntry{
		{Path: "a.txt", IndexState: 'M'},
	})
	if m.cursor != 0 {
		t.Fatalf("cursor not clamped: %d", m.cursor)
	}
}

func TestLocalChangesApplyDiffStaleResponseDropped(t *testing.T) {
	m := newLocalChangesModel()
	m.SetSize(20, 10, 20, 10)
	m.BeginDiffLoad(7)
	m.ApplyDiffLoaded(6, "stale-text") // wrong reqID
	if m.diffText != "" || !m.diffLoading {
		t.Fatalf("stale reqID should not paint: %q loading=%v", m.diffText, m.diffLoading)
	}
	m.ApplyDiffLoaded(7, "fresh") // matches
	if m.diffText != "fresh" {
		t.Fatalf("matching dispatch should paint: %q", m.diffText)
	}
}

func TestLocalChangesApplyDiffFailedRecordsErr(t *testing.T) {
	m := newLocalChangesModel()
	m.SetSize(20, 10, 20, 10)
	m.BeginDiffLoad(1)
	m.ApplyDiffFailed(1, errors.New("boom"))
	if m.diffLoading {
		t.Fatalf("error should clear loading flag")
	}
	if m.diffErr == nil {
		t.Fatalf("err not recorded")
	}
}

func TestLocalChangesScheduleSelectAfterReloadConsumed(t *testing.T) {
	m := newLocalChangesModel()
	m.SetSize(20, 10, 20, 10)
	m.ApplyStatusLoaded([]git.StatusEntry{
		{Path: "a.txt", IndexState: 'M'},
		{Path: "b.txt", IndexState: 'M'},
	})
	m.ScheduleSelectAfterReload("b.txt", true)
	m.ApplyStatusLoaded([]git.StatusEntry{
		{Path: "a.txt", IndexState: 'M'},
		{Path: "b.txt", IndexState: 'M'},
	})
	cur, ok := m.CurrentEntry()
	if !ok || cur.Path != "b.txt" {
		t.Fatalf("cursor should land on b.txt, got %+v", cur)
	}
	if m.pendingSelectPath != "" {
		t.Fatalf("pendingSelectPath should be cleared after consumption, got %q", m.pendingSelectPath)
	}
}

func TestLocalChangesFlatRowsHideEmptyConflicts(t *testing.T) {
	m := newLocalChangesModel()
	m.ApplyStatusLoaded([]git.StatusEntry{
		{Path: "u.txt", IndexState: '?', WorktreeState: '?', Untracked: true},
	})
	rows := m.flatRows()
	for _, r := range rows {
		if r.section == sectionConflicts {
			t.Fatalf("conflicts section emitted with zero conflict entries: %+v", rows)
		}
	}
}

func TestLocalChangesFlatRowsShowConflictsWhenPresent(t *testing.T) {
	m := newLocalChangesModel()
	m.ApplyStatusLoaded([]git.StatusEntry{
		{Path: "f.txt", IndexState: 'U', WorktreeState: 'U', Conflict: true},
	})
	rows := m.flatRows()
	if rows[0].kind != lcRowHeader || rows[0].section != sectionConflicts {
		t.Fatalf("first row should be Conflicts header: %+v", rows)
	}
}

func TestClassifyStatusEmitsInRenderOrder(t *testing.T) {
	// git status can stream a staged file before an unstaged one. The entries
	// slice must still come out ordered Conflicts → Unstaged → Staged so the
	// cursor index lines up with flatRows' visual order — otherwise cursor 0
	// lands off the top row and `enter` opens the wrong file's diff.
	got := classifyStatus([]git.StatusEntry{
		{Path: "staged.go", IndexState: 'M'},      // staged, first in the stream
		{Path: "unstaged.go", WorktreeState: 'M'}, // unstaged, second
		{Path: "conflict.go", IndexState: 'U', WorktreeState: 'U', Conflict: true},
	})
	wantSections := []localChangesSection{sectionConflicts, sectionUnstaged, sectionStaged}
	if len(got) != len(wantSections) {
		t.Fatalf("want %d entries, got %d: %+v", len(wantSections), len(got), got)
	}
	for i, want := range wantSections {
		if got[i].Section != want {
			t.Fatalf("entry %d: section %d, want %d (%+v)", i, got[i].Section, want, got)
		}
	}
}

func TestStatTextBySideAndBinary(t *testing.T) {
	m := newLocalChangesModel()
	m.SetStats(
		[]git.FileStat{
			{Path: "u.txt", Insertions: 12, Deletions: 3},  // both sides
			{Path: "del.txt", Insertions: 0, Deletions: 7}, // delete-only → "-7"
			{Path: "bin.dat", Insertions: -1, Deletions: -1},
		},
		[]git.FileStat{{Path: "s.txt", Insertions: 5, Deletions: 0}}, // add-only → "+5"
	)
	cases := []struct {
		e    localChangesEntry
		want string
	}{
		{localChangesEntry{Path: "u.txt", Section: sectionUnstaged}, "+12 -3"},
		{localChangesEntry{Path: "del.txt", Section: sectionUnstaged}, "-7"},
		{localChangesEntry{Path: "bin.dat", Section: sectionUnstaged}, "bin"},
		{localChangesEntry{Path: "s.txt", Section: sectionStaged}, "+5"},
		{localChangesEntry{Path: "u.txt", Section: sectionStaged}, ""}, // wrong side → no stat
		{localChangesEntry{Path: "missing", Section: sectionUnstaged}, ""},
	}
	for _, c := range cases {
		if got := m.statText(c.e); got != c.want {
			t.Errorf("statText(%q staged=%v) = %q, want %q", c.e.Path, c.e.Staged(), got, c.want)
		}
	}
}

func TestStatColumnRendersInTree(t *testing.T) {
	m := newLocalChangesModel()
	m.SetSize(40, 10, 40, 10)
	m.ApplyStatusLoaded([]git.StatusEntry{{Path: "f.txt", WorktreeState: 'M'}})
	m.SetStats([]git.FileStat{{Path: "f.txt", Insertions: 12, Deletions: 3}}, nil)
	out := m.TreeView()
	if !strings.Contains(out, "+12") || !strings.Contains(out, "-3") {
		t.Fatalf("stat column missing from tree row: %q", out)
	}
}

func TestLocalChangesSelectByPathPrefersStaged(t *testing.T) {
	m := newLocalChangesModel()
	m.SetSize(20, 10, 20, 10)
	m.ApplyStatusLoaded([]git.StatusEntry{
		{Path: "f.txt", IndexState: 'M', WorktreeState: 'M'},
	})
	if ok := m.SelectByPath("f.txt", true); !ok {
		t.Fatalf("SelectByPath(staged) returned false")
	}
	cur, ok := m.CurrentEntry()
	if !ok || cur.Section != sectionStaged {
		t.Fatalf("expected staged entry selected, got %+v", cur)
	}
}

func TestLocalChangesDrillDownArrows(t *testing.T) {
	// Single-pane drill-down (tab toggle retired): → descends tree →
	// diff, ← climbs back to the tree without exiting the mode.
	m := initSized(t)
	m = enterLocalChanges(t, m)
	m.localChanges.ApplyStatusLoaded([]git.StatusEntry{{Path: "f.txt", WorktreeState: 'M'}})
	if m.localChanges.Focused() != paneLCTree {
		t.Fatalf("setup: want tree focus, got %d", m.localChanges.Focused())
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(Model)
	if m.localChanges.Focused() != paneLCDiff {
		t.Fatalf("after → want diff, got %d", m.localChanges.Focused())
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = updated.(Model)
	if m.localChanges.Focused() != paneLCTree {
		t.Fatalf("after ← want tree, got %d", m.localChanges.Focused())
	}
	if m.mode != viewModeLocalChanges {
		t.Fatalf("← from diff must not exit the mode, got %v", m.mode)
	}
}

func TestLocalChangesEscIsNoOp(t *testing.T) {
	// esc has no binding on the local changes page: the diff → tree climb is
	// `←`, and there is no esc/q page exit. The tab cycle owns leaving the
	// page, so esc anywhere is inert.
	m := initSized(t)
	m = enterLocalChanges(t, m)
	if m.mode != viewModeLocalChanges {
		t.Fatalf("setup: mode = %v, want viewModeLocalChanges", m.mode)
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.mode != viewModeLocalChanges {
		t.Fatalf("esc from tree should be a no-op, got mode %v", m.mode)
	}
}

// enterLocalChangesDiff helper: drill into the diff for the cursor entry.
func enterDiff(t *testing.T, m Model) Model {
	t.Helper()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(Model)
	if m.localChanges.Focused() != paneLCDiff {
		t.Fatalf("setup: want diff focus after →, got %d", m.localChanges.Focused())
	}
	return m
}

func TestLocalChangesDiffAutoReturnsWhenSideGone(t *testing.T) {
	// In the diff pane, staging the last hunk removes the file's unstaged
	// side; the next status reload carries no unstaged entry for it, so focus
	// drops back to the tree (the chosen auto-return behavior).
	m := initSized(t)
	m = enterLocalChanges(t, m)
	m.localChanges.ApplyStatusLoaded([]git.StatusEntry{{Path: "f.txt", WorktreeState: 'M'}})
	m = enterDiff(t, m)

	// File is now fully staged → only an index side remains.
	updated, _ := m.Update(localChangesStatusLoadedMsg{entries: []git.StatusEntry{{Path: "f.txt", IndexState: 'M'}}})
	m = updated.(Model)
	if m.localChanges.Focused() != paneLCTree {
		t.Fatalf("want auto-return to tree, got diff focus %d", m.localChanges.Focused())
	}
}

func TestLocalChangesDiffReloadPinsViewedFileOnDrift(t *testing.T) {
	// A reload with no pending-select hint (e.g. `r` or a watcher refresh)
	// must keep the diff pane on the file it was showing even when the entry
	// order shifts — otherwise the index-only clamp drifts the cursor onto a
	// different file and the pane loads the wrong diff.
	m := initSized(t)
	m = enterLocalChanges(t, m)
	m.localChanges.ApplyStatusLoaded([]git.StatusEntry{
		{Path: "a.txt", WorktreeState: 'M'},
		{Path: "b.txt", WorktreeState: 'M'},
	})
	m = enterDiff(t, m) // cursor 0 → a.txt
	if e, _ := m.localChanges.CurrentEntry(); e.Path != "a.txt" {
		t.Fatalf("setup: want a.txt, got %s", e.Path)
	}

	// Reload inserts z.txt before a.txt; index 0 now points at z.txt.
	updated, _ := m.Update(localChangesStatusLoadedMsg{entries: []git.StatusEntry{
		{Path: "z.txt", WorktreeState: 'M'},
		{Path: "a.txt", WorktreeState: 'M'},
	}})
	m = updated.(Model)
	if m.localChanges.Focused() != paneLCDiff {
		t.Fatalf("should stay in diff pane (a.txt still present)")
	}
	if e, _ := m.localChanges.CurrentEntry(); e.Path != "a.txt" {
		t.Fatalf("cursor should stay pinned to a.txt, got %s", e.Path)
	}
}

func TestLocalChangesDiffStaysWhenSideRemains(t *testing.T) {
	// Partial stage: the unstaged side still has changes, so the diff pane
	// stays open across the reload.
	m := initSized(t)
	m = enterLocalChanges(t, m)
	m.localChanges.ApplyStatusLoaded([]git.StatusEntry{{Path: "f.txt", WorktreeState: 'M'}})
	m = enterDiff(t, m)

	// Both sides present → unstaged side survives.
	updated, _ := m.Update(localChangesStatusLoadedMsg{entries: []git.StatusEntry{{Path: "f.txt", IndexState: 'M', WorktreeState: 'M'}}})
	m = updated.(Model)
	if m.localChanges.Focused() != paneLCDiff {
		t.Fatalf("want diff focus retained, got %d", m.localChanges.Focused())
	}
}

func TestDispatchLocalChangesStagePicksAddVsRestore(t *testing.T) {
	t.Cleanup(restoreLocalChangesExec(t))
	var sawAdd, sawRestore string
	addExec = func(ctx context.Context, dir, path string) error {
		sawAdd = path
		return nil
	}
	restoreStagedExec = func(ctx context.Context, dir, path string) error {
		sawRestore = path
		return nil
	}

	// Unstaged → Add
	m := New()
	m.localChanges.ApplyStatusLoaded([]git.StatusEntry{{Path: "u.txt", WorktreeState: 'M'}})
	_, cmd := m.dispatchLocalChangesStage()
	if cmd == nil {
		t.Fatalf("dispatch returned nil cmd for unstaged")
	}
	cmd()
	if sawAdd != "u.txt" {
		t.Fatalf("Add not called for unstaged: sawAdd=%q sawRestore=%q", sawAdd, sawRestore)
	}

	sawAdd, sawRestore = "", ""
	// Staged → Restore
	m = New()
	m.localChanges.ApplyStatusLoaded([]git.StatusEntry{{Path: "s.txt", IndexState: 'M'}})
	_, cmd = m.dispatchLocalChangesStage()
	if cmd == nil {
		t.Fatalf("dispatch returned nil cmd for staged")
	}
	cmd()
	if sawRestore != "s.txt" {
		t.Fatalf("Restore not called for staged: sawAdd=%q sawRestore=%q", sawAdd, sawRestore)
	}
}

func TestDispatchLocalChangesOpenLaunchesCursorFile(t *testing.T) {
	t.Cleanup(restoreLocalChangesExec(t))
	var sawPath string
	openFileExec = func(ctx context.Context, dir, path string) error {
		sawPath = path
		return nil
	}

	m := New()
	m.localChanges.ApplyStatusLoaded([]git.StatusEntry{
		{Path: "a.txt", WorktreeState: 'M'},
		{Path: "b.txt", WorktreeState: 'M'},
	})
	m.localChanges.MoveCursor(1) // land on b.txt

	_, cmd := m.dispatchLocalChangesOpen()
	if cmd == nil {
		t.Fatalf("dispatch returned nil cmd")
	}
	msg := cmd()
	if sawPath != "b.txt" {
		t.Fatalf("open called for wrong file: got %q want %q", sawPath, "b.txt")
	}
	if got, ok := msg.(localChangesOpenSucceededMsg); !ok || got.path != "b.txt" {
		t.Fatalf("expected succeeded msg for b.txt, got %#v", msg)
	}
}

func TestDispatchLocalChangesOpenSurfacesError(t *testing.T) {
	t.Cleanup(restoreLocalChangesExec(t))
	boom := errors.New("no such file")
	openFileExec = func(ctx context.Context, dir, path string) error { return boom }

	m := New()
	m.localChanges.ApplyStatusLoaded([]git.StatusEntry{{Path: "gone.txt", WorktreeState: 'D'}})

	_, cmd := m.dispatchLocalChangesOpen()
	if cmd == nil {
		t.Fatalf("dispatch returned nil cmd")
	}
	msg := cmd()
	failed, ok := msg.(localChangesOpenFailedMsg)
	if !ok {
		t.Fatalf("expected failed msg, got %#v", msg)
	}
	if failed.path != "gone.txt" || failed.err != boom {
		t.Fatalf("failed msg mismatch: %#v", failed)
	}
}

func TestLocalChangesTreeViewEmptyHasPlaceholder(t *testing.T) {
	m := newLocalChangesModel()
	m.SetSize(20, 10, 20, 10)
	m.ApplyStatusLoaded(nil)
	out := m.TreeView()
	if !strings.Contains(out, "(no changes)") {
		t.Fatalf("expected placeholder, got %q", out)
	}
}
