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
	if got, want := m.currentRefs, []string{"refs/heads/feat"}; !equalStrings(got, want) {
		t.Errorf("currentRefs should track selected ref: got %v want %v", got, want)
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
	if got, want := m.currentRefs, []string{refsAllSentinel}; !equalStrings(got, want) {
		t.Errorf("currentRefs should track --all sentinel: got %v want %v", got, want)
	}
}

func TestModelRKeyReloadsBothPanes(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	updated, _ = m.Update(commitsLoadedMsg{rows: []graphRow{
		{commit: git.Commit{Hash: "abc1234", Subject: "first", AuthorTime: time.Now()}},
	}})
	m = updated.(Model)
	updated, _ = m.Update(refsLoadedMsg{refs: []git.Ref{
		{FullName: "refs/heads/main", ShortName: "main", Kind: git.RefKindLocal},
	}})
	m = updated.(Model)
	if !m.graph.loaded || !m.refs.loaded {
		t.Fatalf("both panes should be loaded before R")
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	m = updated.(Model)
	if m.graph.loaded {
		t.Errorf("R should reset graph.loaded")
	}
	if m.refs.loaded {
		t.Errorf("R should reset refs.loaded")
	}
	if cmd == nil {
		t.Fatal("R should return a batched load cmd")
	}
	if !strings.Contains(m.graph.View(), "loading") {
		t.Errorf("graph view should show loading after R, got %q", m.graph.View())
	}
	if !strings.Contains(m.refs.View(), "loading") {
		t.Errorf("refs view should show loading after R, got %q", m.refs.View())
	}
}

func TestModelRKeyPreservesCurrentRefs(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	updated, _ = m.Update(refSelectedMsg{ref: git.Ref{FullName: "refs/heads/feat", Kind: git.RefKindLocal}})
	m = updated.(Model)
	updated, _ = m.Update(commitsLoadedMsg{rows: []graphRow{
		{commit: git.Commit{Hash: "abc1234", Subject: "first", AuthorTime: time.Now()}},
	}})
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	m = updated.(Model)
	if got, want := m.currentRefs, []string{"refs/heads/feat"}; !equalStrings(got, want) {
		t.Errorf("R should preserve currentRefs: got %v want %v", got, want)
	}
}

func equalStrings(a, b []string) bool {
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
