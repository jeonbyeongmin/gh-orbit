package tui

import (
	"strings"
	"testing"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// refModel is a storage container post-PR-B2 — these tests cover the
// data-shape contracts (LocalRefs / RemoteRefs / Worktrees / dirty maps
// / fetch freshness). Cursor / View / Update key handling left with the
// sidebar in PR B2; the worktree dashboard's card renderer is tested in
// worktreeview_test.go.

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

// A for-each-ref failure backs the whole graph, so the Model must surface it
// on the status line rather than leave an unexplained empty graph.
func TestRefsLoadFailureSurfacesStatus(t *testing.T) {
	m := New()
	m.status = ""
	updated, _ := m.Update(refsLoadFailedMsg{err: errSentinel})
	m = updated.(Model)
	if !strings.Contains(m.status, "refs load failed") {
		t.Errorf("refs load failure should surface, status=%q", m.status)
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
	r.SetWorktreeDirty("/a", 3, false)
	r.SetWorktreeDirty("/b", 0, true)
	if !r.WorktreeDirty("/a") {
		t.Error("setup: /a should be dirty")
	}
	if r.WorktreeDirtyCount("/a") != 3 {
		t.Errorf("setup: /a dirty count = %d, want 3", r.WorktreeDirtyCount("/a"))
	}
	// /b disappears.
	r.SetWorktrees([]git.Worktree{{Path: "/a"}}, "/a")
	if _, present := r.worktreeDirtyCount["/b"]; present {
		t.Error("SetWorktrees should prune dirty entries for paths that disappeared")
	}
	if _, present := r.worktreeTimedOut["/b"]; present {
		t.Error("SetWorktrees should prune timedOut entries for paths that disappeared")
	}
}

func TestRefModelSetWorktreeSyncAndPrune(t *testing.T) {
	r := newRefsModel()
	r.SetWorktrees([]git.Worktree{{Path: "/a"}, {Path: "/b"}}, "/a")
	r.SetWorktreeSync("/a", 2, 1, true)
	r.SetWorktreeSync("/b", 0, 0, false)
	if s := r.WorktreeSync("/a"); s.ahead != 2 || s.behind != 1 || !s.hasUpstream {
		t.Errorf("sync /a = %+v, want {ahead 2 behind 1 hasUpstream true}", s)
	}
	// /b disappears — its sync entry must be pruned.
	r.SetWorktrees([]git.Worktree{{Path: "/a"}}, "/a")
	if _, present := r.worktreeSync["/b"]; present {
		t.Error("SetWorktrees should prune sync entries for paths that disappeared")
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
