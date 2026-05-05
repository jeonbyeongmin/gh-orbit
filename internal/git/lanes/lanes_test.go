package lanes

import (
	"testing"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

func TestAllocateLinearOnLaneZero(t *testing.T) {
	a := New()
	pairs := []RowPair{
		a.Push(git.Commit{Hash: "c", Parents: []string{"b"}}),
		a.Push(git.Commit{Hash: "b", Parents: []string{"a"}}),
		a.Push(git.Commit{Hash: "a"}),
	}

	// First push has no connector — there is nothing above the very first
	// commit in the log.
	if pairs[0].Connector.Cells != nil {
		t.Errorf("first push connector should be nil, got %+v", pairs[0].Connector.Cells)
	}
	for i, p := range pairs {
		if p.Commit.CommitLane != 0 {
			t.Errorf("pairs[%d].Commit.CommitLane = %d, want 0", i, p.Commit.CommitLane)
		}
		if len(p.Commit.Cells) != 1 || p.Commit.Cells[0].Kind != CellCommit {
			t.Errorf("pairs[%d].Commit.Cells = %+v, want single CellCommit", i, p.Commit.Cells)
		}
	}
	// Subsequent connectors are a single pipe carrying the lane down to
	// the next commit on the same column.
	for i := 1; i < len(pairs); i++ {
		want := []CellKind{CellPipe}
		got := kindsOf(pairs[i].Connector.Cells)
		if !equalKinds(got, want) {
			t.Errorf("pairs[%d].Connector kinds = %v, want %v", i, got, want)
		}
	}
}

// TestAllocateBranchAndMerge models the canonical "feat into main" graph:
//
//	●         m   parents [c, b]
//	├─╮       (connector below m, drawn above b)
//	│ ●       b   parents [a]
//	│ │       (connector above c — both lanes pass through)
//	● │       c   parents [a]
//	├─╯       (connector above a — lane 1 merges into lane 0)
//	●         a
func TestAllocateBranchAndMerge(t *testing.T) {
	a := New()
	pM := a.Push(git.Commit{Hash: "m", Parents: []string{"c", "b"}})
	pB := a.Push(git.Commit{Hash: "b", Parents: []string{"a"}})
	pC := a.Push(git.Commit{Hash: "c", Parents: []string{"a"}})
	pA := a.Push(git.Commit{Hash: "a"})

	// m is the first commit, so its connector is nil.
	if pM.Connector.Cells != nil {
		t.Errorf("m connector should be nil, got %+v", pM.Connector.Cells)
	}
	checkKinds(t, "m commit", pM.Commit, []CellKind{CellCommit}, 0)

	// b's connector encodes the fork spawned by m: ├─╮.
	checkKinds(t, "b connector", pB.Connector, []CellKind{CellTeeRight, CellCornerTR}, -1)
	checkKinds(t, "b commit", pB.Commit, []CellKind{CellPipe, CellCommit}, 1)

	// c's connector is just two pipes — no fork or merge transition here.
	checkKinds(t, "c connector", pC.Connector, []CellKind{CellPipe, CellPipe}, -1)
	checkKinds(t, "c commit", pC.Commit, []CellKind{CellCommit, CellPipe}, 0)

	// a's connector merges lane 1 into lane 0: ├─╯.
	checkKinds(t, "a connector", pA.Connector, []CellKind{CellTeeRight, CellCornerBR}, -1)
	// After the merge the lane is freed; the commit row carries the dot
	// at column 0 with column 1 blanked.
	checkKinds(t, "a commit", pA.Commit, []CellKind{CellCommit, CellEmpty}, 0)
}

// TestAllocateOctopusSpreadsForkOnConnector — three-parent merge spawns
// two extra lanes; the resulting connector lays them out on a single row
// as ├─┬─╮.
func TestAllocateOctopusSpreadsForkOnConnector(t *testing.T) {
	a := New()
	pM := a.Push(git.Commit{Hash: "m", Parents: []string{"a", "b", "c"}})
	pNext := a.Push(git.Commit{Hash: "a"})

	// m itself only places the dot — the fork visualization is deferred
	// to the next connector.
	checkKinds(t, "m commit", pM.Commit, []CellKind{CellCommit}, 0)

	// The next commit's connector spreads the octopus fork on one row.
	checkKinds(t, "next connector", pNext.Connector,
		[]CellKind{CellTeeRight, CellTeeDown, CellCornerTR}, -1)
	// And the commit row carries the dot at lane 0 with the two new
	// lanes (b, c) flowing through as pipes.
	checkKinds(t, "next commit", pNext.Commit,
		[]CellKind{CellCommit, CellPipe, CellPipe}, 0)
}

// TestAllocateRootFreesSlot — a root commit (no parents) frees its slot
// so a later, unrelated commit can reuse the same column.
func TestAllocateRootFreesSlot(t *testing.T) {
	a := New()
	pRoot := a.Push(git.Commit{Hash: "root"})
	pNext := a.Push(git.Commit{Hash: "next"})

	if pRoot.Commit.Cells[0].Kind != CellCommit {
		t.Fatalf("root commit row = %+v", pRoot.Commit.Cells)
	}
	if pNext.Commit.CommitLane != 0 {
		t.Errorf("next CommitLane = %d, want 0 (root's slot should have been freed)",
			pNext.Commit.CommitLane)
	}
	// The connector sits between root and next; root's slot was freed
	// before next was placed, so there is nothing to draw.
	if k := kindsOf(pNext.Connector.Cells); !allEmpty(k) {
		t.Errorf("next connector kinds = %v, want all empty", k)
	}
}

// TestCommitRowLaneEqualsColumn — on commit rows every non-empty cell's
// Lane matches its column index, so the renderer can color the dot and
// pass-through pipes by column. (Connector rows can use a different lane
// key for intermediate horizontals — that's the merging lane's color, not
// the column's, and is exercised by the visual fixtures above.)
func TestCommitRowLaneEqualsColumn(t *testing.T) {
	a := New()
	pairs := []RowPair{
		a.Push(git.Commit{Hash: "m", Parents: []string{"c", "b"}}),
		a.Push(git.Commit{Hash: "b", Parents: []string{"a"}}),
		a.Push(git.Commit{Hash: "c", Parents: []string{"a"}}),
	}
	for ri, p := range pairs {
		for ci, cell := range p.Commit.Cells {
			if cell.Kind == CellEmpty {
				continue
			}
			if cell.Lane != ci {
				t.Errorf("pairs[%d].Commit.Cells[%d].Lane = %d, want %d",
					ri, ci, cell.Lane, ci)
			}
		}
	}
}

// TestAllocateStreaming — Push works one commit at a time without needing
// the full input slice up front.
func TestAllocateStreaming(t *testing.T) {
	a := New()
	for _, h := range []string{"c", "b", "a"} {
		var parents []string
		if h != "a" {
			parents = []string{string(rune(int('a') + (int(h[0]) - int('a')) - 1))}
		}
		p := a.Push(git.Commit{Hash: h, Parents: parents})
		if len(p.Commit.Cells) == 0 {
			t.Fatalf("Push(%s) returned empty commit row", h)
		}
	}
}

func checkKinds(t *testing.T, label string, r Row, want []CellKind, wantCommitLane int) {
	t.Helper()
	if r.CommitLane != wantCommitLane {
		t.Errorf("%s: CommitLane = %d, want %d", label, r.CommitLane, wantCommitLane)
	}
	if len(r.Cells) != len(want) {
		t.Errorf("%s: cells len = %d, want %d (cells = %+v)",
			label, len(r.Cells), len(want), r.Cells)
		return
	}
	for i, k := range want {
		if r.Cells[i].Kind != k {
			t.Errorf("%s: cells[%d].Kind = %v, want %v",
				label, i, r.Cells[i].Kind, k)
		}
	}
}

func kindsOf(cells []Cell) []CellKind {
	out := make([]CellKind, len(cells))
	for i, c := range cells {
		out[i] = c.Kind
	}
	return out
}

func equalKinds(a, b []CellKind) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func allEmpty(ks []CellKind) bool {
	for _, k := range ks {
		if k != CellEmpty {
			return false
		}
	}
	return true
}
