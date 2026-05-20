package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

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

func TestCycleLocalChangesFocusToggleTreeDiff(t *testing.T) {
	// Sidebar focus retired in PR B2 → the 3-way refs/tree/diff cycle
	// collapsed to a 2-way tree/diff toggle inside the right column.
	m := New()
	m.localChanges.SetFocus(paneLCTree)

	m = m.cycleLocalChangesFocus()
	if m.localChanges.Focused() != paneLCDiff {
		t.Fatalf("after first tab want diff, got %d", m.localChanges.Focused())
	}
	m = m.cycleLocalChangesFocus()
	if m.localChanges.Focused() != paneLCTree {
		t.Fatalf("after second tab want tree, got %d", m.localChanges.Focused())
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

func TestLocalChangesTreeViewEmptyHasPlaceholder(t *testing.T) {
	m := newLocalChangesModel()
	m.SetSize(20, 10, 20, 10)
	m.ApplyStatusLoaded(nil)
	out := m.TreeView()
	if !strings.Contains(out, "(no changes)") {
		t.Fatalf("expected placeholder, got %q", out)
	}
}
