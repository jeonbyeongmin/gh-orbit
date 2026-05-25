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
		"j/k", "enter", "y", "d", "F", "p", "r", "^C ^C", ",", "space", "b",
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
	panelRows := 2 * len(helpData())
	out := renderHelpPanel(120, panelRows)
	got := strings.Count(out, "\n") + 1
	if got > panelRows {
		t.Errorf("renderHelpPanel emitted %d lines, want ≤ %d", got, panelRows)
	}
	for _, want := range []string{"[Global]", "[Graph]", "[Local Changes]"} {
		if !strings.Contains(out, want) {
			t.Errorf("renderHelpPanel missing category header %q\n--- panel ---\n%s", want, out)
		}
	}
}

func TestRenderHelpPanelClampsToHeight(t *testing.T) {
	out := renderHelpPanel(120, 3)
	got := strings.Count(out, "\n") + 1
	if got > 3 {
		t.Errorf("renderHelpPanel(_, 3) emitted %d lines, want ≤ 3", got)
	}
}

func TestRenderHelpPanelHandlesNarrowWidth(t *testing.T) {
	panelRows := 2 * len(helpData())
	out := renderHelpPanel(8, panelRows)
	if out == "" {
		t.Fatal("renderHelpPanel(8, _) returned empty string; want at least one row")
	}
	if got := strings.Count(out, "\n") + 1; got > panelRows {
		t.Errorf("narrow render emitted %d rows, want ≤ %d", got, panelRows)
	}
}

// TestRenderHelpModalColumnsWide verifies the wide-terminal layout puts all
// three category titles on the same (first) row — the side-by-side columns.
func TestRenderHelpModalColumnsWide(t *testing.T) {
	out := renderHelpModalInner(200)
	firstLine := strings.SplitN(out, "\n", 2)[0]
	for _, title := range []string{"Global", "Graph", "Local Changes"} {
		if !strings.Contains(firstLine, title) {
			t.Errorf("wide help modal: first row missing %q (want all titles on one row)\n--- first row ---\n%s", title, firstLine)
		}
	}
}

// TestRenderHelpModalNarrowFallback verifies that when the columns can't fit
// the content budget, renderHelpModalInner falls back to the stacked-rows
// renderHelpPanel form (bracketed headers on separate lines).
func TestRenderHelpModalNarrowFallback(t *testing.T) {
	out := renderHelpModalInner(20)
	if !strings.Contains(out, "[Global]") {
		t.Errorf("narrow help modal should fall back to stacked rows ([Global] header), got:\n%s", out)
	}
	firstLine := strings.SplitN(out, "\n", 2)[0]
	if strings.Contains(firstLine, "Graph") {
		t.Errorf("narrow fallback should not place Graph on the first row (that's the column layout):\n%s", firstLine)
	}
}
