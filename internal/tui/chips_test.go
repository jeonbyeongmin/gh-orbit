package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

func TestBuildChipsEmpty(t *testing.T) {
	s, w := buildChips(nil, nil, false, false)
	if s != "" || w != 0 {
		t.Errorf("nil: got (%q, %d), want (\"\", 0)", s, w)
	}
	s, w = buildChips([]string{}, nil, false, false)
	if s != "" || w != 0 {
		t.Errorf("empty: got (%q, %d), want (\"\", 0)", s, w)
	}
}

func TestBuildChipsLocalOnly(t *testing.T) {
	s, w := buildChips([]string{"main"}, nil, false, false)
	if s == "" || w == 0 {
		t.Fatalf("got empty result for [main]")
	}
	if !strings.Contains(ansi.Strip(s), "main") {
		t.Errorf("rendered = %q (stripped %q), want to contain 'main'", s, ansi.Strip(s))
	}
}

func TestBuildChipsLocalRemotePairCollapses(t *testing.T) {
	s, _ := buildChips([]string{"HEAD -> main", "origin/main"}, nil, false, false)
	plain := ansi.Strip(s)
	// paired-main chip 만 — HEAD 는 더 이상 chip 으로 그리지 않는다.
	// "main" 은 한 번만 — pair merging 이 안 됐으면 두 번 등장한다.
	if strings.Count(plain, "main") != 1 {
		t.Errorf("plain=%q should contain 'main' exactly once", plain)
	}
	if strings.Contains(plain, "HEAD") {
		t.Errorf("plain=%q must NOT contain HEAD chip (HEAD-as-dim-boundary now)", plain)
	}
	// paired chip 의 시각 신호는 이름 앞의 `☁ ` prefix — 이전에 default 로
	// 나간 적 있는 `↑` 마커가 다시 새지 않게 회귀 가드. ansi.Strip 이 일반 룬은
	// 보존하므로 stripped 출력에 정확히 한 번 등장해야 한다.
	if strings.Contains(plain, "↑") {
		t.Errorf("plain=%q must not contain stale '↑' marker", plain)
	}
	if strings.Count(plain, "☁") != 1 {
		t.Errorf("paired chip should carry exactly one '☁' sync prefix; got %q", plain)
	}
}

func TestBuildChipsPairedChipPreservesWidth(t *testing.T) {
	// totalW 가 실제 visible runewidth 와 일치해야 한다 — paired chip 은
	// chipDisplay 가 이름 앞에 `☁ ` (2 cells) 를 붙이므로 plain 대비 +2 cell
	// 늘어나고, 그 차이가 totalW 계산에 정확히 반영되어야 한다.
	pairedRendered, pairedW := buildChips([]string{"main", "origin/main"}, nil, false, false)
	plainRendered, plainW := buildChips([]string{"feature/x"}, nil, false, false)

	if pairedW != runewidth.StringWidth(ansi.Strip(pairedRendered)) {
		t.Errorf("paired chip totalW (%d) != visible runewidth of stripped output (%q -> %d)",
			pairedW, ansi.Strip(pairedRendered), runewidth.StringWidth(ansi.Strip(pairedRendered)))
	}
	if plainW != runewidth.StringWidth(ansi.Strip(plainRendered)) {
		t.Errorf("plain chip totalW (%d) != visible runewidth of stripped output (%q -> %d)",
			plainW, ansi.Strip(plainRendered), runewidth.StringWidth(ansi.Strip(plainRendered)))
	}
	// paired "☁ main" = 6 + 2 padding = 8. plain "feature/x" = 9 + 2 = 11.
	if pairedW != 8 {
		t.Errorf("paired 'main' chip width = %d, want 8 (☁ + space + text + padding)", pairedW)
	}
	if plainW != 11 {
		t.Errorf("plain 'feature/x' chip width = %d, want 11 (text+padding)", plainW)
	}
}

func TestBuildChipsTagStripsPrefix(t *testing.T) {
	s, _ := buildChips([]string{"tag: v0.0.1"}, nil, false, false)
	plain := ansi.Strip(s)
	if !strings.Contains(plain, "v0.0.1") {
		t.Errorf("plain=%q must contain v0.0.1", plain)
	}
	if strings.Contains(plain, "tag:") {
		t.Errorf("plain=%q must not contain 'tag:' prefix", plain)
	}
}

func TestBuildChipsDetachedHeadEmitsNothing(t *testing.T) {
	// Detached HEAD has only the bare `HEAD` token and no other refs.
	// HEAD-as-dim-boundary supersedes the chip — buildChips returns nothing.
	s, w := buildChips([]string{"HEAD"}, nil, false, false)
	if s != "" || w != 0 {
		t.Errorf("detached HEAD alone should render no chip; got (%q, %d)", s, w)
	}
}

func TestBuildChipsHeadArrowSuppressesHEADToken(t *testing.T) {
	// `HEAD -> main` produces only a `main` chip — the HEAD prefix no longer
	// emits its own chip slot.
	s, _ := buildChips([]string{"HEAD -> main"}, nil, false, false)
	plain := ansi.Strip(s)
	if strings.Contains(plain, "HEAD") {
		t.Errorf("plain=%q must NOT contain HEAD chip", plain)
	}
	if !strings.Contains(plain, "main") {
		t.Errorf("plain=%q must contain main chip", plain)
	}
}

func TestBuildChipsTruncatesAfterTwoWithoutHead(t *testing.T) {
	s, _ := buildChips([]string{"a", "b", "c", "d", "e"}, nil, false, false)
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

func TestBuildChipsTruncatesAfterTwoEvenWithHeadInput(t *testing.T) {
	s, _ := buildChips([]string{"HEAD -> main", "tag: v1", "tag: v2", "tag: v3"}, nil, false, false)
	plain := ansi.Strip(s)
	// HEAD 칩 제거 후 bodyCap=2: main + v1 가 보이고 v2, v3 가 +2 overflow 로 합쳐진다.
	if strings.Contains(plain, "HEAD") {
		t.Errorf("plain=%q must NOT contain HEAD chip", plain)
	}
	if !strings.Contains(plain, "main") {
		t.Errorf("plain=%q must contain main chip", plain)
	}
	if !strings.Contains(plain, "v1") {
		t.Errorf("plain=%q must contain v1 chip", plain)
	}
	if !strings.Contains(plain, "+2") {
		t.Errorf("plain=%q must contain '+2' overflow", plain)
	}
	if strings.Contains(plain, "v3") {
		t.Errorf("plain=%q must not contain truncated v3 chip", plain)
	}
}

func TestBuildChipsDropsSymbolicRemoteHead(t *testing.T) {
	s, _ := buildChips([]string{"HEAD -> develop", "origin/develop", "origin/HEAD"}, nil, false, false)
	plain := ansi.Strip(s)
	if strings.Contains(plain, "origin/HEAD") {
		t.Errorf("origin/HEAD must be dropped; plain=%q", plain)
	}
}

func TestBuildChipsTruncatesLongBranchName(t *testing.T) {
	long := strings.Repeat("a", maxChipTextWidth+10)
	s, _ := buildChips([]string{long}, nil, false, false)
	plain := ansi.Strip(s)
	if !strings.Contains(plain, "…") {
		t.Errorf("long branch name should be truncated with ellipsis; got %q", plain)
	}
	if strings.Contains(plain, long) {
		t.Errorf("untruncated long name should not appear; got %q", plain)
	}
}

func TestBuildChipsTruncatedNamePreservesPairPrefix(t *testing.T) {
	long := strings.Repeat("a", maxChipTextWidth+5)
	// pair: HEAD -> <long>, origin/<long>
	s, _ := buildChips([]string{"HEAD -> " + long, "origin/" + long}, nil, false, false)
	plain := ansi.Strip(s)
	// 이름 truncation 은 maxChipTextWidth 한도에서 "…" 로 그대로 동작해야 하고,
	// paired prefix `☁ ` 는 truncate 와 무관하게 정확히 한 번 등장해야 한다.
	if !strings.Contains(plain, "…") {
		t.Errorf("paired chip with truncated name should still end with '…'; got %q", plain)
	}
	if strings.Contains(plain, "…↑") {
		t.Errorf("plain=%q must not contain stale '…↑' marker", plain)
	}
	if strings.Count(plain, "☁") != 1 {
		t.Errorf("truncated paired chip should still carry one '☁' sync prefix; got %q", plain)
	}
}

func TestBuildChipsSelectedOverridesBackground(t *testing.T) {
	useTheme(t, "github-dark")
	unselected, _ := buildChips([]string{"main"}, nil, false, false)
	selected, _ := buildChips([]string{"main"}, nil, true, false)
	if unselected == selected {
		t.Errorf("selected output should differ from unselected; both = %q", unselected)
	}
	// selected 출력은 colorSelected (205) 배경 ANSI 코드를 포함해야 한다.
	if !strings.Contains(selected, "48;5;205") {
		t.Errorf("selected output should set background 205, got %q", selected)
	}
}

func TestBuildChipsPRBadgeOnLocalChip(t *testing.T) {
	prs := map[string]prInfo{"feat-x": {Number: 42, Checks: prChecksPassing}}
	s, w := buildChips([]string{"feat-x"}, prs, false, false)
	plain := ansi.Strip(s)
	if !strings.Contains(plain, "#42✓") {
		t.Errorf("plain=%q should contain PR badge '#42✓'", plain)
	}
	// totalW 는 배지를 포함한 visible runewidth 와 일치해야 한다.
	if got := runewidth.StringWidth(plain); got != w {
		t.Errorf("reported width %d != visible width %d (plain=%q)", w, got, plain)
	}
}

func TestBuildChipsPRBadgeMatchesRemoteChip(t *testing.T) {
	// 로컬 ref 없이 원격 chip 만 있는 행 — push 만 해둔 브랜치.
	prs := map[string]prInfo{"feat-x": {Number: 7, Checks: prChecksFailing}}
	s, _ := buildChips([]string{"origin/feat-x"}, prs, false, false)
	plain := ansi.Strip(s)
	if !strings.Contains(plain, "#7✗") {
		t.Errorf("plain=%q should contain PR badge '#7✗' via stripped remote name", plain)
	}
}

func TestBuildChipsPRBadgeSkipsTagAndNonMatching(t *testing.T) {
	prs := map[string]prInfo{"feat-x": {Number: 9, Checks: prChecksPending}}
	s, _ := buildChips([]string{"tag: feat-x", "main"}, prs, false, false)
	plain := ansi.Strip(s)
	if strings.Contains(plain, "#9") {
		t.Errorf("plain=%q must not badge a tag or a non-matching branch", plain)
	}
}

func TestBuildChipsPRBadgeSurvivesNameTruncation(t *testing.T) {
	long := strings.Repeat("b", maxChipTextWidth+5)
	prs := map[string]prInfo{long: {Number: 3, Checks: prChecksNone}}
	s, _ := buildChips([]string{long}, prs, false, false)
	plain := ansi.Strip(s)
	if !strings.Contains(plain, "…") {
		t.Errorf("plain=%q should still truncate the long name", plain)
	}
	if !strings.Contains(plain, "#3") {
		t.Errorf("plain=%q should keep the PR badge after the truncated name", plain)
	}
}

func TestBuildChipsPRBadgeTwoToneSegment(t *testing.T) {
	useTheme(t, "github-dark")
	prs := map[string]prInfo{"feat-x": {Number: 86, Checks: prChecksPending}}
	s, _ := buildChips([]string{"feat-x"}, prs, false, false)
	// 배지 tail 은 칩 본체와 다른 bg(236) + 상태색 fg(pending 214) 를 가진
	// 별도 세그먼트여야 한다.
	if !strings.Contains(s, "48;5;236") {
		t.Errorf("badge tail should use bg 236, got %q", s)
	}
	if !strings.Contains(s, "38;5;214") {
		t.Errorf("pending badge should use fg 214, got %q", s)
	}
	// 칩 본체 bg(local 39) 도 여전히 존재해야 한다 — two-tone 의 양쪽.
	if !strings.Contains(s, "48;5;39") {
		t.Errorf("name segment should keep local chip bg 39, got %q", s)
	}
}

func TestBuildChipsPRBadgeFlattensWhenSelected(t *testing.T) {
	prs := map[string]prInfo{"feat-x": {Number: 86, Checks: prChecksPassing}}
	s, _ := buildChips([]string{"feat-x"}, prs, true, false)
	plain := ansi.Strip(s)
	// selected 행은 단일 색으로 평탄화 — 배지 텍스트는 남고 bg 236 은 사라진다.
	if !strings.Contains(plain, "#86✓") {
		t.Errorf("flattened chip should keep badge text, got %q", plain)
	}
	if strings.Contains(s, "48;5;236") {
		t.Errorf("selected chip must not keep the badge bg, got %q", s)
	}
}
