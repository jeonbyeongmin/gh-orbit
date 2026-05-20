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
		"tab", "h/l", "j/k", "ctrl+↑/↓", "enter",
		"y", "d", "F", "p", "r", "q", ",", "space", "b",
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

// TestPaneHintsCoverEveryPane keeps renderHelpStatus from silently falling
// back to "" when a new pane constant is added. paneCount is the sentinel,
// so we walk [0, paneCount).
func TestPaneHintsCoverEveryPane(t *testing.T) {
	hints := paneHints()
	for p := pane(0); p < paneCount; p++ {
		if hints[p] == "" {
			t.Errorf("paneHints missing entry for pane %v", p)
		}
	}
}

// TestHelpExpandedHeightMatchesData guards against helpExpandedHeight
// drifting away from helpData() — renderHelpPanel emits 2 rows per
// category (header + entries), so the constant must equal 2 * len.
func TestHelpExpandedHeightMatchesData(t *testing.T) {
	if got := 2 * len(helpData()); got != helpExpandedHeight {
		t.Errorf("helpExpandedHeight = %d, want %d (2 rows × %d categories)",
			helpExpandedHeight, got, len(helpData()))
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
