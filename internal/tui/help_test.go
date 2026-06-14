package tui

import (
	"strings"
	"testing"
)

// TestHelpDataCoverage guards against silently dropping a binding from the
// expanded panel. The list mirrors the keys that the focused-pane bottom
// hint carries — every refactor of the panel must keep them all.
func TestHelpDataCoverage(t *testing.T) {
	required := []string{
		"j/k", "enter", "y", "d", "F", "p", "P", "r", "R", "c", "n", "m",
		"^C ^C", "tab/⇧tab", "space", "b",
	}

	var have []string
	for _, c := range helpData() {
		for _, e := range c.entries {
			have = append(have, e.keys)
		}
	}
	joined := strings.Join(have, "|")
	for _, k := range required {
		if !strings.Contains(joined, k) {
			t.Errorf("helpData() missing key %q (got entries: %v)", k, have)
		}
	}
}

func TestRenderHelpPanelLineCount(t *testing.T) {
	cats := helpData()
	panelRows := 2 * len(cats)
	out := renderHelpPanel(cats, 120, panelRows)
	got := strings.Count(out, "\n") + 1
	if got > panelRows {
		t.Errorf("renderHelpPanel emitted %d lines, want ≤ %d", got, panelRows)
	}
	for _, want := range []string{"[Global]", "[Graph]", "[Sync]", "[Worktree]", "[Tree]", "[Diff]"} {
		if !strings.Contains(out, want) {
			t.Errorf("renderHelpPanel missing category header %q\n--- panel ---\n%s", want, out)
		}
	}
}

func TestRenderHelpPanelClampsToHeight(t *testing.T) {
	out := renderHelpPanel(helpData(), 120, 3)
	got := strings.Count(out, "\n") + 1
	if got > 3 {
		t.Errorf("renderHelpPanel(_, 3) emitted %d lines, want ≤ 3", got)
	}
}

func TestRenderHelpPanelHandlesNarrowWidth(t *testing.T) {
	cats := helpData()
	panelRows := 2 * len(cats)
	out := renderHelpPanel(cats, 8, panelRows)
	if out == "" {
		t.Fatal("renderHelpPanel(8, _) returned empty string; want at least one row")
	}
	if got := strings.Count(out, "\n") + 1; got > panelRows {
		t.Errorf("narrow render emitted %d rows, want ≤ %d", got, panelRows)
	}
}

// TestRenderHelpExpandedColumnsWide verifies the wide-terminal layout puts all
// of the graph page's category titles on the same (first) row — the
// side-by-side columns.
func TestRenderHelpExpandedColumnsWide(t *testing.T) {
	out := renderHelpExpanded(helpCategoriesFor(0), 200, 12)
	firstLine := strings.SplitN(out, "\n", 2)[0]
	for _, title := range []string{"Global", "Graph", "Sync"} {
		if !strings.Contains(firstLine, title) {
			t.Errorf("wide help panel: first row missing %q (want all titles on one row)\n--- first row ---\n%s", title, firstLine)
		}
	}
}

// TestRenderHelpExpandedNarrowFallback verifies that when the columns are wider
// than the terminal, renderHelpExpanded falls back to the stacked-rows
// renderHelpPanel form (bracketed headers on separate lines).
func TestRenderHelpExpandedNarrowFallback(t *testing.T) {
	out := renderHelpExpanded(helpCategoriesFor(0), 20, 8)
	if !strings.Contains(out, "[Global]") {
		t.Errorf("narrow help panel should fall back to stacked rows ([Global] header), got:\n%s", out)
	}
	firstLine := strings.SplitN(out, "\n", 2)[0]
	if strings.Contains(firstLine, "Graph") {
		t.Errorf("narrow fallback should not place Graph on the first row (that's the column layout):\n%s", firstLine)
	}
}

// TestRenderHelpExpandedClampsToHeight verifies the column layout is clamped to
// the reserved row budget (small terminals show fewer rows, not overflow).
func TestRenderHelpExpandedClampsToHeight(t *testing.T) {
	out := renderHelpExpanded(helpCategoriesFor(0), 200, 4)
	if got := strings.Count(out, "\n") + 1; got > 4 {
		t.Errorf("renderHelpExpanded(_, 4) emitted %d rows, want ≤ 4", got)
	}
}

// TestHelpCategoriesForPageScoping locks in the page-aware panel: each page
// shows Global plus only its own categories, and graph-only keys (fetch) never
// leak onto the worktree / local pages.
func TestHelpCategoriesForPageScoping(t *testing.T) {
	titles := func(cats []helpCategory) string {
		var b []string
		for _, c := range cats {
			b = append(b, c.title)
		}
		return strings.Join(b, ",")
	}
	hasKey := func(cats []helpCategory, key string) bool {
		for _, c := range cats {
			for _, e := range c.entries {
				if e.keys == key {
					return true
				}
			}
		}
		return false
	}

	cases := []struct {
		page       int
		wantTitles string
	}{
		{0, "Global,Graph,Sync"},
		{1, "Global,Worktree"},
		{2, "Global,Tree,Diff"},
		{3, "Global,Pull Requests"},
	}
	for _, tc := range cases {
		got := helpCategoriesFor(tc.page)
		if titles(got) != tc.wantTitles {
			t.Errorf("page %d titles = %q, want %q", tc.page, titles(got), tc.wantTitles)
		}
		// Global cycle key is present on every page.
		if !hasKey(got, "tab/⇧tab") {
			t.Errorf("page %d missing the global tab/⇧tab key", tc.page)
		}
	}
	// The graph-only fetch key must not appear on worktree / local pages.
	if hasKey(helpCategoriesFor(1), "F") {
		t.Error("worktree page help should not list the graph-only F (fetch) key")
	}
	if hasKey(helpCategoriesFor(2), "F") {
		t.Error("local changes page help should not list the graph-only F (fetch) key")
	}
}
