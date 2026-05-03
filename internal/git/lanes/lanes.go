// Package lanes computes commit-graph lane layouts for the TUI's commit list.
//
// One lane = one vertical track waiting for the next commit hash that should
// land on it. Push(c) finds c.Hash among the active lanes (one or many —
// many means several branches merge into c), picks the leftmost match as
// c's column, draws merge cells on the others, then advances state:
// c.Parents[0] inherits c's slot, c.Parents[1:] each open a fresh slot to
// the right and emit a fork cell on the same row.
//
// One commit = one row. Connector-only rows (lazygit's "│ │ │" between
// commit lines) are not produced — the renderer compresses fork/merge
// glyphs onto the commit row itself so the bubbles/list widget keeps a
// 1:1 mapping between rows and commits.
package lanes

import (
	"slices"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// CellKind names every shape the renderer needs to know about.
type CellKind uint8

const (
	CellEmpty      CellKind = iota
	CellPipe                // │   lane passes through
	CellCommit              // ●   commit dot at this column
	CellMergeLeft           // ╲   diagonal meeting the commit from the left
	CellMergeRight          // ╱   diagonal meeting the commit from the right
	CellForkLeft            // ╱   diagonal leaving the commit toward the left
	CellForkRight           // ╲   diagonal leaving the commit toward the right
)

// Cell is one column of one row.
type Cell struct {
	Kind CellKind
	// Lane is the column index; the renderer uses it as the color rotation
	// key so every cell on the same vertical track shares a color, even
	// when a freed column is later reused by a different branch.
	Lane int
}

// Row is one commit's worth of layout. CommitLane is the column index of
// the dot.
type Row struct {
	Cells      []Cell
	CommitLane int
}

// Allocator streams commits into rows, one Push per commit. Callers must
// feed commits in the order git.Log returns them (newest first / child →
// parent).
type Allocator struct {
	// slots[i] = next-expected commit hash on column i, "" if free.
	slots []string
}

// New returns a fresh allocator.
func New() *Allocator { return &Allocator{} }

// Push lays out one commit and returns its row.
func (a *Allocator) Push(c git.Commit) Row {
	merging := a.findMergingCols(c.Hash)
	commitCol := a.placeCommit(merging)
	cells := a.buildRow(commitCol, merging)
	a.advanceState(commitCol, merging, c.Parents)
	cells = a.appendForkCells(cells, commitCol, c.Parents)
	return Row{Cells: cells, CommitLane: commitCol}
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

func (a *Allocator) buildRow(commitCol int, merging []int) []Cell {
	width := len(a.slots)
	if commitCol >= width {
		width = commitCol + 1
	}
	cells := make([]Cell, width)
	for i := 0; i < width; i++ {
		switch {
		case i == commitCol:
			cells[i] = Cell{Kind: CellCommit, Lane: i}
		case slices.Contains(merging, i):
			kind := CellMergeRight
			if i < commitCol {
				kind = CellMergeLeft
			}
			cells[i] = Cell{Kind: kind, Lane: i}
		case i < len(a.slots) && a.slots[i] != "":
			cells[i] = Cell{Kind: CellPipe, Lane: i}
		default:
			cells[i] = Cell{Kind: CellEmpty}
		}
	}
	return cells
}

func (a *Allocator) advanceState(commitCol int, merging []int, parents []string) {
	if len(merging) > 1 {
		for _, s := range merging[1:] {
			a.slots[s] = ""
		}
	}
	a.growSlotsTo(commitCol + 1)
	if len(parents) >= 1 {
		a.slots[commitCol] = parents[0]
	} else {
		a.slots[commitCol] = ""
	}
}

func (a *Allocator) appendForkCells(cells []Cell, commitCol int, parents []string) []Cell {
	if len(parents) <= 1 {
		return cells
	}
	for _, p := range parents[1:] {
		s := a.firstFree()
		a.growSlotsTo(s + 1)
		a.slots[s] = p

		for len(cells) <= s {
			cells = append(cells, Cell{Kind: CellEmpty})
		}
		kind := CellForkRight
		if s < commitCol {
			kind = CellForkLeft
		}
		cells[s] = Cell{Kind: kind, Lane: s}
	}
	return cells
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
