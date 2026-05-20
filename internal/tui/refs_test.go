package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// pressRefsKey dispatches a single key into the refModel directly. Used by
// tests that exercise refs.go's local key handling without standing up a
// full Model.
func pressRefsKey(t *testing.T, r refModel, key string) refModel {
	t.Helper()
	out, _ := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	return out
}

func TestRefModelInitialView(t *testing.T) {
	r := newRefsModel()
	if got := r.View(); got != "loading…" {
		t.Errorf("initial view = %q, want %q", got, "loading…")
	}
}

func TestRefModelHandlesLoadFailure(t *testing.T) {
	r := newRefsModel()
	r, _ = r.Update(refsLoadFailedMsg{err: errSentinel})
	view := r.View()
	if !strings.Contains(view, "load error") {
		t.Errorf("error view should mention load error, got %q", view)
	}
}

func TestRefModelDefaultCursorOnLocalChanges(t *testing.T) {
	r := newRefsModel()
	if r.onWorktree != -1 {
		t.Errorf("default onWorktree = %d, want -1", r.onWorktree)
	}
	if !r.onLocalChanges {
		t.Error("default onLocalChanges should be true (sticky row is the entry-point focus)")
	}
}

func TestRefModelEnterOnLocalChangesEmitsLocalChangesMsg(t *testing.T) {
	r := newRefsModel()
	r, _ = r.Update(refsLoadedMsg{refs: nil})
	_, cmd := r.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter on Local Changes should emit a cmd")
	}
	msg := cmd()
	if _, ok := msg.(localChangesEnterRequestedMsg); !ok {
		t.Errorf("enter on Local Changes emitted %T, want localChangesEnterRequestedMsg", msg)
	}
}

func TestRefModelEnterOnWorktreeEmitsSwitchMsg(t *testing.T) {
	r := newRefsModel()
	r.SetWorktrees([]git.Worktree{
		{Path: "/tmp/wt-a", Branch: "main"},
		{Path: "/tmp/wt-b", Branch: "feat/foo"},
	}, "/tmp/wt-a")
	r, _ = r.Update(refsLoadedMsg{refs: nil})
	r.onWorktree = 1
	r.onLocalChanges = false

	_, cmd := r.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter on worktree row should emit a cmd")
	}
	msg := cmd()
	sw, ok := msg.(refWorktreeSwitchRequestedMsg)
	if !ok {
		t.Errorf("enter on worktree row emitted %T, want refWorktreeSwitchRequestedMsg", msg)
	}
	if sw.path != "/tmp/wt-b" {
		t.Errorf("switch path = %q, want /tmp/wt-b", sw.path)
	}
}

func TestRefModelAOnWorktreeEmitsAddMsg(t *testing.T) {
	r := newRefsModel()
	r.SetWorktrees([]git.Worktree{{Path: "/tmp/wt-a", Branch: "main"}}, "/tmp/wt-a")
	r, _ = r.Update(refsLoadedMsg{refs: nil})
	r.onWorktree = 0
	r.onLocalChanges = false

	_, cmd := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if cmd == nil {
		t.Fatal("a on worktree row should emit a cmd")
	}
	if _, ok := cmd().(refWorktreeAddRequestedMsg); !ok {
		t.Errorf("a on worktree row emitted %T, want refWorktreeAddRequestedMsg", cmd())
	}
}

func TestRefModelAOnLocalChangesNoOp(t *testing.T) {
	r := newRefsModel()
	r, _ = r.Update(refsLoadedMsg{refs: nil})
	_, cmd := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if cmd != nil {
		t.Errorf("a on Local Changes should be no-op, got cmd = %v", cmd)
	}
}

func TestRefModelJKAcrossWorktreesAndLocalChanges(t *testing.T) {
	r := newRefsModel()
	r.SetWorktrees([]git.Worktree{
		{Path: "/tmp/wt-a", Branch: "main"},
		{Path: "/tmp/wt-b", Branch: "feat/a"},
	}, "/tmp/wt-a")
	r, _ = r.Update(refsLoadedMsg{refs: nil})
	r.onWorktree = 0
	r.onLocalChanges = false

	// j → wt-b
	r = pressRefsKey(t, r, "j")
	if r.onWorktree != 1 {
		t.Errorf("after j: onWorktree = %d, want 1", r.onWorktree)
	}
	// j → Local Changes
	r = pressRefsKey(t, r, "j")
	if r.onWorktree != -1 || !r.onLocalChanges {
		t.Errorf("after j past last wt: onWorktree=%d onLocalChanges=%v, want -1/true", r.onWorktree, r.onLocalChanges)
	}
	// j on Local Changes: no further
	r = pressRefsKey(t, r, "j")
	if !r.onLocalChanges {
		t.Error("j on Local Changes should stay")
	}
	// k → wt-b
	r = pressRefsKey(t, r, "k")
	if r.onWorktree != 1 || r.onLocalChanges {
		t.Errorf("after k from Local Changes: onWorktree=%d onLocalChanges=%v, want 1/false", r.onWorktree, r.onLocalChanges)
	}
	// k → wt-a
	r = pressRefsKey(t, r, "k")
	if r.onWorktree != 0 {
		t.Errorf("after k: onWorktree = %d, want 0", r.onWorktree)
	}
	// k on first wt: stays
	r = pressRefsKey(t, r, "k")
	if r.onWorktree != 0 {
		t.Errorf("k on first wt should stay, onWorktree = %d", r.onWorktree)
	}
}

func TestRefModelGGoesToTop(t *testing.T) {
	r := newRefsModel()
	r.SetWorktrees([]git.Worktree{{Path: "/tmp/wt-a"}}, "/tmp/wt-a")
	r, _ = r.Update(refsLoadedMsg{refs: nil})

	r = pressRefsKey(t, r, "g")
	if r.onWorktree != 0 || r.onLocalChanges {
		t.Errorf("g should land on first worktree: onWorktree=%d onLocalChanges=%v", r.onWorktree, r.onLocalChanges)
	}
}

func TestRefModelGGoesToLocalChangesWhenNoWorktrees(t *testing.T) {
	r := newRefsModel()
	r, _ = r.Update(refsLoadedMsg{refs: nil})

	r = pressRefsKey(t, r, "g")
	if !r.onLocalChanges {
		t.Error("g without worktrees should land on Local Changes")
	}
}

func TestRefModelCapitalGGoesToBottom(t *testing.T) {
	r := newRefsModel()
	r.SetWorktrees([]git.Worktree{{Path: "/tmp/wt-a"}}, "/tmp/wt-a")
	r, _ = r.Update(refsLoadedMsg{refs: nil})
	r.onWorktree = 0
	r.onLocalChanges = false

	r = pressRefsKey(t, r, "G")
	if !r.onLocalChanges {
		t.Error("G should land on Local Changes (bottom of inventory)")
	}
}

func TestRefModelSetWorktreesClampsCursorWhenEntryDisappears(t *testing.T) {
	r := newRefsModel()
	r.SetWorktrees([]git.Worktree{
		{Path: "/tmp/a"}, {Path: "/tmp/b"}, {Path: "/tmp/c"},
	}, "/tmp/a")
	r.onWorktree = 2
	r.onLocalChanges = false

	// /tmp/c disappears.
	r.SetWorktrees([]git.Worktree{{Path: "/tmp/a"}, {Path: "/tmp/b"}}, "/tmp/a")
	if r.onWorktree != -1 || !r.onLocalChanges {
		t.Errorf("disappeared cursor should fall back to Local Changes, got onWorktree=%d onLocalChanges=%v",
			r.onWorktree, r.onLocalChanges)
	}
}

func TestRefModelStickyRowVisibleInView(t *testing.T) {
	r := newRefsModel()
	r.SetSize(40, 10)
	r, _ = r.Update(refsLoadedMsg{refs: nil})

	view := ansi.Strip(r.View())
	if !strings.Contains(view, "● Local Changes") {
		t.Errorf("View should contain Local Changes sticky row, got %q", view)
	}
}

func TestRefModelStickyRowBareLabelWhenNoSummary(t *testing.T) {
	r := newRefsModel()
	r.SetSize(40, 10)
	r, _ = r.Update(refsLoadedMsg{refs: nil})

	view := ansi.Strip(r.View())
	if strings.Contains(view, "files ·") {
		t.Errorf("View without summary should not show meta, got %q", view)
	}
}

func TestRefModelStickyRowRendersInlineMeta(t *testing.T) {
	r := newRefsModel()
	r.SetSize(60, 10)
	r.SetLocalChangesSummary(
		git.LocalChangesSummary{FilesChanged: 3, Insertions: 12, Deletions: 4},
		time.Now().Add(-2*time.Minute),
	)
	r, _ = r.Update(refsLoadedMsg{refs: nil})

	view := ansi.Strip(r.View())
	if !strings.Contains(view, "3 files") || !strings.Contains(view, "+12 -4") {
		t.Errorf("inline meta missing, got %q", view)
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

// --- fetch footer ---

func TestFormatFetchFooterEmptyWhenNever(t *testing.T) {
	r := newRefsModel()
	if got := r.formatFetchFooter(time.Now(), 40); got != "" {
		t.Errorf("expected empty footer when lastFetchAt is zero, got %q", got)
	}
}

func TestFormatFetchFooterRendersAge(t *testing.T) {
	r := newRefsModel()
	now := time.Now()
	r.SetLastFetchAt(now.Add(-3 * time.Minute))
	got := ansi.Strip(r.formatFetchFooter(now, 40))
	if !strings.Contains(got, "fetched 3m ago") {
		t.Errorf("expected 'fetched 3m ago', got %q", got)
	}
}

func TestFormatFetchFooterJustNowNoAgoSuffix(t *testing.T) {
	r := newRefsModel()
	now := time.Now()
	r.SetLastFetchAt(now)
	got := ansi.Strip(r.formatFetchFooter(now, 40))
	if !strings.Contains(got, "fetched just now") {
		t.Errorf("expected 'fetched just now', got %q", got)
	}
	if strings.Contains(got, "just now ago") {
		t.Errorf("'just now' should not get ' ago' suffix, got %q", got)
	}
}

func TestRefModelViewIncludesFooterAfterFetch(t *testing.T) {
	r := newRefsModel()
	r.SetSize(40, 10)
	r.SetLastFetchAt(time.Now().Add(-5 * time.Minute))
	r, _ = r.Update(refsLoadedMsg{refs: nil})

	view := ansi.Strip(r.View())
	if !strings.Contains(view, "fetched") {
		t.Errorf("View after fetch should contain footer, got %q", view)
	}
}
