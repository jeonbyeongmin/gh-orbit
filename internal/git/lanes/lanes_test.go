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

// TestAllocateLaneEqualsColumnIndex — every cell's Lane equals its column,
// so the renderer can color by column and the same vertical track keeps
// the same color even after a freed column is reused.
func TestAllocateLaneEqualsColumnIndex(t *testing.T) {
	a := New()
	rows := []Row{
		a.Push(git.Commit{Hash: "m", Parents: []string{"c", "b"}}),
		a.Push(git.Commit{Hash: "b", Parents: []string{"a"}}),
		a.Push(git.Commit{Hash: "c", Parents: []string{"a"}}),
	}
	for ri, r := range rows {
		for ci, cell := range r.Cells {
			if cell.Kind == CellEmpty {
				continue
			}
			if cell.Lane != ci {
				t.Errorf("rows[%d].Cells[%d].Lane = %d, want %d (column index)", ri, ci, cell.Lane, ci)
			}
		}
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
