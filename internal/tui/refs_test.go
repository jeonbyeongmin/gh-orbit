package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// refModel is a storage container post-PR-B2 — these tests cover the
// data-shape contracts (LocalRefs / RemoteRefs / Worktrees / dirty maps
// / Local Changes meta / fetch freshness) and the few render helpers
// that the dashboard reuses (composeLocalChangesRow, formatLocalChangesMeta,
// renderWorktreeSidebarRow). Cursor / View / Update key handling left
// with the sidebar in PR B2.

func TestRefModelHandlesLoadFailure(t *testing.T) {
	r := newRefsModel()
	r, _ = r.Update(refsLoadFailedMsg{err: errSentinel})
	if !r.loaded {
		t.Error("loaded should flip to true on a load failure (loaded = 'we tried')")
	}
	if r.err == nil {
		t.Error("err should carry the failure sentinel")
	}
}

func TestRefModelResetForReloadClearsLoaded(t *testing.T) {
	r := newRefsModel()
	r, _ = r.Update(refsLoadedMsg{refs: nil})
	if !r.loaded {
		t.Fatal("setup: loaded should be true after refsLoadedMsg")
	}
	r.ResetForReload()
	if r.loaded {
		t.Error("ResetForReload should flip loaded back to false")
	}
}

// --- partitionByKind ---

func TestPartitionByKindSeparatesByKind(t *testing.T) {
	refs := []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal},
		{ShortName: "feat/foo", Kind: git.RefKindLocal},
		{ShortName: "origin/feat/qa", Kind: git.RefKindRemote},
		{ShortName: "v0.1.0", Kind: git.RefKindTag},
	}
	out := partitionByKind(refs)
	if len(out[0]) != 2 {
		t.Errorf("local count = %d, want 2", len(out[0]))
	}
	if len(out[1]) != 1 || out[1][0].ShortName != "origin/feat/qa" {
		t.Errorf("remote slice = %+v, want [origin/feat/qa]", out[1])
	}
	if len(out[2]) != 1 || out[2][0].ShortName != "v0.1.0" {
		t.Errorf("tag slice = %+v, want [v0.1.0]", out[2])
	}
}

func TestPartitionByKindHidesMirroredRemote(t *testing.T) {
	refs := []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal},
		{ShortName: "origin/main", Kind: git.RefKindRemote},
		{ShortName: "origin/feat/qa", Kind: git.RefKindRemote},
	}
	out := partitionByKind(refs)
	if len(out[1]) != 1 || out[1][0].ShortName != "origin/feat/qa" {
		t.Errorf("mirror filter should hide origin/main when local main exists; remote slice = %+v", out[1])
	}
}

// --- Worktrees ---

func TestRefModelSetWorktreesPrunesDirtyMaps(t *testing.T) {
	r := newRefsModel()
	r.SetWorktrees([]git.Worktree{{Path: "/a"}, {Path: "/b"}}, "/a")
	r.SetWorktreeDirty("/a", true, false)
	r.SetWorktreeDirty("/b", false, true)
	r.SetAgentState("/b", agentStateRunning)
	if !r.WorktreeDirty("/a") {
		t.Error("setup: /a should be dirty")
	}
	// /b disappears.
	r.SetWorktrees([]git.Worktree{{Path: "/a"}}, "/a")
	if _, present := r.worktreeDirty["/b"]; present {
		t.Error("SetWorktrees should prune dirty entries for paths that disappeared")
	}
	if _, present := r.worktreeTimedOut["/b"]; present {
		t.Error("SetWorktrees should prune timedOut entries for paths that disappeared")
	}
	if _, present := r.agentSessionState["/b"]; present {
		t.Error("SetWorktrees should prune agent-state entries for paths that disappeared")
	}
}

func TestRefModelWorktreesAccessor(t *testing.T) {
	r := newRefsModel()
	r.SetWorktrees([]git.Worktree{{Path: "/a"}, {Path: "/b"}}, "/a")
	wts := r.Worktrees()
	if len(wts) != 2 {
		t.Errorf("Worktrees() len = %d, want 2", len(wts))
	}
}

// --- Local Changes meta helpers ---

func TestFormatLocalChangesMetaSingularFile(t *testing.T) {
	r := newRefsModel()
	r.SetLocalChangesSummary(git.LocalChangesSummary{FilesChanged: 1, Insertions: 5, Deletions: 0}, time.Now())
	got := r.formatLocalChangesMeta(time.Now())
	if !strings.Contains(got, "1 file ·") {
		t.Errorf("singular form expected, got %q", got)
	}
}

func TestFormatLocalChangesMetaEmptySummary(t *testing.T) {
	r := newRefsModel()
	if got := r.formatLocalChangesMeta(time.Now()); got != "" {
		t.Errorf("empty summary should yield empty meta, got %q", got)
	}
}

func TestFormatLocalChangesMetaJustNowNoAgoSuffix(t *testing.T) {
	r := newRefsModel()
	now := time.Now()
	r.SetLocalChangesSummary(git.LocalChangesSummary{FilesChanged: 1, Insertions: 1, Deletions: 0}, now)
	got := r.formatLocalChangesMeta(now)
	if strings.Contains(got, "just now ago") {
		t.Errorf("'just now' should not get ' ago' suffix: %q", got)
	}
	if !strings.Contains(got, "just now") {
		t.Errorf("expected 'just now' segment: %q", got)
	}
}

func TestComposeLocalChangesRowTruncatesMetaBeforeLabel(t *testing.T) {
	out := composeLocalChangesRow("● Local Changes", "3 files · +47 -12 · 2m ago", 22, false)
	plain := ansi.Strip(out)
	if !strings.Contains(plain, "● Local Changes") {
		t.Errorf("label must survive narrow width: %q", plain)
	}
	if !strings.HasSuffix(plain, "…") {
		t.Errorf("expected truncation marker on meta, got %q", plain)
	}
}

func TestComposeLocalChangesRowBareLabelWhenTooNarrowForMeta(t *testing.T) {
	out := composeLocalChangesRow("● Local Changes", "3 files", 15, false)
	plain := ansi.Strip(out)
	if strings.Contains(plain, "files") {
		t.Errorf("meta should drop entirely when no room: %q", plain)
	}
}

// --- renderWorktreeSidebarRow shape (the dashboard reuses it) ---

func TestRenderWorktreeRowShowsCurrentMarker(t *testing.T) {
	out := renderWorktreeSidebarRow(
		git.Worktree{Path: "/tmp/wt-a", Branch: "main"},
		true, false, agentStateNone, 0, "", "", time.Time{}, time.Time{}, 40,
	)
	if !strings.Contains(ansi.Strip(out), "▶") {
		t.Errorf("current=true row should carry ▶ marker: %q", ansi.Strip(out))
	}
}

func TestRenderWorktreeRowDirtyMarkerLast(t *testing.T) {
	// With no last-commit data the dirty marker is still the trailing column.
	out := renderWorktreeSidebarRow(
		git.Worktree{Path: "/tmp/wt-a", Branch: "main"},
		false, false, agentStateNone, 0, "●", "", time.Time{}, time.Time{}, 40,
	)
	plain := ansi.Strip(out)
	if !strings.HasSuffix(strings.TrimSpace(plain), "●") {
		t.Errorf("dirty marker should be the trailing segment: %q", plain)
	}
}

// --- last-commit column (subject + relative time) ---

var lcNow = time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
var lcWhen = lcNow.Add(-2 * time.Minute) // relativeShortAt → "2m"

func TestRenderWorktreeRowAllColumns(t *testing.T) {
	out := renderWorktreeSidebarRow(
		git.Worktree{Path: "/repo/feat-auth", Branch: "feat/auth"},
		false, false, agentStateNone, 0, "●", "fix login race", lcWhen, lcNow, 60,
	)
	plain := ansi.Strip(out)
	wantOrder := []string{"feat-auth", "feat/auth", "●", "fix login race", "2m"}
	prev := -1
	for _, tok := range wantOrder {
		i := strings.Index(plain, tok)
		if i < 0 {
			t.Fatalf("token %q missing from row: %q", tok, plain)
		}
		if i < prev {
			t.Errorf("token %q out of display order in: %q", tok, plain)
		}
		prev = i
	}
}

func TestRenderWorktreeRowSubjectCappedAt30(t *testing.T) {
	subject := "abcdefghijklmnopqrstuvwxyz0123456789" // 36 runes
	out := renderWorktreeSidebarRow(
		git.Worktree{Path: "/repo/wt", Branch: "br"},
		false, false, agentStateNone, 0, "●", subject, lcWhen, lcNow, 120,
	)
	plain := ansi.Strip(out)
	if !strings.Contains(plain, "…") {
		t.Errorf("over-cap subject should be truncated with …: %q", plain)
	}
	if strings.Contains(plain, "456789") {
		t.Errorf("subject should be capped at 30, tail leaked: %q", plain)
	}
}

func TestRenderWorktreeRowDropOrder(t *testing.T) {
	wt := git.Worktree{Path: "/repo/wt", Branch: "br"}
	subject := "hello world here" // 16
	cases := []struct {
		width                                    int
		subject, branch, dirty, timeStr, nameTok bool
	}{
		{40, true, true, true, true, true},   // all columns
		{18, false, true, true, true, true},  // subject dropped (floor)
		{13, false, false, true, true, true}, // branch dropped
		{8, false, false, true, false, true}, // time dropped, ● kept
		{4, false, false, false, false, true},
	}
	for _, c := range cases {
		out := renderWorktreeSidebarRow(wt, false, false, agentStateNone, 0, "●", subject, lcWhen, lcNow, c.width)
		plain := ansi.Strip(out)
		// Universal invariant: rendered display width never exceeds width.
		if w := runewidth.StringWidth(plain); w > c.width {
			t.Errorf("width=%d: rendered width %d exceeds budget: %q", c.width, w, plain)
		}
		if c.nameTok && !strings.Contains(plain, "wt") {
			t.Errorf("width=%d: name must always survive: %q", c.width, plain)
		}
		if got := strings.Contains(plain, "hello"); got != c.subject {
			t.Errorf("width=%d: subject present=%v want %v: %q", c.width, got, c.subject, plain)
		}
		if got := strings.Contains(plain, "br"); got != c.branch {
			t.Errorf("width=%d: branch present=%v want %v: %q", c.width, got, c.branch, plain)
		}
		if got := strings.Contains(plain, "●"); got != c.dirty {
			t.Errorf("width=%d: dirty present=%v want %v: %q", c.width, got, c.dirty, plain)
		}
		if got := strings.Contains(plain, "2m"); got != c.timeStr {
			t.Errorf("width=%d: time present=%v want %v: %q", c.width, got, c.timeStr, plain)
		}
	}
}

func TestRenderWorktreeRowAgentMarker(t *testing.T) {
	// running state paints the spinner glyph right after name, before branch.
	running := string(agentSpinnerFrames[0])
	out := renderWorktreeSidebarRow(
		git.Worktree{Path: "/repo/wt", Branch: "br"},
		false, false, agentStateRunning, 0, "●", "", time.Time{}, time.Time{}, 40,
	)
	plain := ansi.Strip(out)
	iName := strings.Index(plain, "wt")
	iMark := strings.Index(plain, running)
	iBr := strings.Index(plain, "br")
	if iMark < 0 {
		t.Fatalf("running row should carry the spinner glyph: %q", plain)
	}
	if iName < 0 || iName >= iMark || iMark >= iBr {
		t.Errorf("want display order name < marker < branch, got name=%d marker=%d br=%d in %q", iName, iMark, iBr, plain)
	}
}

func TestRenderWorktreeRowAgentMarkerStateGlyphs(t *testing.T) {
	cases := []struct {
		state agentState
		glyph string
	}{
		{agentStateRunning, string(agentSpinnerFrames[0])},
		{agentStateParked, agentParkedGlyph},
		{agentStateUnknownActive, agentUnknownGlyph},
	}
	for _, c := range cases {
		out := renderWorktreeSidebarRow(
			git.Worktree{Path: "/repo/wt", Branch: "br"},
			false, false, c.state, 0, "", "", time.Time{}, time.Time{}, 40,
		)
		if plain := ansi.Strip(out); !strings.Contains(plain, c.glyph) {
			t.Errorf("state %v should paint %q, got %q", c.state, c.glyph, plain)
		}
	}
	// agentStateNone paints no marker at all.
	out := renderWorktreeSidebarRow(
		git.Worktree{Path: "/repo/wt", Branch: "br"},
		false, false, agentStateNone, 0, "", "", time.Time{}, time.Time{}, 40,
	)
	for _, g := range []string{string(agentSpinnerFrames[0]), agentParkedGlyph, agentUnknownGlyph} {
		if strings.Contains(ansi.Strip(out), g) {
			t.Errorf("none state should paint no marker, found %q in %q", g, ansi.Strip(out))
		}
	}
}

func TestRenderWorktreeRowAgentMarkerOutlastsDirty(t *testing.T) {
	// The agent marker is the highest-value fixed column: at a width that
	// forces ● out it must still render (drop order …→ ● → agent) while name
	// always survives. The marker is a single Braille cell now: avail 7
	// (width 9 − prefix 2) fits "wt · ⠋" (6) but not "wt · ⠋ · ●" (10).
	mark := string(agentSpinnerFrames[0])
	wt := git.Worktree{Path: "/repo/wt"} // no branch keeps the row short
	out := renderWorktreeSidebarRow(wt, false, false, agentStateRunning, 0, "●", "", time.Time{}, time.Time{}, 9)
	plain := ansi.Strip(out)
	if w := runewidth.StringWidth(plain); w > 9 {
		t.Fatalf("width 9 budget exceeded: %d in %q", w, plain)
	}
	if !strings.Contains(plain, "wt") {
		t.Errorf("name must always survive: %q", plain)
	}
	if !strings.Contains(plain, mark) {
		t.Errorf("agent marker should outlast ● under width pressure: %q", plain)
	}
	if strings.Contains(plain, "●") {
		t.Errorf("● should drop before the agent marker at this width: %q", plain)
	}
}

func TestRenderWorktreeRowSubjectFloor(t *testing.T) {
	// name(1) · time(2): fixedWidth = "n · 2m" = 6. leftover = avail-6-sep(3).
	// leftover>=12 needs avail>=21 → width>=23; width 22 → leftover 11 → hidden.
	wt := git.Worktree{Path: "/repo/n"}
	subject := "abcdefghijklmnop"
	atFloor := renderWorktreeSidebarRow(wt, false, false, agentStateNone, 0, "", subject, lcWhen, lcNow, 23)
	if !strings.Contains(ansi.Strip(atFloor), "abcde") {
		t.Errorf("leftover==12 should show subject: %q", ansi.Strip(atFloor))
	}
	belowFloor := renderWorktreeSidebarRow(wt, false, false, agentStateNone, 0, "", subject, lcWhen, lcNow, 22)
	if strings.Contains(ansi.Strip(belowFloor), "abcde") {
		t.Errorf("leftover==11 should hide subject whole: %q", ansi.Strip(belowFloor))
	}
}

func TestRenderWorktreeRowBlankWhenNoCommit(t *testing.T) {
	// Unborn-HEAD / loading: empty subject + zero time → no subject/time
	// columns, no `?`, but the row still renders name + branch.
	out := renderWorktreeSidebarRow(
		git.Worktree{Path: "/repo/fresh", Branch: "wip"},
		false, false, agentStateNone, 0, "", "", time.Time{}, lcNow, 60,
	)
	plain := ansi.Strip(out)
	if !strings.Contains(plain, "fresh") || !strings.Contains(plain, "wip") {
		t.Errorf("row should still show name + branch: %q", plain)
	}
	if strings.Contains(plain, "?") {
		t.Errorf("missing last-commit must be blank, never `?`: %q", plain)
	}
}

func TestRenderWorktreeRowWideRuneSubjectWidthSafe(t *testing.T) {
	// Korean subject: each syllable is width 2. The width invariant must hold.
	out := renderWorktreeSidebarRow(
		git.Worktree{Path: "/repo/wt", Branch: "br"},
		false, false, agentStateNone, 0, "●", "버그 수정 완료", lcWhen, lcNow, 40,
	)
	if w := runewidth.StringWidth(ansi.Strip(out)); w > 40 {
		t.Errorf("wide-rune subject overflowed width: %d > 40: %q", w, ansi.Strip(out))
	}
}
