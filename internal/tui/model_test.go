package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

func TestSplitRatioClampOnResize(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	if m.splitRatio != splitRatioDefault {
		t.Fatalf("default splitRatio = %d, want %d", m.splitRatio, splitRatioDefault)
	}

	send := func(t tea.KeyType) {
		updated, _ := m.Update(tea.KeyMsg{Type: t})
		m = updated.(Model)
	}

	// 12 ctrl+down presses (each +5) would push past splitRatioMax=80; we
	// expect it to clamp.
	for i := 0; i < 12; i++ {
		send(tea.KeyCtrlDown)
	}
	if m.splitRatio != splitRatioMax {
		t.Errorf("after 12 ctrl+down, splitRatio = %d, want %d (clamp)", m.splitRatio, splitRatioMax)
	}

	// Now drain back below the floor.
	for i := 0; i < 16; i++ {
		send(tea.KeyCtrlUp)
	}
	if m.splitRatio != splitRatioMin {
		t.Errorf("after 16 ctrl+up, splitRatio = %d, want %d (clamp)", m.splitRatio, splitRatioMin)
	}
}

func TestYKeyCopiesHashFromCommitTab(t *testing.T) {
	original := clipboardWrite
	t.Cleanup(func() { clipboardWrite = original })
	var captured string
	clipboardWrite = func(s string) error {
		captured = s
		return nil
	}

	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	// Pretend graph cursor selected this commit.
	m.commitDetail.MarkLoading("0123456789abcdef0123456789abcdef01234567", 1)
	m.focused = paneTab
	if m.tabs.Active() != tabCommit {
		t.Fatalf("tabs default = %v, want tabCommit", m.tabs.Active())
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = updated.(Model)
	if captured != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("clipboard captured = %q, want full hash", captured)
	}
	if !strings.HasPrefix(m.status, "copied ") {
		t.Errorf("status = %q, want 'copied ...'", m.status)
	}
}

func TestYKeyIsNoopOutsideCommitTab(t *testing.T) {
	original := clipboardWrite
	t.Cleanup(func() { clipboardWrite = original })
	var captured string
	clipboardWrite = func(s string) error {
		captured = s
		return nil
	}

	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.commitDetail.MarkLoading("abc1234deadbeefcafe1234567890abcdef12345", 1)
	// graph focused, not paneTab
	m.focused = paneGraph

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = updated.(Model)
	if captured != "" {
		t.Errorf("clipboard should not be written when graph focused, got %q", captured)
	}
}

func TestYKeySurfacesClipboardError(t *testing.T) {
	original := clipboardWrite
	t.Cleanup(func() { clipboardWrite = original })
	clipboardWrite = func(string) error {
		return errors.New("xclip: executable file not found in $PATH")
	}

	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.commitDetail.MarkLoading("abc1234deadbeefcafe1234567890abcdef12345", 1)
	m.focused = paneTab

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = updated.(Model)
	if !strings.Contains(m.status, "clipboard unavailable") {
		t.Errorf("status = %q, want clipboard error", m.status)
	}
	if !strings.Contains(m.status, "xclip") {
		t.Errorf("status should carry the underlying error, got %q", m.status)
	}
}

func TestFocusCycle_TabWrap(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	// New() seeds focused at paneGraph.
	if m.focused != paneGraph {
		t.Fatalf("initial focus = %v, want paneGraph", m.focused)
	}

	tabStep := func() Model {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
		return updated.(Model)
	}

	// tab cycles forward and wraps: graph → tab → refs → graph → tab.
	m = tabStep()
	if m.focused != paneTab {
		t.Errorf("after tab #1 from graph, focus = %v, want paneTab", m.focused)
	}
	m = tabStep()
	if m.focused != paneRefs {
		t.Errorf("after tab #2 from tab, focus = %v, want paneRefs (wrap)", m.focused)
	}
	m = tabStep()
	if m.focused != paneGraph {
		t.Errorf("after tab #3 from refs, focus = %v, want paneGraph", m.focused)
	}
	m = tabStep()
	if m.focused != paneTab {
		t.Errorf("after tab #4 from graph, focus = %v, want paneTab", m.focused)
	}
}

func TestTabPaneHL_TogglesCommitChanges(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.focused = paneTab
	if m.tabs.Active() != tabCommit {
		t.Fatalf("tabs default = %v, want tabCommit", m.tabs.Active())
	}

	send := func(r rune) Model {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		return updated.(Model)
	}

	m = send('l')
	if m.tabs.Active() != tabChanges {
		t.Errorf("after l on paneTab, active = %v, want tabChanges", m.tabs.Active())
	}
	m = send('l')
	if m.tabs.Active() != tabCommit {
		t.Errorf("after l #2 (wrap), active = %v, want tabCommit", m.tabs.Active())
	}
	m = send('h')
	if m.tabs.Active() != tabChanges {
		t.Errorf("after h on paneTab, active = %v, want tabChanges (wrap reverse)", m.tabs.Active())
	}
}

func TestRefsGraphHL_NoOp(t *testing.T) {
	send := func(m Model, r rune) Model {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		return updated.(Model)
	}

	for _, focus := range []pane{paneRefs, paneGraph} {
		m := New()
		updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
		m = updated.(Model)
		m.focused = focus
		startActive := m.tabs.Active()

		for _, key := range []rune{'h', 'l'} {
			m = send(m, key)
			if m.focused != focus {
				t.Errorf("h/l from %v moved focus to %v, want unchanged", focus, m.focused)
			}
			if m.tabs.Active() != startActive {
				t.Errorf("h/l from %v changed tab to %v, want %v", focus, m.tabs.Active(), startActive)
			}
		}
	}
}

func TestDiffOverlay_TabHLSwallowed(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.mode = viewModeDiffWindow
	startFocus := m.focused
	startActive := m.tabs.Active()

	cases := []struct {
		name string
		msg  tea.KeyMsg
	}{
		{"tab", tea.KeyMsg{Type: tea.KeyTab}},
		{"h", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}}},
		{"l", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}}},
	}
	for _, tc := range cases {
		updated, cmd := m.Update(tc.msg)
		m = updated.(Model)
		if m.mode != viewModeDiffWindow {
			t.Errorf("%s in overlay closed/changed mode to %v, want viewModeDiffWindow", tc.name, m.mode)
		}
		if m.focused != startFocus {
			t.Errorf("%s in overlay moved focus to %v, want %v", tc.name, m.focused, startFocus)
		}
		if m.tabs.Active() != startActive {
			t.Errorf("%s in overlay changed tab to %v, want %v", tc.name, m.tabs.Active(), startActive)
		}
		if cmd != nil {
			t.Errorf("%s in overlay should not dispatch a cmd, got %v", tc.name, cmd)
		}
	}
}

func TestModelInitSeedsCurrentRefsWithAllSentinel(t *testing.T) {
	m := New()
	if got, want := m.currentRefs, []string{refsAllSentinel}; !slices.Equal(got, want) {
		t.Errorf("currentRefs at New = %v, want %v (unified graph default)", got, want)
	}
}

func TestModelRefSelectedJumpsCursor(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	now := time.Now()
	updated, _ = m.Update(commitsLoadedMsg{rows: []graphRow{
		{commit: git.Commit{Hash: "aaa1111", Subject: "first", AuthorTime: now}},
		{commit: git.Commit{Hash: "bbb2222", Subject: "second", AuthorTime: now}},
		{commit: git.Commit{Hash: "ccc3333", Subject: "third", AuthorTime: now}},
	}})
	m = updated.(Model)
	if !m.graph.loaded {
		t.Fatalf("graph should be loaded before refSelectedMsg")
	}

	updated, cmd := m.Update(refSelectedMsg{ref: git.Ref{
		FullName:   "refs/heads/feat",
		ShortName:  "feat",
		Kind:       git.RefKindLocal,
		ObjectName: "ccc3333",
	}})
	m = updated.(Model)
	if !m.graph.loaded {
		t.Errorf("graph.loaded should remain true after refSelectedMsg (no reload)")
	}
	// refSelectedMsg dispatches a diff debounce tick so the right pane
	// refreshes for the newly focused commit. It does NOT dispatch a graph
	// load cmd — currentRefs stays at --all.
	if cmd == nil {
		t.Error("refSelectedMsg should dispatch a diff debounce cmd for the new cursor")
	}
	if got := m.graph.list.Index(); got != 2 {
		t.Errorf("graph cursor index = %d, want 2 (row of ccc3333)", got)
	}
	if got, want := m.currentRefs, []string{refsAllSentinel}; !slices.Equal(got, want) {
		t.Errorf("currentRefs should stay at --all after Enter, got %v want %v", got, want)
	}
	if m.status != "" {
		t.Errorf("status should be empty after successful jump, got %q", m.status)
	}
}

func TestModelRefSelectedTipMissingShowsStatus(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	updated, _ = m.Update(commitsLoadedMsg{rows: []graphRow{
		{commit: git.Commit{Hash: "aaa1111", Subject: "first", AuthorTime: time.Now()}},
	}})
	m = updated.(Model)

	updated, _ = m.Update(refSelectedMsg{ref: git.Ref{
		FullName:   "refs/heads/old",
		ShortName:  "old",
		Kind:       git.RefKindLocal,
		ObjectName: "deadbeef",
	}})
	m = updated.(Model)
	if !strings.Contains(m.status, "ref tip not in loaded window") {
		t.Errorf("status %q should mention loaded window", m.status)
	}
	if !strings.Contains(m.status, "old") {
		t.Errorf("status %q should mention the ref short name", m.status)
	}
}

func TestModelRKeyReloadsBothPanes(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	updated, _ = m.Update(commitsLoadedMsg{rows: []graphRow{
		{commit: git.Commit{Hash: "abc1234", Subject: "first", AuthorTime: time.Now()}},
	}})
	m = updated.(Model)
	updated, _ = m.Update(refsLoadedMsg{refs: []git.Ref{
		{FullName: "refs/heads/main", ShortName: "main", Kind: git.RefKindLocal},
	}})
	m = updated.(Model)
	if !m.graph.loaded || !m.refs.loaded {
		t.Fatalf("both panes should be loaded before r")
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = updated.(Model)
	if m.graph.loaded {
		t.Errorf("r should reset graph.loaded")
	}
	if m.refs.loaded {
		t.Errorf("r should reset refs.loaded")
	}
	if cmd == nil {
		t.Fatal("r should return a batched load cmd")
	}
	if !strings.Contains(m.graph.View(), "loading") {
		t.Errorf("graph view should show loading after r, got %q", m.graph.View())
	}
	if !strings.Contains(m.refs.View(), "loading") {
		t.Errorf("refs view should show loading after r, got %q", m.refs.View())
	}
}

func TestModelRKeyPreservesAllSentinel(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	// Selecting a ref no longer mutates currentRefs (unified graph keeps the
	// --all base). r must preserve that invariant across reloads.
	updated, _ = m.Update(refSelectedMsg{ref: git.Ref{
		FullName:   "refs/heads/feat",
		ShortName:  "feat",
		Kind:       git.RefKindLocal,
		ObjectName: "abc1234",
	}})
	m = updated.(Model)
	updated, _ = m.Update(commitsLoadedMsg{rows: []graphRow{
		{commit: git.Commit{Hash: "abc1234", Subject: "first", AuthorTime: time.Now()}},
	}})
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = updated.(Model)
	if got, want := m.currentRefs, []string{refsAllSentinel}; !slices.Equal(got, want) {
		t.Errorf("r should preserve --all sentinel: got %v want %v", got, want)
	}
}

func TestModelCapitalRIsReservedAndIgnored(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	updated, _ = m.Update(commitsLoadedMsg{rows: []graphRow{
		{commit: git.Commit{Hash: "abc1234", Subject: "first", AuthorTime: time.Now()}},
	}})
	m = updated.(Model)
	updated, _ = m.Update(refsLoadedMsg{refs: []git.Ref{
		{FullName: "refs/heads/main", ShortName: "main", Kind: git.RefKindLocal},
	}})
	m = updated.(Model)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	m = updated.(Model)
	if cmd != nil {
		t.Errorf("R is reserved for a future Rebase action and should not dispatch yet, got cmd=%v", cmd)
	}
	if !m.graph.loaded || !m.refs.loaded {
		t.Errorf("R should not reset pane state")
	}
}

func TestModelFKeyDispatchesFetch(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'F'}})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("F should return a fetchCmd")
	}
	if !m.fetchInFlight {
		t.Error("F should set fetchInFlight=true")
	}
	if m.status != "fetching…" {
		t.Errorf("status = %q, want fetching…", m.status)
	}

	// Second F while in-flight is a no-op.
	updated, cmd2 := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'F'}})
	m = updated.(Model)
	if cmd2 != nil {
		t.Error("second F should not dispatch a parallel fetch")
	}
	if m.status != "fetching…" {
		t.Errorf("status should still be fetching…, got %q", m.status)
	}
}

func TestModelFetchSucceededReloadsBothPanes(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	updated, _ = m.Update(commitsLoadedMsg{rows: []graphRow{
		{commit: git.Commit{Hash: "abc1234", Subject: "first", AuthorTime: time.Now()}},
	}})
	m = updated.(Model)
	updated, _ = m.Update(refsLoadedMsg{refs: []git.Ref{
		{FullName: "refs/heads/main", ShortName: "main", Kind: git.RefKindLocal},
	}})
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'F'}})
	m = updated.(Model)

	updated, cmd := m.Update(fetchSucceededMsg{})
	m = updated.(Model)
	if m.fetchInFlight {
		t.Error("fetchInFlight should clear after success")
	}
	if m.status != "fetch: done" {
		t.Errorf("status = %q, want fetch: done", m.status)
	}
	if cmd == nil {
		t.Fatal("fetchSucceededMsg should batch a refs+log reload cmd")
	}
	if m.graph.loaded {
		t.Error("graph.loaded should reset on fetch success")
	}
	if m.refs.loaded {
		t.Error("refs.loaded should reset on fetch success")
	}
}

func TestModelCommitSelectedDispatchesDebounce(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	priorReqID := m.diffReqID
	updated, cmd := m.Update(commitSelectedMsg{hash: "aaa1111"})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("commitSelectedMsg should return a batched debounce + commit-detail cmd")
	}
	if m.diffReqID != priorReqID+1 {
		t.Errorf("diffReqID should advance by 1, got %d (was %d)", m.diffReqID, priorReqID)
	}
	if !m.changes.loadingFiles {
		t.Error("changes pane should be marked loading after commitSelectedMsg")
	}
	if m.changes.hash != "aaa1111" {
		t.Errorf("changes.hash = %q, want aaa1111", m.changes.hash)
	}
	if !m.commitDetail.loading {
		t.Error("commitDetail should be marked loading after commitSelectedMsg")
	}
	if m.commitDetail.hash != "aaa1111" {
		t.Errorf("commitDetail.hash = %q, want aaa1111", m.commitDetail.hash)
	}
}

func TestModelDebounceMsgStaleReqIDIsDropped(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	// Simulate that two cursor moves happened before the first tick fires.
	m.diffReqID = 5

	_, cmd := m.Update(diffDebounceMsg{reqID: 3, hash: "stale"})
	if cmd != nil {
		t.Errorf("stale debounce tick should return no cmd, got %v", cmd)
	}
}

func TestModelDebounceMsgFreshReqIDDispatches(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.diffReqID = 5

	_, cmd := m.Update(diffDebounceMsg{reqID: 5, hash: "abc1234"})
	if cmd == nil {
		t.Fatal("fresh debounce tick should dispatch loadDiffStatCmd")
	}
}

func TestModelDKeyOpensDiffWindow(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	updated, _ = m.Update(commitsLoadedMsg{rows: []graphRow{
		{commit: git.Commit{Hash: "aaa1111", Subject: "first", AuthorTime: time.Now()}},
	}})
	m = updated.(Model)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m = updated.(Model)
	if m.mode != viewModeDiffWindow {
		t.Errorf("mode after d = %v, want viewModeDiffWindow", m.mode)
	}
	if cmd == nil {
		t.Fatal("d should dispatch loadDiffPatchCmd")
	}
	if !m.diff.loadingPatch {
		t.Error("d should mark patch loading")
	}
	if m.diff.currentHash != "aaa1111" {
		t.Errorf("diff.currentHash = %q, want aaa1111", m.diff.currentHash)
	}
}

func TestModelDKeyWithoutSelectionIsNoop(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	// No commits loaded — Selected() returns false.
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m = updated.(Model)
	if m.mode != viewModeNormal {
		t.Errorf("d without selection should not flip viewMode, got %v", m.mode)
	}
	if cmd != nil {
		t.Error("d without selection should not dispatch a cmd")
	}
}

func TestModelEscClosesDiffWindow(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.mode = viewModeDiffWindow

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.mode != viewModeNormal {
		t.Errorf("esc should return to normal mode, got %v", m.mode)
	}
	if cmd != nil {
		t.Errorf("esc should not dispatch a cmd, got %v", cmd)
	}
}

func TestModelQClosesDiffWindowWithoutQuitting(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.mode = viewModeDiffWindow

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	m = updated.(Model)
	if m.mode != viewModeNormal {
		t.Errorf("q in diff window should return to normal mode, got %v", m.mode)
	}
	if cmd != nil {
		// tea.Quit is a non-nil cmd — its presence here would mean the app
		// quits when the user just wanted to close the overlay.
		t.Errorf("q in diff window must not dispatch tea.Quit, got cmd=%v", cmd)
	}
}

func TestModelStaleStatLoadedIsIgnored(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	// Pretend two cursor moves happened; the latest reqID is 7 and changes
	// is loading for hash "current". A stale response at reqID 3 must not
	// mutate the file list.
	m.diffReqID = 7
	m.changes.MarkPending("current")

	updated, _ = m.Update(diffStatLoadedMsg{reqID: 3, hash: "old", files: []git.FileStat{
		{Path: "stale.txt", Insertions: 1},
	}})
	m = updated.(Model)
	if len(m.changes.files) != 0 {
		t.Errorf("stale diffStatLoadedMsg must not populate changes.files, got %v", m.changes.files)
	}
	if !m.changes.loadingFiles {
		t.Error("stale response should leave loadingFiles=true since the in-flight call is still pending")
	}
}

func TestModelRKeyCancelsPreviousStream(t *testing.T) {
	m := New()
	cancelled := false
	m.streamCancel = func() { cancelled = true }
	prevReqID := m.streamReqID

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = updated.(Model)

	if !cancelled {
		t.Error("r should cancel any in-flight stream before issuing a new load")
	}
	if m.streamCancel != nil {
		t.Errorf("streamCancel should be cleared after cancel, got %v", m.streamCancel)
	}
	if m.streamReqID == prevReqID {
		t.Errorf("streamReqID should advance on r, stayed at %d", m.streamReqID)
	}
}

func TestModelStaleStreamBatchDropped(t *testing.T) {
	m := New()
	m.streamReqID = 5

	updated, _ := m.Update(commitsAppendedMsg{
		reqID: 4,
		rows: []graphRow{
			{commit: git.Commit{Hash: "stale1", Subject: "stale", AuthorTime: time.Now()}},
		},
	})
	m = updated.(Model)

	if items := m.graph.list.Items(); len(items) != 0 {
		t.Errorf("stale (reqID=4) batch should not merge; got %d items", len(items))
	}
}

func TestModelStaleStreamStartedCancelsImmediately(t *testing.T) {
	m := New()
	m.streamReqID = 5
	cancelled := false

	updated, _ := m.Update(commitsStreamStartedMsg{
		reqID:  4,
		cancel: func() { cancelled = true },
		next:   nil,
	})
	m = updated.(Model)

	if !cancelled {
		t.Error("stale commitsStreamStartedMsg should be cancelled on arrival to release its git process")
	}
	if m.streamCancel != nil {
		t.Errorf("streamCancel should not be set from a stale started msg, got %v", m.streamCancel)
	}
}

func TestModelQuitCancelsStream(t *testing.T) {
	m := New()
	cancelled := false
	m.streamCancel = func() { cancelled = true }

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})

	if !cancelled {
		t.Error("q should cancel in-flight stream before quitting so the git process is reaped")
	}
	if cmd == nil {
		t.Fatal("q should return a non-nil cmd (tea.Quit)")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("q cmd should produce tea.QuitMsg, got %T", cmd())
	}
}

func TestModelFetchFailedSurfacesError(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	updated, _ = m.Update(commitsLoadedMsg{rows: []graphRow{
		{commit: git.Commit{Hash: "abc1234", Subject: "first", AuthorTime: time.Now()}},
	}})
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'F'}})
	m = updated.(Model)

	updated, cmd := m.Update(fetchFailedMsg{err: errors.New("git fetch: exit status 128: could not resolve host github.com")})
	m = updated.(Model)
	if m.fetchInFlight {
		t.Error("fetchInFlight should clear on failure")
	}
	if !strings.Contains(m.status, "could not resolve host") {
		t.Errorf("status %q should include stderr", m.status)
	}
	if cmd != nil {
		t.Error("fetch failure should not auto-reload")
	}
	if !m.graph.loaded {
		t.Error("graph.loaded should remain on fetch failure")
	}
}
