// Package lanes computes commit-graph lane layouts for the TUI's commit list.
//
// One lane = one vertical track waiting for the next commit hash that should
// land on it. Push(c) returns a RowPair: a Connector row drawn between the
// previous commit and c, plus c's own Commit row. The connector encodes the
// lane transitions (forks opened by the previous commit, merges arriving at
// c) using orthogonal box-drawing cells; the commit row carries the dot and
// pass-through pipes only.
//
// The renderer keeps a 1:1 mapping between commits and bubbles/list items —
// each list item carries both rows and renders them as a 2-line block via a
// Height()=2 delegate, so j/k navigation still moves one commit per press.
package lanes

import (
	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// CellKind names every shape the renderer needs to know about. The connector
// kinds encode the four "open sides" of an orthogonal box-drawing glyph:
//
//	 U
//	─┼─
//	 D
//
// L/R/U/D bits are derived in the renderer to decide whether the trailing
// space between two adjacent cells should be filled with `─` (both sides
// open toward each other) or left as whitespace.
type CellKind uint8

const (
	CellEmpty    CellKind = iota
	CellCommit            // ●  — commit dot
	CellPipe              // │  — vertical pass-through (U,D)
	CellHoriz             // ─  — horizontal connector (L,R)
	CellCornerTL          // ╭  — corner, opens R+D
	CellCornerTR          // ╮  — corner, opens L+D
	CellCornerBL          // ╰  — corner, opens U+R
	CellCornerBR          // ╯  — corner, opens U+L
	CellTeeRight          // ├  — pipe with arm right (U,D,R)
	CellTeeLeft           // ┤  — pipe with arm left (U,D,L)
	CellTeeDown           // ┬  — horizontal with arm down (L,R,D)
	CellTeeUp             // ┴  — horizontal with arm up (L,R,U)
	CellCross             // ┼  — full cross (U,D,L,R)
)

// Cell is one column of one row.
type Cell struct {
	Kind CellKind
	// Lane is the color rotation key. On commit rows it equals the column
	// index — same vertical track keeps the same color, even when a freed
	// column is later reused by a different branch. On connector rows the
	// horizontal pass-through cells between a fork's source and a merge's
	// arm carry the merging/forking lane's key (not the column they sit
	// in), so the routing line stays one continuous color.
	Lane int
}

// Row is one rendered line. CommitLane is the column index of the commit
// dot for commit rows, or -1 for connector rows.
type Row struct {
	Cells      []Cell
	CommitLane int
}

// RowPair is what one Push call produces: a Connector row drawn above the
// commit (between this commit and the previous one), and the Commit row
// itself.
//
// For the very first commit pushed, Connector.Cells is nil — there is no
// previous commit to connect to. The renderer treats a nil-Cells row as an
// empty visual line.
type RowPair struct {
	Connector Row
	Commit    Row
}

// Allocator streams commits into rows, one Push per commit. Callers must
// feed commits in the order git.Log returns them (newest first / child →
// parent).
type Allocator struct {
	// slots[i] = next-expected commit hash on column i, "" if free.
	slots []string
	// pendingForks[i] = lane opened by the previous Push for an extra
	// parent. The next Push uses it to draw the fork in its connector row.
	pendingForks []forkInfo
	// firstPush stays true until the first Push completes — used to
	// suppress the connector row for the very first commit.
	firstPush bool
}

// forkInfo records "the previous commit at column from spawned a new lane
// at column to", so the next connector row can draw the corner pieces.
type forkInfo struct {
	from int
	to   int
}

// New returns a fresh allocator.
func New() *Allocator { return &Allocator{firstPush: true} }

// Push lays out one commit and returns its row pair.
func (a *Allocator) Push(c git.Commit) RowPair {
	merging := a.findMergingCols(c.Hash)
	commitCol := a.placeCommit(merging)
	mergeArms := mergeArmsFrom(merging)

	var connector Row
	if !a.firstPush {
		connector = a.buildConnector(commitCol, mergeArms)
	}
	a.firstPush = false

	// Consolidate: free merge arms before drawing the commit row.
	for _, s := range mergeArms {
		a.slots[s] = ""
	}
	a.growSlotsTo(commitCol + 1)

	commit := a.buildCommitRow(commitCol)

	// Advance state for the next Push.
	if len(c.Parents) >= 1 {
		a.slots[commitCol] = c.Parents[0]
	} else {
		a.slots[commitCol] = ""
	}
	a.pendingForks = a.pendingForks[:0]
	if len(c.Parents) > 1 {
		for _, p := range c.Parents[1:] {
			s := a.firstFree()
			a.growSlotsTo(s + 1)
			a.slots[s] = p
			a.pendingForks = append(a.pendingForks, forkInfo{from: commitCol, to: s})
		}
	}

	return RowPair{Connector: connector, Commit: commit}
}

func mergeArmsFrom(merging []int) []int {
	if len(merging) <= 1 {
		return nil
	}
	return merging[1:]
}

func (a *Allocator) findMergingCols(hash string) []int {
	var out []int
	for i, h := range a.slots {
		if h == hash {
			out = append(out, i)
		}
	}
	return out
}

func (a *Allocator) placeCommit(merging []int) int {
	if len(merging) == 0 {
		return a.firstFree()
	}
	return merging[0]
}

// laneFlags is the per-cell open-sides bitmap used while building a connector
// row. After every transition is recorded the flags collapse into a single
// CellKind via kindFromFlags.
type laneFlags struct {
	up, down, left, right bool
	// laneOf is the color rotation key. -1 means "no owning lane" so the
	// cell stays empty.
	laneOf int
}

func (a *Allocator) buildConnector(commitCol int, mergeArms []int) Row {
	width := len(a.slots)
	if commitCol >= width {
		width = commitCol + 1
	}
	for _, f := range a.pendingForks {
		if f.to >= width {
			width = f.to + 1
		}
	}
	for _, arm := range mergeArms {
		if arm >= width {
			width = arm + 1
		}
	}

	flags := make([]laneFlags, width)
	for i := range flags {
		flags[i].laneOf = -1
	}

	// Pass 1: every lane already alive in the previous state passes
	// through. Lanes opened by the previous Push (pendingForks.to) are
	// excluded — they don't exist above this connector row.
	for i, h := range a.slots {
		if h == "" || isPendingForkTarget(a.pendingForks, i) {
			continue
		}
		flags[i].up = true
		flags[i].down = true
		flags[i].laneOf = i
	}

	// Pass 2: forks opened by the previous commit. The source lane gains
	// a right-arm; the destination lane is born here (no up), going down
	// after a corner-from-left. Intermediate columns get a horizontal
	// pass-through.
	for _, f := range a.pendingForks {
		flags[f.from].right = true
		if flags[f.from].laneOf == -1 {
			flags[f.from].laneOf = f.from
		}
		flags[f.to].left = true
		flags[f.to].down = true
		if flags[f.to].laneOf == -1 {
			flags[f.to].laneOf = f.to
		}
		for j := f.from + 1; j < f.to; j++ {
			flags[j].left = true
			flags[j].right = true
			if flags[j].laneOf == -1 {
				flags[j].laneOf = f.from
			}
		}
	}

	// Pass 3: merge arms — lanes that were carrying this commit's hash
	// from above and now have to bend into commitCol. The arm lane's
	// down is cleared (the lane terminates at this connector); commitCol
	// gains a left or right arm matching the bend direction.
	for _, arm := range mergeArms {
		if arm == commitCol {
			continue // defensive; mergeArms excludes commitCol already
		}
		if arm > commitCol {
			flags[arm].up = true
			flags[arm].left = true
			flags[arm].down = false
			flags[arm].laneOf = arm
			for j := commitCol + 1; j < arm; j++ {
				flags[j].left = true
				flags[j].right = true
				if flags[j].laneOf == -1 {
					flags[j].laneOf = arm
				}
			}
			flags[commitCol].right = true
		} else {
			flags[arm].up = true
			flags[arm].right = true
			flags[arm].down = false
			flags[arm].laneOf = arm
			for j := arm + 1; j < commitCol; j++ {
				flags[j].left = true
				flags[j].right = true
				if flags[j].laneOf == -1 {
					flags[j].laneOf = arm
				}
			}
			flags[commitCol].left = true
		}
		if flags[commitCol].laneOf == -1 {
			flags[commitCol].laneOf = commitCol
		}
	}

	cells := make([]Cell, width)
	for i := 0; i < width; i++ {
		k := kindFromFlags(flags[i])
		if k == CellEmpty {
			cells[i] = Cell{Kind: CellEmpty}
			continue
		}
		lane := flags[i].laneOf
		if lane < 0 {
			lane = i
		}
		cells[i] = Cell{Kind: k, Lane: lane}
	}
	return Row{Cells: cells, CommitLane: -1}
}

// isPendingForkTarget reports whether column i is the destination of a
// fork opened by the previous Push. pendingForks is typically 0–2 entries
// (octopus is rare), so a linear scan beats a map allocation per call.
func isPendingForkTarget(forks []forkInfo, i int) bool {
	for _, f := range forks {
		if f.to == i {
			return true
		}
	}
	return false
}

func kindFromFlags(f laneFlags) CellKind {
	u, d, l, r := f.up, f.down, f.left, f.right
	switch {
	case !u && !d && !l && !r:
		return CellEmpty
	case u && d && !l && !r:
		return CellPipe
	case !u && !d && l && r:
		return CellHoriz
	case !u && d && !l && r:
		return CellCornerTL
	case !u && d && l && !r:
		return CellCornerTR
	case u && !d && !l && r:
		return CellCornerBL
	case u && !d && l && !r:
		return CellCornerBR
	case u && d && !l && r:
		return CellTeeRight
	case u && d && l && !r:
		return CellTeeLeft
	case !u && d && l && r:
		return CellTeeDown
	case u && !d && l && r:
		return CellTeeUp
	case u && d && l && r:
		return CellCross
	}
	// Half-open shapes (only one bit set) collapse to whichever axis they
	// belong to. They shouldn't appear in well-formed input but render as
	// pipe/horizontal so the lane stays visible instead of disappearing.
	if u || d {
		return CellPipe
	}
	if l || r {
		return CellHoriz
	}
	return CellEmpty
}

func (a *Allocator) buildCommitRow(commitCol int) Row {
	width := len(a.slots)
	if commitCol >= width {
		width = commitCol + 1
	}
	cells := make([]Cell, width)
	for i := 0; i < width; i++ {
		switch {
		case i == commitCol:
			cells[i] = Cell{Kind: CellCommit, Lane: i}
		case i < len(a.slots) && a.slots[i] != "":
			cells[i] = Cell{Kind: CellPipe, Lane: i}
		default:
			cells[i] = Cell{Kind: CellEmpty}
		}
	}
	return Row{Cells: cells, CommitLane: commitCol}
}

func (a *Allocator) firstFree() int {
	for i, h := range a.slots {
		if h == "" {
			return i
		}
	}
	return len(a.slots)
}

func (a *Allocator) growSlotsTo(n int) {
	for len(a.slots) < n {
		a.slots = append(a.slots, "")
	}
}
