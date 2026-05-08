package tui

import (
	"strings"
	"testing"
)

// TestHelpDataCoverage guards against silently dropping a binding from the
// expanded panel. The list mirrors the keys that the previous one-line
// helpTextNormal carried — every refactor of the panel must keep them all.
func TestHelpDataCoverage(t *testing.T) {
	required := []string{
		"tab", "h/l", "j/k", "ctrl+↑/↓", "enter", "o", "C",
		"y", "d", "F", "P", "r", "q",
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

func TestPaneHintsContainGlobalSuffix(t *testing.T) {
	const suffix = "? help · q quit"
	for p, hint := range paneHints() {
		if !strings.HasSuffix(hint, suffix) {
			t.Errorf("paneHints[%v] = %q; want suffix %q", p, hint, suffix)
		}
	}
}

func TestRenderHelpPanelLineCount(t *testing.T) {
	out := renderHelpPanel(120, helpExpandedHeight)
	got := strings.Count(out, "\n") + 1
	if got > helpExpandedHeight {
		t.Errorf("renderHelpPanel emitted %d lines, want ≤ %d", got, helpExpandedHeight)
	}
	for _, want := range []string{"[Global]", "[Refs]", "[Graph]", "[Tab]"} {
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
	out := renderHelpPanel(8, helpExpandedHeight)
	if out == "" {
		t.Fatal("renderHelpPanel(8, _) returned empty string; want at least one row")
	}
	if got := strings.Count(out, "\n") + 1; got > helpExpandedHeight {
		t.Errorf("narrow render emitted %d rows, want ≤ %d", got, helpExpandedHeight)
	}
}
