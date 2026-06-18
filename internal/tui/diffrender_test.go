package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

func TestClassifyDiffLine(t *testing.T) {
	cases := []struct {
		line string
		want diffLineKind
	}{
		{"diff --git a/x b/x", dlMeta},
		{"index 111..222 100644", dlMeta},
		{"--- a/x", dlMeta},
		{"+++ b/x", dlMeta},
		{"@@ -1,2 +1,3 @@ func main()", dlHunk},
		{" context line", dlContext},
		{"-removed", dlDel},
		{"+added", dlAdd},
		{`\ No newline at end of file`, dlMeta},
		{"", dlMeta},
	}
	for _, c := range cases {
		if got := classifyDiffLine(c.line); got != c.want {
			t.Errorf("classifyDiffLine(%q) = %d, want %d", c.line, got, c.want)
		}
	}
}

func TestExpandTabs(t *testing.T) {
	cases := []struct{ in, want string }{
		{"no tabs", "no tabs"},
		{"\tx", "    x"},
		{"a\tb", "a   b"},   // a at col0, tab fills to col4
		{"ab\tc", "ab  c"},  // ab at col0-1, tab fills to col4
		{"abcd\te", "abcd    e"},
		{"変\tx", "変  x"}, // wide rune (width 2) → tab fills to col4 with 2 spaces, not 3
	}
	for _, c := range cases {
		if got := expandTabs(c.in, 4); got != c.want {
			t.Errorf("expandTabs(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestWordTokens(t *testing.T) {
	toks := wordTokens([]rune("fmt.Println(x)"))
	var got []string
	for _, tk := range toks {
		got = append(got, tk.text)
	}
	want := []string{"fmt", ".", "Println", "(", "x", ")"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("wordTokens = %v, want %v", got, want)
	}
	// offsets must be contiguous and rune-accurate.
	for i, tk := range toks {
		if i > 0 && tk.start != toks[i-1].start+toks[i-1].n {
			t.Errorf("token %d start %d not contiguous", i, tk.start)
		}
	}
}

func TestWordLevelChangedSmallEdit(t *testing.T) {
	// A small, targeted edit: only the literal changes. Most of the line is
	// shared, so it clears wordMatchFloor and the emphasis lands on just "1"/"3".
	del := []rune("    count := 1")
	add := []rune("    count := 3")
	dr, ar := wordLevelChanged(del, add)
	if len(dr) == 0 || len(ar) == 0 {
		t.Fatalf("expected changed ranges, got del=%v add=%v", dr, ar)
	}
	if got := rangesText(del, dr); got != "1" {
		t.Errorf("del changed text = %q, want %q", got, "1")
	}
	if got := rangesText(add, ar); got != "3" {
		t.Errorf("add changed text = %q, want %q", got, "3")
	}
}

func TestWordLevelChangedDissimilarSkips(t *testing.T) {
	cases := []struct{ a, b string }{
		// Nothing in common.
		{"alpha beta gamma", "123 456 789"},
		// More rewritten than edited — below wordMatchFloor, so no per-word
		// emphasis; the full-width bar carries the change instead.
		{"    println(x)", "    fmt.Printf(\"%d\", value)"},
	}
	for _, c := range cases {
		dr, ar := wordLevelChanged([]rune(c.a), []rune(c.b))
		if dr != nil || ar != nil {
			t.Errorf("wordLevelChanged(%q,%q) = del=%v add=%v, want nil,nil", c.a, c.b, dr, ar)
		}
	}
}

func TestRenderDiffContentPreservesLines(t *testing.T) {
	patch := strings.Join([]string{
		"diff --git a/main.go b/main.go",
		"index 111..222 100644",
		"--- a/main.go",
		"+++ b/main.go",
		"@@ -1,3 +1,3 @@",
		" package main",
		"-func old() {}",
		"+func new() {}",
		" // tail",
		"",
	}, "\n")

	// width 0 → no full-width padding, so the stripped render equals the source.
	out := renderDiffContent(patch, 0)
	if got, want := len(strings.Split(out, "\n")), len(strings.Split(patch, "\n")); got != want {
		t.Fatalf("line count changed: got %d want %d (one rendered line per input line is required)", got, want)
	}
	// ANSI-stripping the rendered text must reproduce the plain patch.
	if got := ansi.Strip(out); got != patch {
		t.Errorf("stripped render != source patch.\n got: %q\nwant: %q", got, patch)
	}
	// The add/del lines must actually carry styling (escape sequences present).
	for _, line := range strings.Split(out, "\n") {
		plain := ansi.Strip(line)
		if strings.HasPrefix(plain, "+func") || strings.HasPrefix(plain, "-func") {
			if line == plain {
				t.Errorf("expected ANSI styling on %q", plain)
			}
		}
	}
}

func TestRenderDiffContentEmphForeground(t *testing.T) {
	// A one-word edit inside a (gray) comment clears wordMatchFloor, so the
	// changed word gets word-level emphasis. Its foreground must be the bright
	// override (diffEmphFg), not the dim comment color, or it'd be unreadable on
	// the emph background. Comparing against a reference segment rendered through
	// the same path keeps this independent of the test's color profile.
	patch := strings.Join([]string{
		"@@ -1,1 +1,1 @@",
		"-// the old label",
		"+// the new label",
	}, "\n")
	out := renderDiffContent(patch, 40)
	addWord := styledSeg("new", diffEmphFg, diffAddEmphBg)
	delWord := styledSeg("old", diffEmphFg, diffDelEmphBg)
	if !strings.Contains(out, addWord) {
		t.Errorf("changed add word not rendered with the bright emph foreground")
	}
	if !strings.Contains(out, delWord) {
		t.Errorf("changed del word not rendered with the bright emph foreground")
	}
}

func TestRenderDiffContentEmpty(t *testing.T) {
	if got := renderDiffContent("", 80); got != "" {
		t.Errorf("renderDiffContent(\"\") = %q, want empty", got)
	}
}

func TestRenderDiffContentFullWidthBars(t *testing.T) {
	const width = 40
	patch := strings.Join([]string{
		"@@ -1,2 +1,2 @@",
		" ctx := keep()",
		"-x := 1",
		"+x := 2",
	}, "\n")
	lines := strings.Split(renderDiffContent(patch, width), "\n")
	// Changed (+/-) lines fill the full width; context lines do not get a bar.
	for _, ln := range lines {
		plain := ansi.Strip(ln)
		switch {
		case strings.HasPrefix(plain, "+") || strings.HasPrefix(plain, "-"):
			if w := runewidth.StringWidth(plain); w != width {
				t.Errorf("changed line %q width = %d, want %d (full-width bar)", plain, w, width)
			}
		case strings.HasPrefix(plain, " ctx"):
			if w := runewidth.StringWidth(plain); w >= width {
				t.Errorf("context line %q should not be padded to full width (got %d)", plain, w)
			}
		}
	}
}

// rangesText concatenates the runes covered by the given ranges.
func rangesText(runes []rune, ranges [][2]int) string {
	var b strings.Builder
	for _, r := range ranges {
		b.WriteString(string(runes[r[0]:r[1]]))
	}
	return b.String()
}
