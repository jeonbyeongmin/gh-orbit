package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// refModel is a storage container post-PR-B2 — these tests cover the
// data-shape contracts (LocalRefs / RemoteRefs / Worktrees / dirty maps
// / Local Changes meta / fetch freshness) and the few render helpers
// that the dashboard reuses (composeLocalChangesRow, formatLocalChangesMeta,
// renderWorktreeSidebarRow). Cursor / View / Update key handling left
// with the sidebar in PR B2.

func TestRefModelHandlesLoadFailure(t *testing.T) {
	r := newRefsModel()
	r, _ = r.Update(refsLoadFailedMsg{err: errSentinel})
	if !r.loaded {
		t.Error("loaded should flip to true on a load failure (loaded = 'we tried')")
	}
	if r.err == nil {
		t.Error("err should carry the failure sentinel")
	}
}

func TestRefModelResetForReloadClearsLoaded(t *testing.T) {
	r := newRefsModel()
	r, _ = r.Update(refsLoadedMsg{refs: nil})
	if !r.loaded {
		t.Fatal("setup: loaded should be true after refsLoadedMsg")
	}
	r.ResetForReload()
	if r.loaded {
		t.Error("ResetForReload should flip loaded back to false")
	}
}

// --- partitionByKind ---

func TestPartitionByKindSeparatesByKind(t *testing.T) {
	refs := []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal},
		{ShortName: "feat/foo", Kind: git.RefKindLocal},
		{ShortName: "origin/feat/qa", Kind: git.RefKindRemote},
		{ShortName: "v0.1.0", Kind: git.RefKindTag},
	}
	out := partitionByKind(refs)
	if len(out[0]) != 2 {
		t.Errorf("local count = %d, want 2", len(out[0]))
	}
	if len(out[1]) != 1 || out[1][0].ShortName != "origin/feat/qa" {
		t.Errorf("remote slice = %+v, want [origin/feat/qa]", out[1])
	}
	if len(out[2]) != 1 || out[2][0].ShortName != "v0.1.0" {
		t.Errorf("tag slice = %+v, want [v0.1.0]", out[2])
	}
}

func TestPartitionByKindHidesMirroredRemote(t *testing.T) {
	refs := []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal},
		{ShortName: "origin/main", Kind: git.RefKindRemote},
		{ShortName: "origin/feat/qa", Kind: git.RefKindRemote},
	}
	out := partitionByKind(refs)
	if len(out[1]) != 1 || out[1][0].ShortName != "origin/feat/qa" {
		t.Errorf("mirror filter should hide origin/main when local main exists; remote slice = %+v", out[1])
	}
}

// --- Worktrees ---

func TestRefModelSetWorktreesPrunesDirtyMaps(t *testing.T) {
	r := newRefsModel()
	r.SetWorktrees([]git.Worktree{{Path: "/a"}, {Path: "/b"}}, "/a")
	r.SetWorktreeDirty("/a", true, false)
	r.SetWorktreeDirty("/b", false, true)
	if !r.WorktreeDirty("/a") {
		t.Error("setup: /a should be dirty")
	}
	// /b disappears.
	r.SetWorktrees([]git.Worktree{{Path: "/a"}}, "/a")
	if _, present := r.worktreeDirty["/b"]; present {
		t.Error("SetWorktrees should prune dirty entries for paths that disappeared")
	}
	if _, present := r.worktreeTimedOut["/b"]; present {
		t.Error("SetWorktrees should prune timedOut entries for paths that disappeared")
	}
}

func TestRefModelWorktreesAccessor(t *testing.T) {
	r := newRefsModel()
	r.SetWorktrees([]git.Worktree{{Path: "/a"}, {Path: "/b"}}, "/a")
	wts := r.Worktrees()
	if len(wts) != 2 {
		t.Errorf("Worktrees() len = %d, want 2", len(wts))
	}
}

// --- Local Changes meta helpers ---

func TestFormatLocalChangesMetaSingularFile(t *testing.T) {
	r := newRefsModel()
	r.SetLocalChangesSummary(git.LocalChangesSummary{FilesChanged: 1, Insertions: 5, Deletions: 0}, time.Now())
	got := r.formatLocalChangesMeta(time.Now())
	if !strings.Contains(got, "1 file ·") {
		t.Errorf("singular form expected, got %q", got)
	}
}

func TestFormatLocalChangesMetaEmptySummary(t *testing.T) {
	r := newRefsModel()
	if got := r.formatLocalChangesMeta(time.Now()); got != "" {
		t.Errorf("empty summary should yield empty meta, got %q", got)
	}
}

func TestFormatLocalChangesMetaJustNowNoAgoSuffix(t *testing.T) {
	r := newRefsModel()
	now := time.Now()
	r.SetLocalChangesSummary(git.LocalChangesSummary{FilesChanged: 1, Insertions: 1, Deletions: 0}, now)
	got := r.formatLocalChangesMeta(now)
	if strings.Contains(got, "just now ago") {
		t.Errorf("'just now' should not get ' ago' suffix: %q", got)
	}
	if !strings.Contains(got, "just now") {
		t.Errorf("expected 'just now' segment: %q", got)
	}
}

func TestComposeLocalChangesRowTruncatesMetaBeforeLabel(t *testing.T) {
	out := composeLocalChangesRow("● Local Changes", "3 files · +47 -12 · 2m ago", 22, false)
	plain := ansi.Strip(out)
	if !strings.Contains(plain, "● Local Changes") {
		t.Errorf("label must survive narrow width: %q", plain)
	}
	if !strings.HasSuffix(plain, "…") {
		t.Errorf("expected truncation marker on meta, got %q", plain)
	}
}

func TestComposeLocalChangesRowBareLabelWhenTooNarrowForMeta(t *testing.T) {
	out := composeLocalChangesRow("● Local Changes", "3 files", 15, false)
	plain := ansi.Strip(out)
	if strings.Contains(plain, "files") {
		t.Errorf("meta should drop entirely when no room: %q", plain)
	}
}

// --- renderWorktreeSidebarRow shape (the dashboard reuses it) ---

func TestRenderWorktreeRowShowsCurrentMarker(t *testing.T) {
	out := renderWorktreeSidebarRow(
		git.Worktree{Path: "/tmp/wt-a", Branch: "main"},
		true, false, "", 40,
	)
	if !strings.Contains(ansi.Strip(out), "▶") {
		t.Errorf("current=true row should carry ▶ marker: %q", ansi.Strip(out))
	}
}

func TestRenderWorktreeRowDirtyMarkerLast(t *testing.T) {
	out := renderWorktreeSidebarRow(
		git.Worktree{Path: "/tmp/wt-a", Branch: "main"},
		false, false, "●", 40,
	)
	plain := ansi.Strip(out)
	if !strings.HasSuffix(strings.TrimSpace(plain), "●") {
		t.Errorf("dirty marker should be the trailing segment: %q", plain)
	}
}
