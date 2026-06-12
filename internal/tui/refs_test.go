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
		true, false, "", "", time.Time{}, time.Time{}, 40, 0,
	)
	if !strings.Contains(ansi.Strip(out), "▶") {
		t.Errorf("current=true row should carry ▶ marker: %q", ansi.Strip(out))
	}
}

func TestRenderWorktreeRowDirtyMarkerLast(t *testing.T) {
	// With no last-commit data the dirty marker is still the trailing column.
	out := renderWorktreeSidebarRow(
		git.Worktree{Path: "/tmp/wt-a", Branch: "main"},
		false, false, "●", "", time.Time{}, time.Time{}, 40, 0,
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
		false, false, "●", "fix login race", lcWhen, lcNow, 60, 0,
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
		false, false, "●", subject, lcWhen, lcNow, 120, 0,
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
	// Keep-priority name > agent > branch > subject > ● dirty > time, allocated
	// greedily: each column takes space if it fits, smaller lower-priority
	// columns still fill leftover a skipped bigger column left behind. name "wt"
	// (2), branch "br" (2), ● (1), time "2m" (2), subject floor 12.
	cases := []struct {
		width                                    int
		subject, branch, dirty, timeStr, nameTok bool
	}{
		{40, true, true, true, true, true},   // all columns fit
		{18, false, true, true, true, true},  // subject below floor; branch+●+time stay
		{13, false, true, true, false, true}, // time can't fit; branch+● stay
		{8, false, false, true, false, true}, // branch needs 7>avail6, ● (1) still fits
		{4, false, false, false, false, true},
	}
	for _, c := range cases {
		out := renderWorktreeSidebarRow(wt, false, false, "●", subject, lcWhen, lcNow, c.width, 0)
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

func TestRenderWorktreeRowSubjectFloor(t *testing.T) {
	// subject now outranks time, so it competes only with the name column:
	// room = avail - name(1) - sep(3). room>=12 needs avail>=16 → width>=18;
	// width 17 → room 11 → subject hidden whole (time takes the slack instead).
	wt := git.Worktree{Path: "/repo/n"}
	subject := "abcdefghijklmnop"
	atFloor := renderWorktreeSidebarRow(wt, false, false, "", subject, lcWhen, lcNow, 18, 0)
	if !strings.Contains(ansi.Strip(atFloor), "abcde") {
		t.Errorf("room==12 should show subject: %q", ansi.Strip(atFloor))
	}
	belowFloor := renderWorktreeSidebarRow(wt, false, false, "", subject, lcWhen, lcNow, 17, 0)
	if strings.Contains(ansi.Strip(belowFloor), "abcde") {
		t.Errorf("room==11 should hide subject whole: %q", ansi.Strip(belowFloor))
	}
}

func TestRenderWorktreeRowBlankWhenNoCommit(t *testing.T) {
	// Unborn-HEAD / loading: empty subject + zero time → no subject/time
	// columns, no `?`, but the row still renders name + branch.
	out := renderWorktreeSidebarRow(
		git.Worktree{Path: "/repo/fresh", Branch: "wip"},
		false, false, "", "", time.Time{}, lcNow, 60, 0,
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
		false, false, "●", "버그 수정 완료", lcWhen, lcNow, 40, 0,
	)
	if w := runewidth.StringWidth(ansi.Strip(out)); w > 40 {
		t.Errorf("wide-rune subject overflowed width: %d > 40: %q", w, ansi.Strip(out))
	}
}

func TestWorktreeDisplayNameCap(t *testing.T) {
	// A long branch-shaped worktree name is capped at worktreeNameCap with a
	// trailing … so it can't swallow the row.
	got := worktreeDisplayName("/repo/.worktrees/this-is-a-really-long-worktree-name-over-24")
	if w := runewidth.StringWidth(got); w > worktreeNameCap {
		t.Errorf("name width %d exceeds cap %d: %q", w, worktreeNameCap, got)
	}
	if !strings.HasPrefix(got, "this-is-a-really-long") || !strings.HasSuffix(got, "…") {
		t.Errorf("over-cap name should keep its head and end in …: %q", got)
	}
	if strings.Contains(got, "over-24") {
		t.Errorf("capped name should drop the tail: %q", got)
	}
	// A short name is returned untouched (no padding here — that's the row's job).
	if got := worktreeDisplayName("/repo/feat-auth"); got != "feat-auth" {
		t.Errorf("short name should pass through unchanged: %q", got)
	}
}

func TestRenderWorktreeRowBranchSubjectOutliveTimeDirty(t *testing.T) {
	// New keep-priority: branch + subject outrank ● and time. At a width where
	// name + branch + a capped subject consume the row, ● and time drop while
	// branch + subject survive — the inversion the redesign fixes.
	out := renderWorktreeSidebarRow(
		git.Worktree{Path: "/repo/wt", Branch: "br"},
		false, false, "●", "refactor allocation pass to honor priorities", lcWhen, lcNow, 30, 0,
	)
	plain := ansi.Strip(out)
	if w := runewidth.StringWidth(plain); w > 30 {
		t.Fatalf("width 30 budget exceeded: %d in %q", w, plain)
	}
	if !strings.Contains(plain, "br") {
		t.Errorf("branch must survive over time/●: %q", plain)
	}
	if !strings.Contains(plain, "refactor") {
		t.Errorf("subject must survive over time/●: %q", plain)
	}
	if strings.Contains(plain, "●") {
		t.Errorf("● should drop before branch/subject at this width: %q", plain)
	}
	if strings.Contains(plain, "2m") {
		t.Errorf("time should drop before branch/subject at this width: %q", plain)
	}
}

func TestRenderWorktreeRowNameColumnAligns(t *testing.T) {
	// With a set-wide nameColW, a short name pads so the branch column starts at
	// the same offset as a row whose name already fills the column.
	const nameColW = 8 // width of the longer name below
	short := ansi.Strip(renderWorktreeSidebarRow(
		git.Worktree{Path: "/r/aa", Branch: "br"},
		false, false, "", "", time.Time{}, time.Time{}, 40, nameColW,
	))
	long := ansi.Strip(renderWorktreeSidebarRow(
		git.Worktree{Path: "/r/bbbbbbbb", Branch: "br"},
		false, false, "", "", time.Time{}, time.Time{}, 40, nameColW,
	))
	if i, j := strings.Index(short, "br"), strings.Index(long, "br"); i != j {
		t.Errorf("branch column should align: short@%d long@%d (%q / %q)", i, j, short, long)
	}
}
