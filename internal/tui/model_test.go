package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

func TestModelRefSelectedReloadsGraph(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	updated, _ = m.Update(commitsLoadedMsg{rows: []graphRow{
		{commit: git.Commit{Hash: "abc1234", Subject: "first", AuthorTime: time.Now()}},
	}})
	m = updated.(Model)
	if !m.graph.loaded {
		t.Fatalf("graph should be loaded before refSelectedMsg")
	}

	updated, cmd := m.Update(refSelectedMsg{ref: git.Ref{FullName: "refs/heads/feat", Kind: git.RefKindLocal}})
	m = updated.(Model)
	if m.graph.loaded {
		t.Errorf("graph.loaded should reset to false after refSelectedMsg")
	}
	if cmd == nil {
		t.Fatal("refSelectedMsg should return a load cmd")
	}
	if !strings.Contains(m.graph.View(), "loading") {
		t.Errorf("graph view should show loading state, got %q", m.graph.View())
	}
}

func TestModelAllKeyOnRefsPaneReloadsGraph(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	// Move focus to refs pane.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	m = updated.(Model)
	if m.focused != paneRefs {
		t.Fatalf("focus should be paneRefs after 'h', got %v", m.focused)
	}

	updated, _ = m.Update(commitsLoadedMsg{rows: []graphRow{
		{commit: git.Commit{Hash: "abc1234", Subject: "first", AuthorTime: time.Now()}},
	}})
	m = updated.(Model)
	if !m.graph.loaded {
		t.Fatalf("graph should be loaded before 'a'")
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = updated.(Model)
	if m.graph.loaded {
		t.Errorf("'a' on refs pane should reset graph for reload")
	}
	if cmd == nil {
		t.Error("'a' on refs pane should return a load cmd")
	}
}
