package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// withModel returns a Model with non-zero size + worktrees seeded so the
// dashboard renders. Width/height matter only for paneSizes; the dashboard
// render takes width as an argument.
func withModel(t *testing.T, worktrees []git.Worktree, currentPath string) Model {
	t.Helper()
	m := initSized(t)
	m.refs.SetWorktrees(worktrees, currentPath)
	m.refs, _ = m.refs.Update(refsLoadedMsg{refs: nil})
	return m
}

func TestDashboardLinesZeroWhenNoWorktrees(t *testing.T) {
	m := initSized(t)
	if got := dashboardLines(m); got != 0 {
		t.Errorf("dashboardLines with no worktrees = %d, want 0", got)
	}
}

func TestDashboardLinesHeaderPlusWorktreesPlusSeparator(t *testing.T) {
	m := withModel(t,
		[]git.Worktree{{Path: "/a"}, {Path: "/b"}, {Path: "/c"}, {Path: "/d"}},
		"/a",
	)
	if got := dashboardLines(m); got != 6 {
		t.Errorf("dashboardLines for 4 worktrees = %d, want 6 (header + 4 rows + separator)", got)
	}
}

func TestRenderTopDashboardEmptyWhenNoWorktrees(t *testing.T) {
	m := initSized(t)
	if got := renderTopDashboard(m, 80); got != "" {
		t.Errorf("dashboard with zero worktrees should render empty, got %q", got)
	}
}

func TestRenderTopDashboardIncludesWorktreeNames(t *testing.T) {
	m := withModel(t,
		[]git.Worktree{
			{Path: "/tmp/wt-main", Branch: "main"},
			{Path: "/tmp/wt-feat", Branch: "feat/foo"},
		},
		"/tmp/wt-main",
	)
	plain := ansi.Strip(renderTopDashboard(m, 80))
	for _, want := range []string{"Worktrees (2)", "wt-main", "wt-feat", "main", "feat/foo"} {
		if !strings.Contains(plain, want) {
			t.Errorf("dashboard missing %q: %q", want, plain)
		}
	}
}

func TestRenderTopDashboardCurrentMarker(t *testing.T) {
	m := withModel(t,
		[]git.Worktree{
			{Path: "/tmp/wt-a", Branch: "main"},
			{Path: "/tmp/wt-b", Branch: "feat/foo"},
		},
		"/tmp/wt-b",
	)
	plain := ansi.Strip(renderTopDashboard(m, 80))
	// The current marker is `▶`; it should land on the wt-b row, not the
	// wt-a row. A row-aware check beats searching the whole string for `▶`.
	rows := strings.Split(plain, "\n")
	var aRow, bRow string
	for _, r := range rows {
		if strings.Contains(r, "wt-a") {
			aRow = r
		}
		if strings.Contains(r, "wt-b") {
			bRow = r
		}
	}
	if aRow == "" || bRow == "" {
		t.Fatalf("dashboard missing wt-a / wt-b rows; got:\n%s", plain)
	}
	if strings.Contains(aRow, "▶") {
		t.Errorf("wt-a should not carry ▶ marker: %q", aRow)
	}
	if !strings.Contains(bRow, "▶") {
		t.Errorf("wt-b (current) should carry ▶ marker: %q", bRow)
	}
}

func TestRenderTopDashboardDirtyMarker(t *testing.T) {
	m := withModel(t,
		[]git.Worktree{{Path: "/tmp/wt-a", Branch: "main"}},
		"/tmp/wt-a",
	)
	m.refs.SetWorktreeDirty("/tmp/wt-a", true, false)
	plain := ansi.Strip(renderTopDashboard(m, 80))
	if !strings.Contains(plain, "●") {
		t.Errorf("dashboard should carry dirty marker `●`: %q", plain)
	}
}

func TestRenderTopDashboardHeaderCarriesLocalChangesMeta(t *testing.T) {
	m := withModel(t,
		[]git.Worktree{{Path: "/tmp/wt-a", Branch: "main"}},
		"/tmp/wt-a",
	)
	m.refs.SetLocalChangesSummary(
		git.LocalChangesSummary{FilesChanged: 3, Insertions: 12, Deletions: 4},
		time.Now().Add(-2*time.Minute),
	)
	plain := ansi.Strip(renderTopDashboard(m, 100))
	if !strings.Contains(plain, "◆ Local Changes") {
		t.Errorf("dashboard header should carry Local Changes meta: %q", plain)
	}
	if !strings.Contains(plain, "3 files") || !strings.Contains(plain, "+12 -4") {
		t.Errorf("dashboard header should include Local Changes counts: %q", plain)
	}
}

func TestRenderTopDashboardHeaderDropsMetaOnNarrowWidth(t *testing.T) {
	m := withModel(t,
		[]git.Worktree{{Path: "/tmp/wt-a", Branch: "main"}},
		"/tmp/wt-a",
	)
	m.refs.SetLocalChangesSummary(
		git.LocalChangesSummary{FilesChanged: 3, Insertions: 12, Deletions: 4},
		time.Now(),
	)
	plain := ansi.Strip(renderTopDashboard(m, 20))
	// Header line is the first row. It must always render "Worktrees" even
	// if Local Changes meta gets dropped for lack of room.
	header := strings.SplitN(plain, "\n", 2)[0]
	if !strings.Contains(header, "Worktrees") {
		t.Errorf("narrow header should keep Worktrees label: %q", header)
	}
}

func TestRenderTopDashboardFooterCarriesFreshness(t *testing.T) {
	m := withModel(t,
		[]git.Worktree{{Path: "/tmp/wt-a", Branch: "main"}},
		"/tmp/wt-a",
	)
	m.refs.SetLastFetchAt(time.Now().Add(-5 * time.Minute))
	plain := ansi.Strip(renderTopDashboard(m, 80))
	if !strings.Contains(plain, "fetched 5m ago") {
		t.Errorf("dashboard footer should carry 'fetched 5m ago': %q", plain)
	}
}

func TestRenderTopDashboardFooterPlainWhenNeverFetched(t *testing.T) {
	m := withModel(t,
		[]git.Worktree{{Path: "/tmp/wt-a", Branch: "main"}},
		"/tmp/wt-a",
	)
	plain := ansi.Strip(renderTopDashboard(m, 40))
	if strings.Contains(plain, "fetched") {
		t.Errorf("dashboard footer should be plain rule before any fetch attempt: %q", plain)
	}
	// Last line should still be the separator rule.
	lines := strings.Split(plain, "\n")
	last := lines[len(lines)-1]
	if !strings.Contains(last, "─") {
		t.Errorf("dashboard last line should be the separator rule, got %q", last)
	}
}

// TestRenderTopDashboardCursorRowWhenFocused — when paneDashboard owns
// the cursor, the cursor row carries the colorCursorRowBg ANSI escape
// (background 237). Locks in Decision 4's cursor row tint.
func TestRenderTopDashboardCursorRowWhenFocused(t *testing.T) {
	m := withModel(t,
		[]git.Worktree{
			{Path: "/tmp/wt-a", Branch: "main"},
			{Path: "/tmp/wt-b", Branch: "feat/foo"},
		},
		"/tmp/wt-a",
	)
	m.focused = paneDashboard
	m.dashboardFocus.cursor = 1
	raw := renderTopDashboard(m, 80)
	if !strings.Contains(raw, "48;5;237") {
		t.Errorf("focused dashboard should contain cursorRowBgStyle escape (48;5;237), got %q", raw)
	}
}

// TestRenderTopDashboardNoCursorWhenUnfocused — paneGraph (default)
// → no cursorRowBgStyle escape anywhere; the band stays read-only.
func TestRenderTopDashboardNoCursorWhenUnfocused(t *testing.T) {
	m := withModel(t,
		[]git.Worktree{
			{Path: "/tmp/wt-a", Branch: "main"},
			{Path: "/tmp/wt-b", Branch: "feat/foo"},
		},
		"/tmp/wt-a",
	)
	// focused defaults to paneGraph; do not flip it.
	raw := renderTopDashboard(m, 80)
	if strings.Contains(raw, "48;5;237") {
		t.Errorf("unfocused dashboard should not contain cursorRowBgStyle escape (48;5;237), got %q", raw)
	}
}

// TestRenderTopDashboardCurrentMarkerSurvivesCursor — when the cursor
// row coincides with the current ▶ row, the selectedStyle (fg 205 +
// bold) on the body survives the cursorRowBgStyle (bg 237) overlay.
// Both escape sequences appear in the output. Locks in Decision 4's
// "▶ + bold+select 표시는 focus 와 무관하게 항상 유지".
func TestRenderTopDashboardCurrentMarkerSurvivesCursor(t *testing.T) {
	m := withModel(t,
		[]git.Worktree{
			{Path: "/tmp/wt-a", Branch: "main"},
			{Path: "/tmp/wt-b", Branch: "feat/foo"},
		},
		"/tmp/wt-b",
	)
	m.focused = paneDashboard
	m.dashboardFocus.cursor = 1 // same as current
	raw := renderTopDashboard(m, 80)
	if !strings.Contains(raw, "38;5;205") {
		t.Errorf("current row body should carry selectedStyle fg (38;5;205), got %q", raw)
	}
	if !strings.Contains(raw, "48;5;237") {
		t.Errorf("cursor row should carry cursorRowBgStyle bg (48;5;237), got %q", raw)
	}
	// ▶ glyph (prefix) survives independently of body styling.
	plain := ansi.Strip(raw)
	if !strings.Contains(plain, "▶") {
		t.Errorf("▶ marker should still appear in plain text, got %q", plain)
	}
}

func TestModelViewWithDashboardDoesNotPanic(t *testing.T) {
	m := New()
	updated, _ := m.Update(initWindowSize(120, 40))
	m = updated.(Model)
	m.refs.SetWorktrees(
		[]git.Worktree{
			{Path: "/tmp/wt-a", Branch: "main"},
			{Path: "/tmp/wt-b", Branch: "feat/foo"},
		},
		"/tmp/wt-a",
	)
	m.refs, _ = m.refs.Update(refsLoadedMsg{refs: nil})
	view := m.View()
	if view == "" {
		t.Fatal("View should produce non-empty output with dashboard always on")
	}
}

// initWindowSize is a thin alias so the test reads top-to-bottom without a
// nested type literal.
func initWindowSize(w, h int) tea.WindowSizeMsg {
	return tea.WindowSizeMsg{Width: w, Height: h}
}
