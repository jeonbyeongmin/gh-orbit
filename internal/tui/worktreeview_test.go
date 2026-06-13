package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

var cardNow = time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)

func cardLinesPlain(d worktreeCardData, width int) []string {
	out := buildWorktreeCard(d, cardNow, width)
	for i := range out {
		out[i] = ansi.Strip(out[i])
	}
	return out
}

func TestBuildWorktreeCardThreeLinesWidthExact(t *testing.T) {
	d := worktreeCardData{branch: "feat/auth", path: "/repo/feat-auth", subject: "add login form", when: cardNow.Add(-time.Hour)}
	for _, w := range []int{30, 50, 80} {
		lines := cardLinesPlain(d, w)
		if len(lines) != worktreeCardLines {
			t.Fatalf("width %d: got %d lines, want %d", w, len(lines), worktreeCardLines)
		}
		for i, ln := range lines {
			if got := runewidth.StringWidth(ln); got != w {
				t.Errorf("width %d line %d: rendered width %d != %d: %q", w, i, got, w, ln)
			}
		}
	}
}

func TestBuildWorktreeCardBranchLeadsStatusTrails(t *testing.T) {
	d := worktreeCardData{branch: "feat/auth", badge: "#42✓", dirty: "●", path: "/repo/x", subject: "s", when: cardNow.Add(-2 * time.Minute)}
	l := cardLinesPlain(d, 60)[0]
	bi, gi, ti := strings.Index(l, "feat/auth"), strings.Index(l, "#42✓"), strings.Index(l, "2m")
	if bi < 0 || gi < 0 || ti < 0 {
		t.Fatalf("line1 missing tokens: %q", l)
	}
	if bi >= gi || gi >= ti {
		t.Errorf("want branch < badge < time on line1: %q", l)
	}
	if !strings.HasSuffix(strings.TrimRight(l, " "), "2m") {
		t.Errorf("status cluster should sit at the right edge: %q", l)
	}
}

func TestBuildWorktreeCardCurrentAndCursorMarkers(t *testing.T) {
	d := worktreeCardData{branch: "develop", path: "/r", subject: "s", isCurrent: true, isCursor: true}
	lines := cardLinesPlain(d, 40)
	if !strings.HasPrefix(lines[0], "▌▶") {
		t.Errorf("current+cursor line1 should start ▌▶: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "▌") || !strings.HasPrefix(lines[2], "▌") {
		t.Errorf("cursor bar ▌ should run down all card lines: %q / %q", lines[1], lines[2])
	}
}

func TestBuildWorktreeCardPathAndSubject(t *testing.T) {
	d := worktreeCardData{branch: "br", path: "~/project/gh-orbit", subject: "Merge pull request #101", when: cardNow}
	lines := cardLinesPlain(d, 60)
	if !strings.Contains(lines[1], "~/project/gh-orbit") {
		t.Errorf("line2 should carry the path: %q", lines[1])
	}
	if !strings.Contains(lines[2], "Merge pull request #101") {
		t.Errorf("line3 should carry the subject: %q", lines[2])
	}
}

func TestBuildWorktreeCardDetached(t *testing.T) {
	d := worktreeCardData{detached: true, path: "/r", subject: "s"}
	if !strings.Contains(cardLinesPlain(d, 40)[0], "(detached)") {
		t.Errorf("detached worktree should show (detached)")
	}
}

func TestBuildWorktreeCardEmptySubjectDash(t *testing.T) {
	d := worktreeCardData{branch: "br", path: "/r", subject: ""}
	if !strings.Contains(cardLinesPlain(d, 40)[2], "—") {
		t.Errorf("empty subject should render an em-dash placeholder")
	}
}

func TestBuildWorktreeCardLongBranchKeepsStatus(t *testing.T) {
	d := worktreeCardData{branch: "feat/a-really-long-branch-name-that-will-not-fit", badge: "#7✗", path: "/r", subject: "s", when: cardNow.Add(-time.Hour)}
	l := cardLinesPlain(d, 30)[0]
	if runewidth.StringWidth(l) != 30 {
		t.Fatalf("width != 30: %q", l)
	}
	if !strings.Contains(l, "#7✗") {
		t.Errorf("badge/status must survive a long branch: %q", l)
	}
	if !strings.Contains(l, "…") {
		t.Errorf("over-long branch should truncate with …: %q", l)
	}
}

func TestBuildWorktreeCardNeverOverflowsWidth(t *testing.T) {
	// Regression: a status cluster wider than the card width must not push line 1
	// past width — padToWidth only pads, so an overflow would wrap and corrupt
	// the card frame. Every line must be exactly width at every narrow width.
	d := worktreeCardData{branch: "develop", badge: "#1234✓", sync: "↑12↓34", dirty: "●99", path: "/some/long/worktree/path", subject: "a subject", when: cardNow.Add(-90 * 24 * time.Hour)}
	for _, w := range []int{8, 12, 16, 20, 24} {
		for i, ln := range cardLinesPlain(d, w) {
			if got := runewidth.StringWidth(ln); got != w {
				t.Errorf("width %d line %d: rendered %d cells, want exactly %d: %q", w, i, got, w, ln)
			}
		}
	}
}

func TestBuildWorktreeCardStatusClusterOrder(t *testing.T) {
	// badge · sync · dirty · time, left to right, right-anchored.
	d := worktreeCardData{branch: "feat/x", badge: "#9✓", sync: "↑2↓1", dirty: "●3", path: "/r", subject: "s", when: cardNow.Add(-time.Hour)}
	l := cardLinesPlain(d, 70)[0]
	bi, si, di, ti := strings.Index(l, "#9✓"), strings.Index(l, "↑2↓1"), strings.Index(l, "●3"), strings.Index(l, "1h")
	if bi < 0 || si < 0 || di < 0 || ti < 0 {
		t.Fatalf("status cluster missing a token: %q", l)
	}
	if bi >= si || si >= di || di >= ti {
		t.Errorf("want badge<sync<dirty<time: %q", l)
	}
}

func TestRenderWorktreeCardSourcesCountAndSync(t *testing.T) {
	m := withModel(t, []git.Worktree{{Path: "/wt/a", Branch: "feat/a"}}, "/wt/a")
	m.refs.SetWorktreeDirty("/wt/a", 4, false)
	m.refs.SetWorktreeSync("/wt/a", 2, 1, true)
	l := ansi.Strip(m.renderWorktreeCard(git.Worktree{Path: "/wt/a", Branch: "feat/a"}, 0, 70, cardNow)[0])
	if !strings.Contains(l, "●4") {
		t.Errorf("dirty file count ●4 missing: %q", l)
	}
	if !strings.Contains(l, "↑2↓1") {
		t.Errorf("ahead/behind ↑2↓1 missing: %q", l)
	}
}

func TestRenderWorktreeCardNoUpstreamOmitsSync(t *testing.T) {
	m := withModel(t, []git.Worktree{{Path: "/wt/a", Branch: "feat/a"}}, "/wt/a")
	m.refs.SetWorktreeSync("/wt/a", 0, 0, false) // no upstream → no ↑↓
	l := ansi.Strip(m.renderWorktreeCard(git.Worktree{Path: "/wt/a", Branch: "feat/a"}, 0, 70, cardNow)[0])
	if strings.ContainsAny(l, "↑↓") {
		t.Errorf("no-upstream card must omit the ↑↓ column: %q", l)
	}
}

func TestTruncLeftKeepTail(t *testing.T) {
	got := truncLeftKeepTail("/very/long/path/to/worktree-dir", 16)
	if runewidth.StringWidth(got) > 16 {
		t.Errorf("width > 16: %q", got)
	}
	if !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "worktree-dir") {
		t.Errorf("should keep the tail behind a leading …: %q", got)
	}
	if truncLeftKeepTail("short", 12) != "short" {
		t.Errorf("a fitting string should pass through unchanged")
	}
}

func TestWorktreePathDisplayCollapsesHome(t *testing.T) {
	t.Setenv("HOME", "/Users/x")
	if got := worktreePathDisplay("/Users/x/project/gh-orbit"); got != "~/project/gh-orbit" {
		t.Errorf("home prefix should collapse to ~: %q", got)
	}
	if got := worktreePathDisplay("/opt/elsewhere"); got != "/opt/elsewhere" {
		t.Errorf("non-home path should pass through: %q", got)
	}
}

func TestRenderWorktreesViewWindowsToCursor(t *testing.T) {
	// More cards than fit a short view: the window must follow the cursor and
	// flag the clipped overflow, never exceed the height, and keep the cursor
	// card on screen.
	wts := make([]git.Worktree, 8)
	for i := range wts {
		wts[i] = git.Worktree{Path: "/wt/" + string(rune('a'+i)), Branch: "feat/" + string(rune('0'+i))}
	}
	m := withModel(t, wts, "/wt/a")
	m.worktreesModal.cursor = 7 // last card

	out := m.renderWorktreesView(60, 16)
	lines := strings.Split(out, "\n")
	if len(lines) != 16 {
		t.Fatalf("view must stay exactly 16 lines even when clipped, got %d", len(lines))
	}
	body := ansi.Strip(out)
	if !strings.Contains(body, "feat/7") {
		t.Errorf("cursor card (feat/7) must be visible after windowing: %q", body)
	}
	if !strings.Contains(body, "more") {
		t.Errorf("clipped list should show an ↑/↓ more marker: %q", body)
	}
}

func TestRenderWorktreesViewFillsHeight(t *testing.T) {
	m := withModel(t, []git.Worktree{
		{Path: "/main", Branch: "develop", IsMain: true},
		{Path: "/a", Branch: "feat/a"},
		{Path: "/b", Branch: "feat/b"},
	}, "/main")
	m.workdir = "/main"
	out := m.renderWorktreesView(60, 20)
	lines := strings.Split(out, "\n")
	if len(lines) != 20 {
		t.Fatalf("view should be exactly 20 lines, got %d", len(lines))
	}
	for i, ln := range lines {
		if w := runewidth.StringWidth(ansi.Strip(ln)); w > 60 {
			t.Errorf("line %d exceeds width 60: %d", i, w)
		}
	}
	if !strings.Contains(ansi.Strip(lines[0]), "[Worktrees · 3]") {
		t.Errorf("header should show the worktree count: %q", lines[0])
	}
	body := ansi.Strip(out)
	if !strings.Contains(body, "develop") || !strings.Contains(body, "feat/a") {
		t.Errorf("cards should list the worktree branches")
	}
}
