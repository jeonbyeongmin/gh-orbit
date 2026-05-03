package lanes

import (
	"testing"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

func TestAllocateLinear(t *testing.T) {
	a := New()
	for i, hash := range []struct {
		hash    string
		parents []string
	}{
		{"c", []string{"b"}},
		{"b", []string{"a"}},
		{"a", nil},
	} {
		r := a.Push(git.Commit{Hash: hash.hash, Parents: hash.parents})
		if r.CommitLane != 0 {
			t.Errorf("row %d (%s): CommitLane = %d, want 0", i, hash.hash, r.CommitLane)
		}
		if len(r.Cells) != 1 || r.Cells[0].Kind != CellCommit {
			t.Errorf("row %d (%s): cells = %+v, want single CellCommit", i, hash.hash, r.Cells)
		}
	}
}

// TestAllocateBranchAndMerge models the canonical "feat into main" graph:
//
//	●╲     m   parents [c, b]
//	│ ●    b   parents [a]
//	●╱     c   parents [a]    -- lane 1 (b) and lane 0 (c) both expect a
//	●      a
func TestAllocateBranchAndMerge(t *testing.T) {
	a := New()
	rM := a.Push(git.Commit{Hash: "m", Parents: []string{"c", "b"}})
	rB := a.Push(git.Commit{Hash: "b", Parents: []string{"a"}})
	rC := a.Push(git.Commit{Hash: "c", Parents: []string{"a"}})
	rA := a.Push(git.Commit{Hash: "a"})

	checkKinds(t, "rM", rM, []CellKind{CellCommit, CellForkRight}, 0)
	checkKinds(t, "rB", rB, []CellKind{CellPipe, CellCommit}, 1)
	checkKinds(t, "rC", rC, []CellKind{CellCommit, CellPipe}, 0)
	checkKinds(t, "rA", rA, []CellKind{CellCommit, CellMergeRight}, 0)
}

// TestAllocateOctopus — three-parent merge spawns two extra lanes to the right.
func TestAllocateOctopus(t *testing.T) {
	a := New()
	r := a.Push(git.Commit{Hash: "m", Parents: []string{"a", "b", "c"}})
	checkKinds(t, "octopus", r, []CellKind{CellCommit, CellForkRight, CellForkRight}, 0)
}

// TestAllocateRootClearsLane — a root commit (no parents) frees its slot so
// later commits can reuse it.
func TestAllocateRootClearsLane(t *testing.T) {
	a := New()
	rRoot := a.Push(git.Commit{Hash: "root"})
	if rRoot.Cells[0].Kind != CellCommit {
		t.Fatalf("root row = %+v", rRoot.Cells)
	}
	// The next, unrelated commit should also land on column 0.
	rNext := a.Push(git.Commit{Hash: "next"})
	if rNext.CommitLane != 0 {
		t.Errorf("next CommitLane = %d, want 0 (root's slot should have been freed)", rNext.CommitLane)
	}
}

// TestAllocateLaneColorsStableAcrossBranch — once a branch's color is
// assigned, it stays the same for every row of that branch's lifetime.
func TestAllocateLaneColorsStableAcrossBranch(t *testing.T) {
	a := New()
	rM := a.Push(git.Commit{Hash: "m", Parents: []string{"c", "b"}})
	rB := a.Push(git.Commit{Hash: "b", Parents: []string{"a"}})
	rC := a.Push(git.Commit{Hash: "c", Parents: []string{"a"}})

	mainColor := rM.Cells[0].Lane
	featColor := rM.Cells[1].Lane
	if mainColor == featColor {
		t.Errorf("main and feat lanes share color %d — must differ", mainColor)
	}
	if rC.Cells[0].Lane != mainColor {
		t.Errorf("rC lane 0 color = %d, want %d (main lane)", rC.Cells[0].Lane, mainColor)
	}
	if rB.Cells[1].Lane != featColor {
		t.Errorf("rB lane 1 color = %d, want %d (feat lane)", rB.Cells[1].Lane, featColor)
	}
}

// TestAllocateStreaming — Push works one commit at a time without needing
// the input slice up front.
func TestAllocateStreaming(t *testing.T) {
	a := New()
	for _, h := range []string{"c", "b", "a"} {
		var parents []string
		if h != "a" {
			parents = []string{string(rune(int('a') + (int(h[0]) - int('a')) - 1))}
		}
		r := a.Push(git.Commit{Hash: h, Parents: parents})
		if len(r.Cells) == 0 {
			t.Fatalf("Push(%s) returned empty row", h)
		}
	}
}

func checkKinds(t *testing.T, label string, r Row, want []CellKind, wantCommitLane int) {
	t.Helper()
	if r.CommitLane != wantCommitLane {
		t.Errorf("%s: CommitLane = %d, want %d", label, r.CommitLane, wantCommitLane)
	}
	if len(r.Cells) != len(want) {
		t.Errorf("%s: cells len = %d, want %d (cells = %+v)", label, len(r.Cells), len(want), r.Cells)
		return
	}
	for i, k := range want {
		if r.Cells[i].Kind != k {
			t.Errorf("%s: cells[%d].Kind = %v, want %v", label, i, r.Cells[i].Kind, k)
		}
	}
}
