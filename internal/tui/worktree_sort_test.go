package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// withModel seeds a sized model with a worktree inventory — shared by the
// sort and modal tests.
func withModel(t *testing.T, worktrees []git.Worktree, currentPath string) Model {
	t.Helper()
	m := initSized(t)
	m.refs.SetWorktrees(worktrees, currentPath)
	m.refs, _ = m.refs.Update(refsLoadedMsg{refs: nil})
	return m
}

// paths is a small helper so order assertions read as a path slice.
func paths(wts []git.Worktree) []string {
	out := make([]string, len(wts))
	for i, wt := range wts {
		out[i] = wt.Path
	}
	return out
}

func eqStrings(a, b []string) bool {
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

// sortFixture seeds a 4-tree model: main (pinned), two dated feature
// trees, and one with no last-commit (zero `when`). Natural order is the
// slice order below.
func sortFixture(t *testing.T) Model {
	t.Helper()
	m := withModel(t,
		[]git.Worktree{
			{Path: "/main", Branch: "develop", IsMain: true},
			{Path: "/old", Branch: "feat/old"},
			{Path: "/new", Branch: "feat/new"},
			{Path: "/unknown", Branch: "feat/unk"},
		},
		"/main",
	)
	now := time.Now()
	m.refs.SetWorktreeLastCommit("/main", "init", now.Add(-100*time.Hour))
	m.refs.SetWorktreeLastCommit("/old", "old work", now.Add(-10*time.Hour))
	m.refs.SetWorktreeLastCommit("/new", "fresh", now.Add(-1*time.Hour))
	// "/unknown" deliberately left unset → zero-value `when`.
	return m
}

func TestModalWorktreesNaturalOrderWhenSortOff(t *testing.T) {
	m := sortFixture(t)
	// sortByCommit defaults to false.
	got := paths(m.modalWorktrees())
	want := []string{"/main", "/old", "/new", "/unknown"}
	if !eqStrings(got, want) {
		t.Errorf("sort off should keep git natural order: got %v, want %v", got, want)
	}
}

func TestModalWorktreesSortPinsMainAndDescends(t *testing.T) {
	m := sortFixture(t)
	m.worktreesModal.sortByCommit = true
	got := paths(m.modalWorktrees())
	// main pinned, then newest→oldest, unknown (zero when) last.
	want := []string{"/main", "/new", "/old", "/unknown"}
	if !eqStrings(got, want) {
		t.Errorf("sort on should pin main then descend by last-commit, unknown last: got %v, want %v", got, want)
	}
}

func TestModalWorktreesSortStableTieBreak(t *testing.T) {
	// Two unknown-when trees keep their original relative order under the
	// stable sort.
	m := withModel(t,
		[]git.Worktree{
			{Path: "/main", IsMain: true},
			{Path: "/unk-a", Branch: "feat/a"},
			{Path: "/unk-b", Branch: "feat/b"},
		},
		"/main",
	)
	m.worktreesModal.sortByCommit = true
	got := paths(m.modalWorktrees())
	want := []string{"/main", "/unk-a", "/unk-b"}
	if !eqStrings(got, want) {
		t.Errorf("equal (unknown) keys should preserve original order: got %v, want %v", got, want)
	}
}

func TestWorktreesModalToggleSortPreservesCursorWorktree(t *testing.T) {
	m := sortFixture(t)
	// Cursor on "/old" (natural index 1). After toggle the order becomes
	// [/main, /new, /old, /unknown] → "/old" moves to index 2; the cursor
	// must follow the same worktree, not stay on index 1.
	m.worktreesModal.cursor = 1
	m = m.worktreesModalToggleSort()
	if !m.worktreesModal.sortByCommit {
		t.Fatal("toggle should flip sortByCommit to true")
	}
	got := m.modalWorktrees()[m.worktreesModal.cursor].Path
	if got != "/old" {
		t.Errorf("cursor should still point at /old after sort, got %q (cursor=%d)", got, m.worktreesModal.cursor)
	}
	// Toggle back: order returns to natural, cursor stays on /old (index 1).
	m = m.worktreesModalToggleSort()
	if m.worktreesModal.sortByCommit {
		t.Fatal("second toggle should flip sortByCommit back to false")
	}
	if got := m.modalWorktrees()[m.worktreesModal.cursor].Path; got != "/old" {
		t.Errorf("cursor should still point at /old after toggle-off, got %q", got)
	}
}

func TestWorktreesModalHeaderSortTagOnlyWhenSorting(t *testing.T) {
	m := sortFixture(t)
	header := func() string {
		return strings.SplitN(ansi.Strip(m.renderWorktreesView(60, 20)), "\n", 2)[0]
	}
	if strings.Contains(header(), "↓time") {
		t.Errorf("sort tag should be absent when sort off: %q", header())
	}
	m.worktreesModal.sortByCommit = true
	if !strings.Contains(header(), "↓time") {
		t.Errorf("sort tag ↓time should appear in header when sorting: %q", header())
	}
}

func TestModalWorktreesSortDoesNotMutateOriginal(t *testing.T) {
	m := sortFixture(t)
	m.worktreesModal.sortByCommit = true
	_ = m.modalWorktrees()
	// The backing slice exposed by Worktrees() must stay in git order.
	got := paths(m.refs.Worktrees())
	want := []string{"/main", "/old", "/new", "/unknown"}
	if !eqStrings(got, want) {
		t.Errorf("modalWorktrees must not mutate the original slice: got %v, want %v", got, want)
	}
}
