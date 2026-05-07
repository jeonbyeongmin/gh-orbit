package tui

import (
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

func makeChangeFiles(n int) []git.FileStat {
	out := make([]git.FileStat, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, git.FileStat{
			Path:       fmt.Sprintf("f%02d.txt", i),
			Insertions: 1,
			Deletions:  0,
		})
	}
	return out
}

func TestChangesFileListEdgeFollowDown(t *testing.T) {
	c := newChangesModel()
	c.SetSize(80, 5)
	c.SetFiles("hash", makeChangeFiles(10))
	if c.cursor != 0 || c.fileListYOffset != 0 {
		t.Fatalf("initial state: cursor=%d yOffset=%d, want 0,0", c.cursor, c.fileListYOffset)
	}
	j := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}}
	// First 4 j presses: cursor walks 0→4 inside the visible window, yOffset stays.
	for i := 0; i < 4; i++ {
		c, _ = c.Update(j)
	}
	if c.cursor != 4 || c.fileListYOffset != 0 {
		t.Errorf("after 4 j: cursor=%d yOffset=%d, want 4,0", c.cursor, c.fileListYOffset)
	}
	// 5th j hits the bottom edge — yOffset slides by one.
	c, _ = c.Update(j)
	if c.cursor != 5 || c.fileListYOffset != 1 {
		t.Errorf("after 5 j: cursor=%d yOffset=%d, want 5,1", c.cursor, c.fileListYOffset)
	}
}

func TestChangesFileListEdgeFollowUp(t *testing.T) {
	c := newChangesModel()
	c.SetSize(80, 5)
	c.SetFiles("hash", makeChangeFiles(10))
	j := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}}
	k := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}}
	for i := 0; i < 8; i++ {
		c, _ = c.Update(j)
	}
	if c.fileListYOffset == 0 {
		t.Fatalf("setup: yOffset still 0 after 8 j")
	}
	yStart, cStart := c.fileListYOffset, c.cursor
	c, _ = c.Update(k)
	if c.cursor != cStart-1 {
		t.Errorf("k once: cursor=%d, want %d", c.cursor, cStart-1)
	}
	if c.fileListYOffset != yStart {
		t.Errorf("k inside window must leave yOffset alone: got %d, want %d", c.fileListYOffset, yStart)
	}
	// Now drive cursor down to yOffset itself, then k once more to cross the upper edge.
	for c.cursor > c.fileListYOffset {
		c, _ = c.Update(k)
	}
	yBefore := c.fileListYOffset
	c, _ = c.Update(k)
	if c.fileListYOffset != yBefore-1 {
		t.Errorf("k at upper edge: yOffset=%d, want %d", c.fileListYOffset, yBefore-1)
	}
}

func TestChangesFileListYOffsetClampOnSetSize(t *testing.T) {
	c := newChangesModel()
	c.SetSize(80, 5)
	c.SetFiles("hash", makeChangeFiles(20))
	j := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}}
	for i := 0; i < 19; i++ {
		c, _ = c.Update(j)
	}
	if c.cursor != 19 {
		t.Fatalf("cursor=19 expected, got %d", c.cursor)
	}
	if c.fileListYOffset == 0 {
		t.Fatalf("yOffset should be > 0 after 19 j with visibleH=5")
	}
	// Grow the window — yOffset should clamp down so trailing rows aren't blank.
	c.SetSize(80, 30)
	if c.fileListYOffset != 0 {
		t.Errorf("after height=30, yOffset=%d, want 0 (no trailing blanks)", c.fileListYOffset)
	}
	// Shrink tightly — cursor=19 must remain inside the visible window.
	c.SetSize(80, 3)
	if c.cursor < c.fileListYOffset || c.cursor >= c.fileListYOffset+3 {
		t.Errorf("cursor=%d out of view [%d, %d)", c.cursor, c.fileListYOffset, c.fileListYOffset+3)
	}
}

func TestChangesFileListGGoToTopBottom(t *testing.T) {
	c := newChangesModel()
	c.SetSize(80, 5)
	c.SetFiles("hash", makeChangeFiles(20))
	G := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}}
	g := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}}
	c, _ = c.Update(G)
	if c.cursor != 19 {
		t.Errorf("G: cursor=%d, want 19", c.cursor)
	}
	if c.fileListYOffset != 19-5+1 {
		t.Errorf("G: yOffset=%d, want %d", c.fileListYOffset, 19-5+1)
	}
	c, _ = c.Update(g)
	if c.cursor != 0 || c.fileListYOffset != 0 {
		t.Errorf("g: cursor=%d yOffset=%d, want 0,0", c.cursor, c.fileListYOffset)
	}
}

func TestChangesMarkPendingResetsYOffset(t *testing.T) {
	c := newChangesModel()
	c.SetSize(80, 5)
	c.SetFiles("hash1", makeChangeFiles(20))
	j := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}}
	for i := 0; i < 10; i++ {
		c, _ = c.Update(j)
	}
	if c.fileListYOffset == 0 {
		t.Fatalf("setup: yOffset should be > 0")
	}
	c.MarkPending("hash2")
	if c.cursor != 0 || c.fileListYOffset != 0 {
		t.Errorf("after MarkPending: cursor=%d yOffset=%d, want 0,0", c.cursor, c.fileListYOffset)
	}
}
