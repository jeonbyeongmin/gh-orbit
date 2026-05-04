package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestBuildChipsEmpty(t *testing.T) {
	s, w := buildChips(nil, false)
	if s != "" || w != 0 {
		t.Errorf("nil: got (%q, %d), want (\"\", 0)", s, w)
	}
	s, w = buildChips([]string{}, false)
	if s != "" || w != 0 {
		t.Errorf("empty: got (%q, %d), want (\"\", 0)", s, w)
	}
}

func TestBuildChipsLocalOnly(t *testing.T) {
	s, w := buildChips([]string{"main"}, false)
	if s == "" || w == 0 {
		t.Fatalf("got empty result for [main]")
	}
	if !strings.Contains(ansi.Strip(s), "main") {
		t.Errorf("rendered = %q (stripped %q), want to contain 'main'", s, ansi.Strip(s))
	}
}

func TestBuildChipsLocalRemotePairCollapses(t *testing.T) {
	s, _ := buildChips([]string{"HEAD -> main", "origin/main"}, false)
	plain := ansi.Strip(s)
	// HEAD chip + main↑ chip 두 개. "main" 은 한 번만 — pair merging 이 안 됐으면
	// "main" 이 HEAD -> main 분리로 두 번 등장한다.
	if strings.Count(plain, "main") != 1 {
		t.Errorf("plain=%q should contain 'main' exactly once", plain)
	}
	if !strings.Contains(plain, "HEAD") {
		t.Errorf("plain=%q must contain HEAD chip", plain)
	}
	if !strings.Contains(plain, "↑") {
		t.Errorf("plain=%q must contain ↑ marker for PairedRemote", plain)
	}
}

func TestBuildChipsTagStripsPrefix(t *testing.T) {
	s, _ := buildChips([]string{"tag: v0.0.1"}, false)
	plain := ansi.Strip(s)
	if !strings.Contains(plain, "v0.0.1") {
		t.Errorf("plain=%q must contain v0.0.1", plain)
	}
	if strings.Contains(plain, "tag:") {
		t.Errorf("plain=%q must not contain 'tag:' prefix", plain)
	}
}

func TestBuildChipsDetachedHead(t *testing.T) {
	s, w := buildChips([]string{"HEAD"}, false)
	plain := ansi.Strip(s)
	if !strings.Contains(plain, "HEAD") {
		t.Errorf("plain=%q must contain HEAD chip", plain)
	}
	if w == 0 {
		t.Errorf("width=0 for detached HEAD chip")
	}
}

func TestBuildChipsTruncatesAfterTwoWithoutHead(t *testing.T) {
	s, _ := buildChips([]string{"a", "b", "c", "d", "e"}, false)
	plain := ansi.Strip(s)
	// 첫 두 개 + +3 == 3개 chip. "d" / "e" 는 안 보여야 함.
	if !strings.Contains(plain, "+3") {
		t.Errorf("plain=%q must contain '+3' overflow chip", plain)
	}
	if !strings.Contains(plain, "a") || !strings.Contains(plain, "b") {
		t.Errorf("plain=%q must contain first two chips a/b", plain)
	}
	if strings.Contains(plain, "d") || strings.Contains(plain, "e") {
		t.Errorf("plain=%q must not contain truncated chips d/e", plain)
	}
}

func TestBuildChipsTruncatesKeepsHeadChip(t *testing.T) {
	s, _ := buildChips([]string{"HEAD -> main", "tag: v1", "tag: v2", "tag: v3"}, false)
	plain := ansi.Strip(s)
	// HEAD chip + main + +2  (main 은 IsHead 이므로 첫 body chip).
	// tag v1 v2 v3 가 합쳐서 +2 가 되려면 본문에서 chip 수 = main + v1 + v2 + v3 = 4
	// → bodyCap=1 → main 만 보이고 +3 (v1, v2, v3) 가 overflow.
	if !strings.Contains(plain, "HEAD") {
		t.Errorf("plain=%q must contain HEAD chip", plain)
	}
	if !strings.Contains(plain, "main") {
		t.Errorf("plain=%q must contain main chip", plain)
	}
	if !strings.Contains(plain, "+3") {
		t.Errorf("plain=%q must contain '+3' overflow", plain)
	}
}

func TestBuildChipsDropsSymbolicRemoteHead(t *testing.T) {
	s, _ := buildChips([]string{"HEAD -> develop", "origin/develop", "origin/HEAD"}, false)
	plain := ansi.Strip(s)
	if strings.Contains(plain, "origin/HEAD") {
		t.Errorf("origin/HEAD must be dropped; plain=%q", plain)
	}
}

func TestBuildChipsTruncatesLongBranchName(t *testing.T) {
	long := strings.Repeat("a", maxChipTextWidth+10)
	s, _ := buildChips([]string{long}, false)
	plain := ansi.Strip(s)
	if !strings.Contains(plain, "…") {
		t.Errorf("long branch name should be truncated with ellipsis; got %q", plain)
	}
	if strings.Contains(plain, long) {
		t.Errorf("untruncated long name should not appear; got %q", plain)
	}
}

func TestBuildChipsTruncatedNameKeepsPairMarker(t *testing.T) {
	long := strings.Repeat("a", maxChipTextWidth+5)
	// pair: HEAD -> <long>, origin/<long>
	s, _ := buildChips([]string{"HEAD -> " + long, "origin/" + long}, false)
	plain := ansi.Strip(s)
	if !strings.Contains(plain, "…↑") {
		t.Errorf("paired chip with truncated name should end with '…↑'; got %q", plain)
	}
}

func TestBuildChipsSelectedOverridesBackground(t *testing.T) {
	unselected, _ := buildChips([]string{"main"}, false)
	selected, _ := buildChips([]string{"main"}, true)
	if unselected == selected {
		t.Errorf("selected output should differ from unselected; both = %q", unselected)
	}
	// selected 출력은 colorSelected (205) 배경 ANSI 코드를 포함해야 한다.
	if !strings.Contains(selected, "48;5;205") {
		t.Errorf("selected output should set background 205, got %q", selected)
	}
}
