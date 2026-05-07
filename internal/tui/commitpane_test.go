package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

func TestCommitDetailLoadedFlowsThroughViewport(t *testing.T) {
	c := newCommitDetailModel()
	c.SetSize(80, 20)
	c.MarkLoading("abc1234", 1)
	c.ApplyDetailLoaded(1, "abc1234", git.Detail{
		Hash:        "abc1234",
		AuthorName:  "Alice",
		AuthorEmail: "alice@example.com",
		Body:        "subject line\n\nbody paragraph",
	})
	if c.loading {
		t.Error("loading flag must clear after ApplyDetailLoaded")
	}
	if !c.loaded {
		t.Error("loaded flag must set after ApplyDetailLoaded")
	}
	if !strings.Contains(c.View(), "subject line") {
		t.Errorf("view should include the body subject, got %q", c.View())
	}
}

func TestCommitDetailMarkLoadingResetsViewportOffset(t *testing.T) {
	c := newCommitDetailModel()
	c.SetSize(40, 3) // small viewport so a long body overflows.
	c.MarkLoading("hash1", 1)
	c.ApplyDetailLoaded(1, "hash1", git.Detail{
		Hash: "hash1",
		Body: strings.Repeat("line\n", 50),
	})
	// Scroll well past the top.
	for i := 0; i < 20; i++ {
		c.ScrollContent(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	}
	if c.viewport.YOffset == 0 {
		t.Fatalf("setup: viewport should have scrolled, YOffset still 0")
	}
	// New commit selection — viewport must snap to the top.
	c.MarkLoading("hash2", 2)
	if c.viewport.YOffset != 0 {
		t.Errorf("MarkLoading must reset viewport YOffset, got %d", c.viewport.YOffset)
	}
}

func TestCommitDetailSetSizeReWrapsBody(t *testing.T) {
	c := newCommitDetailModel()
	c.SetSize(120, 20)
	longLine := strings.Repeat("word ", 30) // ~150 cols, no embedded newlines.
	c.MarkLoading("hash", 1)
	c.ApplyDetailLoaded(1, "hash", git.Detail{Hash: "hash", Body: longLine})
	wide := c.renderContent()
	c.SetSize(40, 20) // shrink width — body should soft-wrap onto more lines.
	narrow := c.renderContent()
	if strings.Count(narrow, "\n") <= strings.Count(wide, "\n") {
		t.Errorf("expected more newlines after width shrink (re-wrap): wide=%d narrow=%d",
			strings.Count(wide, "\n"), strings.Count(narrow, "\n"))
	}
}

func TestCommitDetailStaleReqIDIsDropped(t *testing.T) {
	c := newCommitDetailModel()
	c.MarkLoading("current", 5)
	c.ApplyDetailLoaded(2, "old", git.Detail{Hash: "old", Body: "stale body"})
	if !c.loading {
		t.Error("stale ApplyDetailLoaded must not clear loading flag")
	}
	if c.loaded {
		t.Error("stale ApplyDetailLoaded must not set loaded flag")
	}
	if strings.Contains(c.View(), "stale") {
		t.Errorf("stale body must not flow into the viewport, got %q", c.View())
	}
}

func TestCommitDetailScrollContentJK(t *testing.T) {
	c := newCommitDetailModel()
	c.SetSize(40, 3)
	c.MarkLoading("hash", 1)
	c.ApplyDetailLoaded(1, "hash", git.Detail{
		Hash: "hash",
		Body: strings.Repeat("line\n", 30),
	})
	c.ScrollContent(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if c.viewport.YOffset == 0 {
		t.Errorf("j must scroll viewport down, YOffset stayed 0")
	}
	yAfterJ := c.viewport.YOffset
	c.ScrollContent(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if c.viewport.YOffset >= yAfterJ {
		t.Errorf("k must scroll up, YOffset = %d (was %d)", c.viewport.YOffset, yAfterJ)
	}
}

func TestCommitDetailScrollContentGoesToTopBottom(t *testing.T) {
	c := newCommitDetailModel()
	c.SetSize(40, 3)
	c.MarkLoading("hash", 1)
	c.ApplyDetailLoaded(1, "hash", git.Detail{
		Hash: "hash",
		Body: strings.Repeat("line\n", 30),
	})
	c.ScrollContent(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	if c.viewport.YOffset == 0 {
		t.Errorf("G must jump to bottom, YOffset still 0")
	}
	c.ScrollContent(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	if c.viewport.YOffset != 0 {
		t.Errorf("g must jump to top, YOffset = %d", c.viewport.YOffset)
	}
}
