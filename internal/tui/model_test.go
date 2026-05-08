package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

func TestPaneSizesShrinkWhenHelpExpanded(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	normal := m.paneSizes()
	m.mode = viewModeHelp
	expanded := m.paneSizes()

	wantDelta := helpExpandedHeight - 1
	gotDelta := (normal.graphH + normal.tabH) - (expanded.graphH + expanded.tabH)
	if gotDelta != wantDelta {
		t.Errorf("graph+tab delta on viewModeHelp = %d, want %d (helpExpandedHeight - 1)",
			gotDelta, wantDelta)
	}
}

func TestHelpToggleEntersAndExitsMode(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m = updated.(Model)
	if m.mode != viewModeHelp {
		t.Fatalf("? should enter viewModeHelp, got %v", m.mode)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m = updated.(Model)
	if m.mode != viewModeNormal {
		t.Errorf("second ? should exit viewModeHelp, got %v", m.mode)
	}
}

func TestHelpModeKeysPassThrough(t *testing.T) {
	// The expanded panel is a reference, not a modal — shortcuts must keep
	// working while it is open so a user can act on what they read. tab
	// cycles focus, F dispatches a fetch, q quits.
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.mode = viewModeHelp
	startFocus := m.focused

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	if m.focused == startFocus {
		t.Errorf("tab in help mode should still cycle focus, got %v", m.focused)
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'F'}})
	m = updated.(Model)
	if !m.fetchInFlight {
		t.Errorf("F in help mode should still dispatch fetch")
	}
	if cmd == nil {
		t.Errorf("F in help mode should return a fetchCmd, got nil")
	}

	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Errorf("q in help mode should still quit (tea.Quit), got nil cmd")
	}
}

func TestHelpModeIgnoredInDiffWindow(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.mode = viewModeDiffWindow

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m = updated.(Model)
	if m.mode != viewModeDiffWindow {
		t.Errorf("? in diff overlay should be swallowed; mode = %v", m.mode)
	}
}

func TestHelpModeIgnoredInCheckoutConfirm(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.mode = viewModeCheckoutConfirm
	m.pendingCheckout = pendingCheckout{ref: "feat"}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m = updated.(Model)
	if m.mode != viewModeCheckoutConfirm {
		t.Errorf("? in dirty-tree confirm should be swallowed; mode = %v", m.mode)
	}
}

func TestHelpToggleClosesViaSecondQuestionMark(t *testing.T) {
	// `?` is a single binding that flips the panel both ways — the panel
	// is a reference, not a modal, so a second `?` press must close it.
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.mode = viewModeHelp

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m = updated.(Model)
	if m.mode != viewModeNormal {
		t.Errorf("? in help mode should close panel, got mode=%v", m.mode)
	}
}

func TestRenderHelpStatusReturnsPaneHint(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	m.focused = paneRefs
	if got := m.renderHelpStatus(); !strings.Contains(got, "enter checkout") {
		t.Errorf("paneRefs hint missing 'enter checkout': %q", got)
	}

	m.focused = paneGraph
	if got := m.renderHelpStatus(); !strings.Contains(got, "C detach") {
		t.Errorf("paneGraph hint missing 'C detach': %q", got)
	}

	m.focused = paneTab
	if got := m.renderHelpStatus(); !strings.Contains(got, "y copy") {
		t.Errorf("paneTab hint missing 'y copy': %q", got)
	}
}

func TestRenderHelpStatusInHelpModeReturnsPanel(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.mode = viewModeHelp

	got := m.renderHelpStatus()
	for _, want := range []string{"[Global]", "[Refs]", "[Graph]", "[Tab]"} {
		if !strings.Contains(got, want) {
			t.Errorf("help-mode renderHelpStatus missing %q\n--- panel ---\n%s", want, got)
		}
	}
}

func TestRenderHelpStatusStatusOverridesHint(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.focused = paneGraph
	m.status = "fetching…"
	m.statusStyle = statusBusyS

	got := m.renderHelpStatus()
	if !strings.Contains(got, "fetching…") {
		t.Errorf("rendered line missing status: %q", got)
	}
	if !strings.Contains(got, "C detach") {
		t.Errorf("rendered line should still carry the hint alongside status: %q", got)
	}
}

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

func TestTabPaneArrows_TogglesCommitChanges(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.focused = paneTab
	if m.tabs.Active() != tabCommit {
		t.Fatalf("tabs default = %v, want tabCommit", m.tabs.Active())
	}

	send := func(kt tea.KeyType) Model {
		updated, _ := m.Update(tea.KeyMsg{Type: kt})
		return updated.(Model)
	}

	m = send(tea.KeyRight)
	if m.tabs.Active() != tabChanges {
		t.Errorf("after → on paneTab, active = %v, want tabChanges", m.tabs.Active())
	}
	m = send(tea.KeyRight)
	if m.tabs.Active() != tabCommit {
		t.Errorf("after → #2 (wrap), active = %v, want tabCommit", m.tabs.Active())
	}
	m = send(tea.KeyLeft)
	if m.tabs.Active() != tabChanges {
		t.Errorf("after ← on paneTab, active = %v, want tabChanges (wrap reverse)", m.tabs.Active())
	}
}

func TestRefsGraphTabKeys_NoOp(t *testing.T) {
	sendRune := func(m Model, r rune) Model {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		return updated.(Model)
	}
	sendKey := func(m Model, kt tea.KeyType) Model {
		updated, _ := m.Update(tea.KeyMsg{Type: kt})
		return updated.(Model)
	}

	for _, focus := range []pane{paneRefs, paneGraph} {
		m := New()
		updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
		m = updated.(Model)
		m.focused = focus
		startActive := m.tabs.Active()

		for _, key := range []rune{'h', 'l'} {
			m = sendRune(m, key)
			if m.focused != focus {
				t.Errorf("h/l from %v moved focus to %v, want unchanged", focus, m.focused)
			}
			if m.tabs.Active() != startActive {
				t.Errorf("h/l from %v changed tab to %v, want %v", focus, m.tabs.Active(), startActive)
			}
		}

		for _, kt := range []tea.KeyType{tea.KeyLeft, tea.KeyRight} {
			m = sendKey(m, kt)
			if m.focused != focus {
				t.Errorf("←/→ from %v moved focus to %v, want unchanged", focus, m.focused)
			}
			if m.tabs.Active() != startActive {
				t.Errorf("←/→ from %v changed tab to %v, want %v", focus, m.tabs.Active(), startActive)
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
		{"left", tea.KeyMsg{Type: tea.KeyLeft}},
		{"right", tea.KeyMsg{Type: tea.KeyRight}},
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
	updated, _ = m.Update(commitsAppendedMsg{reqID: 1, done: true, rows: []graphRow{
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

	updated, _ = m.Update(commitsAppendedMsg{reqID: 1, done: true, rows: []graphRow{
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

	updated, _ = m.Update(commitsAppendedMsg{reqID: 1, done: true, rows: []graphRow{
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
	updated, _ = m.Update(commitsAppendedMsg{reqID: 1, done: true, rows: []graphRow{
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

	updated, _ = m.Update(commitsAppendedMsg{reqID: 1, done: true, rows: []graphRow{
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

func TestModelPKeyDispatchesPull(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'P'}})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("P should return a pullCmd")
	}
	if !m.pullInFlight {
		t.Error("P should set pullInFlight=true")
	}
	if m.status != "pulling…" {
		t.Errorf("status = %q, want pulling…", m.status)
	}

	updated, cmd2 := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'P'}})
	m = updated.(Model)
	if cmd2 != nil {
		t.Error("second P should not dispatch a parallel pull")
	}
	if m.status != "pulling…" {
		t.Errorf("status should still be pulling…, got %q", m.status)
	}
}

func TestModelPullSucceededReloadsAndJumpsHEAD(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'P'}})
	m = updated.(Model)

	updated, cmd := m.Update(pullSucceededMsg{})
	m = updated.(Model)
	if m.pullInFlight {
		t.Error("pullInFlight should clear after success")
	}
	if m.status != "pull: done" {
		t.Errorf("status = %q, want pull: done", m.status)
	}
	if m.pendingHEADHash != pendingHEADSentinel {
		t.Errorf("pullSucceededMsg should arm pendingHEADHash with sentinel, got %q", m.pendingHEADHash)
	}
	if cmd == nil {
		t.Fatal("pullSucceededMsg should batch a refs+log reload cmd")
	}

	postReqID := m.streamReqID

	updated, _ = m.Update(refsLoadedMsg{refs: []git.Ref{
		{FullName: "refs/heads/main", ShortName: "main", Kind: git.RefKindLocal, ObjectName: "deadbee", IsHead: true},
	}})
	m = updated.(Model)
	// Before the matching commit streams in, the hash is captured but the
	// jump can't land yet — so pendingHEADHash holds the real hash.
	if m.pendingHEADHash != "deadbee" {
		t.Errorf("pendingHEADHash = %q, want deadbee", m.pendingHEADHash)
	}

	updated, _ = m.Update(commitsAppendedMsg{reqID: postReqID, done: true, rows: []graphRow{
		{commit: git.Commit{Hash: "cafebab", Subject: "first", AuthorTime: time.Now()}},
		{commit: git.Commit{Hash: "deadbee", Subject: "head", AuthorTime: time.Now()}},
	}})
	m = updated.(Model)
	updated, _ = m.Update(commitsStreamDoneMsg{reqID: postReqID})
	m = updated.(Model)

	if m.pendingHEADHash != "" {
		t.Errorf("pendingHEADHash should clear after the row is found, got %q", m.pendingHEADHash)
	}
	c, ok := m.graph.Selected()
	if !ok {
		t.Fatal("graph should have a selected row after HEAD jump")
	}
	if c.Hash != "deadbee" {
		t.Errorf("selected hash = %q, want deadbee (HEAD)", c.Hash)
	}
}

func TestModelPullConflictSurfacesMessage(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'P'}})
	m = updated.(Model)

	updated, cmd := m.Update(pullConflictMsg{err: errors.New("git pull: pull conflict: CONFLICT (content): Merge conflict in foo.go")})
	m = updated.(Model)
	if m.pullInFlight {
		t.Error("pullInFlight should clear on conflict")
	}
	if !strings.Contains(m.status, "CONFLICT") {
		t.Errorf("status %q should mention CONFLICT", m.status)
	}
	if !strings.Contains(m.status, "resolve") {
		t.Errorf("status %q should hint to resolve in terminal", m.status)
	}
	if m.statusStyle.GetForeground() != statusErrS.GetForeground() {
		t.Error("conflict status should use error style")
	}
	if cmd == nil {
		t.Error("pullConflictMsg should still trigger a refs+log reload")
	}
	if m.pendingHEADHash != "" {
		t.Errorf("conflict should not arm a HEAD jump — user is mid-merge, got pendingHEADHash=%q", m.pendingHEADHash)
	}
}

func TestModelPullFailedSurfacesError(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'P'}})
	m = updated.(Model)

	updated, cmd := m.Update(pullFailedMsg{err: errors.New("git pull: exit status 128: could not resolve host github.com")})
	m = updated.(Model)
	if m.pullInFlight {
		t.Error("pullInFlight should clear on failure")
	}
	if !strings.Contains(m.status, "could not resolve host") {
		t.Errorf("status %q should include stderr", m.status)
	}
	if cmd != nil {
		t.Error("pull failure should not auto-reload")
	}
	if m.pendingHEADHash != "" {
		t.Errorf("failure should not arm a HEAD jump, got pendingHEADHash=%q", m.pendingHEADHash)
	}
}

func TestModelPullAndFetchConcurrent(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'F'}})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'P'}})
	m = updated.(Model)
	if !m.fetchInFlight || !m.pullInFlight {
		t.Fatalf("both flags should be true after F+P, got fetch=%v pull=%v", m.fetchInFlight, m.pullInFlight)
	}
	if m.status != "pulling…" {
		t.Errorf("status = %q, want pulling… (P should overwrite fetching…)", m.status)
	}

	updated, cmd := m.Update(fetchSucceededMsg{})
	m = updated.(Model)
	if m.fetchInFlight {
		t.Error("fetchInFlight should clear after fetchSucceededMsg")
	}
	if m.status != "pulling…" {
		t.Errorf("status = %q, want pulling… preserved while pull is still in flight", m.status)
	}
	if cmd != nil {
		t.Error("fetch success during pull-in-flight should skip its reload — pull's own reload will run")
	}
}

func TestModelPullPrefStrategyPropagatesToCmd(t *testing.T) {
	prevResolve := pullResolveStrategy
	prevExec := pullExec
	defer func() {
		pullResolveStrategy = prevResolve
		pullExec = prevExec
	}()
	var seenDir, seenPrefs string
	pullResolveStrategy = func(_ context.Context, dir, prefs string) (git.PullStrategy, error) {
		seenDir = dir
		seenPrefs = prefs
		return git.PullStrategyFFOnly, nil
	}
	pullExec = func(context.Context, string, git.PullStrategy) error { return nil }

	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.pullPrefStrategy = "rebase"

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'P'}})
	_ = updated.(Model)
	if cmd == nil {
		t.Fatal("P should return a pullCmd")
	}
	// Invoke the cmd so the stubbed pullResolveStrategy runs.
	_ = cmd()

	if seenPrefs != "rebase" {
		t.Errorf("pullResolveStrategy got prefs=%q, want rebase", seenPrefs)
	}
	if seenDir != "" {
		t.Errorf("pullResolveStrategy got dir=%q, want empty (cwd)", seenDir)
	}
}

func TestModelFetchSucceededReloadsBothPanes(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	updated, _ = m.Update(commitsAppendedMsg{reqID: 1, done: true, rows: []graphRow{
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
	updated, _ = m.Update(commitsAppendedMsg{reqID: 1, done: true, rows: []graphRow{
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

func TestModelFetchFailedSurfacesError(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	updated, _ = m.Update(commitsAppendedMsg{reqID: 1, done: true, rows: []graphRow{
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

// loadCommitDetailFixture installs a long body into the Commit-tab viewport
// so paneTab dispatch tests have something to scroll. Mirrors the metadata
// + body shape of a real ApplyDetailLoaded call but bypasses the git command
// since these tests run without a repo.
func loadCommitDetailFixture(t *testing.T, m *Model) {
	t.Helper()
	m.commitDetail.MarkLoading("hash", 1)
	m.commitDetail.ApplyDetailLoaded(1, "hash", git.Detail{
		Hash: "hash",
		Body: strings.Repeat("body line\n", 60),
	})
}

func TestPaneTabCommit_JKScrollsBodyViewport(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.focused = paneTab
	loadCommitDetailFixture(t, &m)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = updated.(Model)
	if m.commitDetail.viewport.YOffset == 0 {
		t.Errorf("j on Commit tab must scroll the body viewport, YOffset still 0")
	}
}

func TestPaneTabCommit_TabTogglePreservesScroll(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.focused = paneTab
	loadCommitDetailFixture(t, &m)

	// Scroll a few lines down on the Commit tab.
	for i := 0; i < 3; i++ {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		m = updated.(Model)
	}
	scrolled := m.commitDetail.viewport.YOffset
	if scrolled == 0 {
		t.Fatalf("setup: j×3 should have advanced the viewport")
	}
	// Toggle to Changes and back to Commit — the Commit-tab viewport state must
	// survive the round trip (each tab keeps its own offset).
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	m = updated.(Model)
	if m.tabs.Active() != tabChanges {
		t.Fatalf("l should switch to Changes")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	m = updated.(Model)
	if m.tabs.Active() != tabCommit {
		t.Fatalf("h should switch back to Commit")
	}
	if m.commitDetail.viewport.YOffset != scrolled {
		t.Errorf("scroll position lost across tab toggle: got %d, want %d",
			m.commitDetail.viewport.YOffset, scrolled)
	}
}

func TestPaneTabCommit_CtrlDIsNoOp(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.focused = paneTab
	if m.tabs.Active() != tabCommit {
		t.Fatalf("setup: tab default = %v, want tabCommit", m.tabs.Active())
	}
	loadCommitDetailFixture(t, &m)

	// ctrl+d/ctrl+u stay reserved for the Changes-tab patch viewport — the
	// Commit tab must not consume them, so YOffset stays at 0.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = updated.(Model)
	if m.commitDetail.viewport.YOffset != 0 {
		t.Errorf("ctrl+d on Commit tab must be a no-op, YOffset = %d", m.commitDetail.viewport.YOffset)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	m = updated.(Model)
	if m.commitDetail.viewport.YOffset != 0 {
		t.Errorf("ctrl+u on Commit tab must be a no-op, YOffset = %d", m.commitDetail.viewport.YOffset)
	}
}

func stubCheckout(t *testing.T) (
	getRef func() (string, bool),
	getStash func() (string, bool),
	getDetached func() (string, bool),
) {
	t.Helper()
	prevC, prevCD, prevS := checkoutExec, checkoutDetachedExec, stashExec
	t.Cleanup(func() {
		checkoutExec = prevC
		checkoutDetachedExec = prevCD
		stashExec = prevS
	})
	var ref, stashMsg, detachedRef string
	var refSet, stashSet, detachedSet bool
	checkoutExec = func(_ context.Context, _, r string) error {
		ref, refSet = r, true
		return nil
	}
	checkoutDetachedExec = func(_ context.Context, _, r string) error {
		detachedRef, detachedSet = r, true
		return nil
	}
	stashExec = func(_ context.Context, _, m string) error {
		stashMsg, stashSet = m, true
		return nil
	}
	return func() (string, bool) { return ref, refSet },
		func() (string, bool) { return stashMsg, stashSet },
		func() (string, bool) { return detachedRef, detachedSet }
}

func TestModelRefCheckoutEnterDispatchesLocalShortName(t *testing.T) {
	getRef, _, _ := stubCheckout(t)
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	updated, cmd := m.Update(refCheckoutRequestedMsg{ref: git.Ref{
		ShortName: "feat/foo",
		FullName:  "refs/heads/feat/foo",
		Kind:      git.RefKindLocal,
	}})
	m = updated.(Model)

	if !m.checkoutInFlight {
		t.Error("checkoutInFlight should latch on refCheckoutRequestedMsg")
	}
	if m.pendingCheckout.ref != "feat/foo" || m.pendingCheckout.detached {
		t.Errorf("pendingCheckout = %+v, want {feat/foo false}", m.pendingCheckout)
	}
	if cmd == nil {
		t.Fatal("refCheckoutRequestedMsg should return a checkoutCmd")
	}
	_ = cmd()
	if got, ok := getRef(); !ok || got != "feat/foo" {
		t.Errorf("checkoutExec ref = %q ok=%v, want feat/foo", got, ok)
	}
}

func TestModelRefCheckoutEnterStripsRemotePrefix(t *testing.T) {
	getRef, _, _ := stubCheckout(t)
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	updated, cmd := m.Update(refCheckoutRequestedMsg{ref: git.Ref{
		ShortName: "origin/feat",
		FullName:  "refs/remotes/origin/feat",
		Kind:      git.RefKindRemote,
	}})
	m = updated.(Model)

	if cmd == nil {
		t.Fatal("expected a checkoutCmd")
	}
	_ = cmd()
	got, ok := getRef()
	if !ok || got != "feat" {
		t.Errorf("checkoutExec ref = %q ok=%v, want %q (dwim DWIM)", got, ok, "feat")
	}
	if m.pendingCheckout.ref != "feat" {
		t.Errorf("pendingCheckout.ref = %q, want feat", m.pendingCheckout.ref)
	}
}

func TestModelCheckoutInFlightGate(t *testing.T) {
	stubCheckout(t)
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.checkoutInFlight = true

	updated, cmd := m.Update(refCheckoutRequestedMsg{ref: git.Ref{
		ShortName: "feat", Kind: git.RefKindLocal,
	}})
	m = updated.(Model)
	if cmd != nil {
		t.Errorf("second Enter while checkoutInFlight should be a no-op, got cmd=%v", cmd)
	}
}

func TestModelCheckoutSucceededReloadsAndArmsHEAD(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.checkoutInFlight = true
	m.pendingCheckout = pendingCheckout{ref: "feat", detached: false}

	updated, cmd := m.Update(checkoutSucceededMsg{ref: "feat"})
	m = updated.(Model)

	if m.checkoutInFlight {
		t.Error("checkoutInFlight should clear after success")
	}
	if (m.pendingCheckout != pendingCheckout{}) {
		t.Errorf("pendingCheckout should clear, got %+v", m.pendingCheckout)
	}
	if m.status != "checkout: feat" {
		t.Errorf("status = %q, want checkout: feat", m.status)
	}
	if m.pendingHEADHash != pendingHEADSentinel {
		t.Errorf("pendingHEADHash = %q, want sentinel", m.pendingHEADHash)
	}
	if cmd == nil {
		t.Fatal("checkoutSucceededMsg should batch a reload cmd")
	}
}

func TestModelCheckoutSucceededDetachedFormatsHash(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	updated, _ = m.Update(checkoutSucceededMsg{ref: "abcdef1234567890", detached: true})
	m = updated.(Model)
	if !strings.Contains(m.status, "detached at") {
		t.Errorf("status = %q, want it to mention 'detached at'", m.status)
	}
}

func TestModelCheckoutNeedsCleanTreeEntersConfirmMode(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.checkoutInFlight = true
	m.pendingCheckout = pendingCheckout{ref: "feat"}

	updated, _ = m.Update(checkoutNeedsCleanTreeMsg{ref: "feat"})
	m = updated.(Model)

	if m.mode != viewModeCheckoutConfirm {
		t.Errorf("mode = %v, want viewModeCheckoutConfirm", m.mode)
	}
	if m.checkoutInFlight {
		t.Error("checkoutInFlight should release while modal owns the next decision")
	}
	if (m.pendingCheckout == pendingCheckout{}) {
		t.Error("pendingCheckout must be retained — modal 's' branch needs it")
	}
}

func TestModelCheckoutFailedSurfacesError(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.checkoutInFlight = true
	m.pendingCheckout = pendingCheckout{ref: "feat"}

	updated, _ = m.Update(checkoutFailedMsg{err: errors.New("ref vanished")})
	m = updated.(Model)

	if m.checkoutInFlight {
		t.Error("checkoutInFlight should clear on failure")
	}
	if !strings.Contains(m.status, "checkout failed") {
		t.Errorf("status = %q, want it to start with 'checkout failed'", m.status)
	}
}

func TestModelStashThenCheckoutMsgIncludesStashLabel(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	updated, cmd := m.Update(stashThenCheckoutMsg{ref: "feat", stashLabel: "stash@{0}"})
	m = updated.(Model)

	if !strings.Contains(m.status, "stash@{0}") {
		t.Errorf("status = %q, want it to surface the stash label", m.status)
	}
	if m.pendingHEADHash != pendingHEADSentinel {
		t.Errorf("pendingHEADHash = %q, want sentinel", m.pendingHEADHash)
	}
	if cmd == nil {
		t.Fatal("stashThenCheckoutMsg should also batch a reload cmd")
	}
}

func TestModelCheckoutConfirmStashKeyDispatches(t *testing.T) {
	_, getStash, _ := stubCheckout(t)
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.mode = viewModeCheckoutConfirm
	m.pendingCheckout = pendingCheckout{ref: "feat"}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m = updated.(Model)

	if m.mode != viewModeNormal {
		t.Errorf("mode after 's' = %v, want viewModeNormal", m.mode)
	}
	if !m.checkoutInFlight {
		t.Error("checkoutInFlight should re-arm on 's' (stashing…)")
	}
	if cmd == nil {
		t.Fatal("'s' should dispatch stashThenCheckoutCmd")
	}
	_ = cmd()
	if msg, ok := getStash(); !ok || !strings.Contains(msg, "feat") {
		t.Errorf("stashExec called with %q ok=%v, want a message mentioning the ref", msg, ok)
	}
}

func TestModelCheckoutConfirmAbortClears(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.mode = viewModeCheckoutConfirm
	m.pendingCheckout = pendingCheckout{ref: "feat"}

	for _, key := range []rune{'a'} {
		updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
		m = updated.(Model)
		if cmd != nil {
			t.Errorf("abort key %q should not return a cmd", string(key))
		}
		if m.mode != viewModeNormal {
			t.Errorf("mode after %q = %v, want viewModeNormal", string(key), m.mode)
		}
		if (m.pendingCheckout != pendingCheckout{}) {
			t.Errorf("pendingCheckout after abort = %+v, want zero", m.pendingCheckout)
		}
		if !strings.Contains(m.status, "aborted") {
			t.Errorf("status after abort = %q, want it to mention 'aborted'", m.status)
		}
	}
}

func TestModelCheckoutConfirmEscAlsoAborts(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.mode = viewModeCheckoutConfirm
	m.pendingCheckout = pendingCheckout{ref: "feat"}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.mode != viewModeNormal {
		t.Errorf("mode after esc = %v, want viewModeNormal", m.mode)
	}
}

func TestModelCheckoutConfirmSwallowsOtherKeys(t *testing.T) {
	stubCheckout(t)
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.mode = viewModeCheckoutConfirm
	m.pendingCheckout = pendingCheckout{ref: "feat"}

	for _, k := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'j'}},
		{Type: tea.KeyTab},
		{Type: tea.KeyRunes, Runes: []rune{'F'}},
		{Type: tea.KeyRunes, Runes: []rune{'P'}},
		{Type: tea.KeyRunes, Runes: []rune{'d'}},
		{Type: tea.KeyEnter},
	} {
		updated, cmd := m.Update(k)
		m = updated.(Model)
		if m.mode != viewModeCheckoutConfirm {
			t.Errorf("modal closed on swallowed key %v, mode = %v", k, m.mode)
		}
		if cmd != nil {
			t.Errorf("swallowed key %v should not return cmd, got %v", k, cmd)
		}
		if m.fetchInFlight || m.pullInFlight || m.checkoutInFlight {
			t.Errorf("swallowed key %v leaked into a dispatch (fetch=%v pull=%v checkout=%v)",
				k, m.fetchInFlight, m.pullInFlight, m.checkoutInFlight)
		}
	}
}

func TestModelGraphCDispatchesDetachedCheckout(t *testing.T) {
	_, _, getDetached := stubCheckout(t)
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.focused = paneGraph

	updated, _ = m.Update(commitsAppendedMsg{reqID: 1, done: true, rows: []graphRow{
		{commit: git.Commit{Hash: "abc1234", Subject: "first", AuthorTime: time.Now()}},
	}})
	m = updated.(Model)
	updated, _ = m.Update(commitsStreamDoneMsg{reqID: 1})
	m = updated.(Model)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'C'}})
	m = updated.(Model)

	if !m.checkoutInFlight {
		t.Error("'C' should latch checkoutInFlight")
	}
	if !m.pendingCheckout.detached {
		t.Errorf("pendingCheckout = %+v, want detached=true", m.pendingCheckout)
	}
	if m.pendingCheckout.ref != "abc1234" {
		t.Errorf("pendingCheckout.ref = %q, want abc1234 (graph hash)", m.pendingCheckout.ref)
	}
	if cmd == nil {
		t.Fatal("'C' should return a checkoutCmd")
	}
	_ = cmd()
	if got, ok := getDetached(); !ok || got != "abc1234" {
		t.Errorf("checkoutDetachedExec ref = %q ok=%v, want abc1234", got, ok)
	}
}

func TestModelRefCheckoutWithPullDispatchesChainForLocalUpstream(t *testing.T) {
	withChainStubs(t, chainStubs{
		checkout:    func(context.Context, string, string) error { return nil },
		pullResolve: noopPullResolveFFOnly,
		pull:        func(context.Context, string, git.PullStrategy) error { return nil },
	})
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	updated, cmd := m.Update(refCheckoutWithPullRequestedMsg{ref: git.Ref{
		ShortName: "feat",
		FullName:  "refs/heads/feat",
		Kind:      git.RefKindLocal,
		Upstream:  "origin/feat",
	}})
	m = updated.(Model)

	if !m.checkoutInFlight {
		t.Error("checkoutInFlight should latch on refCheckoutWithPullRequestedMsg")
	}
	if !m.pendingCheckout.withPull {
		t.Errorf("pendingCheckout.withPull = false, want true; got %+v", m.pendingCheckout)
	}
	if m.pendingCheckout.skipPull {
		t.Errorf("local with upstream should not skip pull; got %+v", m.pendingCheckout)
	}
	if cmd == nil {
		t.Fatal("p should return a chain cmd")
	}
	got := cmd().(checkoutThenPullSucceededMsg)
	if got.pullSkipped {
		t.Error("chain should run pull (skipPull=false) for local with upstream")
	}
}

func TestModelRefCheckoutWithPullSkipsPullForTag(t *testing.T) {
	var puRan bool
	withChainStubs(t, chainStubs{
		checkout: func(context.Context, string, string) error { return nil },
		pull: func(context.Context, string, git.PullStrategy) error {
			puRan = true
			return nil
		},
	})
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	updated, cmd := m.Update(refCheckoutWithPullRequestedMsg{ref: git.Ref{
		ShortName: "v1.0",
		FullName:  "refs/tags/v1.0",
		Kind:      git.RefKindTag,
	}})
	m = updated.(Model)

	if !m.pendingCheckout.skipPull {
		t.Errorf("tag should skipPull, got %+v", m.pendingCheckout)
	}
	if m.pendingCheckout.skipReason != "tag has no upstream" {
		t.Errorf("skipReason = %q, want 'tag has no upstream'", m.pendingCheckout.skipReason)
	}
	if !strings.Contains(m.status, "pull skipped") {
		t.Errorf("status %q should mention pull skipped", m.status)
	}
	if cmd == nil {
		t.Fatal("p should return a chain cmd")
	}
	_ = cmd()
	if puRan {
		t.Error("pull must not run for tag refs")
	}
}

func TestModelRefCheckoutWithPullSkipsPullForUpstreamlessLocal(t *testing.T) {
	var puRan bool
	withChainStubs(t, chainStubs{
		checkout: func(context.Context, string, string) error { return nil },
		pull: func(context.Context, string, git.PullStrategy) error {
			puRan = true
			return nil
		},
	})
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	updated, cmd := m.Update(refCheckoutWithPullRequestedMsg{ref: git.Ref{
		ShortName: "wip",
		FullName:  "refs/heads/wip",
		Kind:      git.RefKindLocal,
		Upstream:  "",
	}})
	m = updated.(Model)

	if !m.pendingCheckout.skipPull {
		t.Errorf("upstream-less local should skipPull, got %+v", m.pendingCheckout)
	}
	if m.pendingCheckout.skipReason != "local branch has no upstream" {
		t.Errorf("skipReason = %q", m.pendingCheckout.skipReason)
	}
	if cmd == nil {
		t.Fatal("p should return a chain cmd")
	}
	_ = cmd()
	if puRan {
		t.Error("pull must not run for upstream-less local")
	}
}

func TestModelRefCheckoutWithPullRemoteIsEligible(t *testing.T) {
	var puRan bool
	withChainStubs(t, chainStubs{
		checkout:    func(context.Context, string, string) error { return nil },
		pullResolve: noopPullResolveFFOnly,
		pull: func(context.Context, string, git.PullStrategy) error {
			puRan = true
			return nil
		},
	})
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	updated, cmd := m.Update(refCheckoutWithPullRequestedMsg{ref: git.Ref{
		ShortName: "origin/feat",
		FullName:  "refs/remotes/origin/feat",
		Kind:      git.RefKindRemote,
	}})
	m = updated.(Model)

	// dwim: remote-tracking ref → local "feat".
	if m.pendingCheckout.ref != "feat" {
		t.Errorf("pendingCheckout.ref = %q, want feat (dwim)", m.pendingCheckout.ref)
	}
	if m.pendingCheckout.skipPull {
		t.Error("remote-tracking ref should be pull-eligible (dwim creates local with upstream)")
	}
	if cmd == nil {
		t.Fatal("p should return a chain cmd")
	}
	_ = cmd()
	if !puRan {
		t.Error("pull should run after dwim checkout of a remote-tracking ref")
	}
}

func TestModelCheckoutThenPullSucceededClearsPendingAndJumpsHEAD(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.checkoutInFlight = true
	m.pendingCheckout = pendingCheckout{ref: "feat", withPull: true}

	updated, cmd := m.Update(checkoutThenPullSucceededMsg{ref: "feat"})
	m = updated.(Model)

	if m.checkoutInFlight {
		t.Error("checkoutInFlight should clear")
	}
	if (m.pendingCheckout != pendingCheckout{}) {
		t.Errorf("pendingCheckout should clear, got %+v", m.pendingCheckout)
	}
	if !strings.Contains(m.status, "pull: done") {
		t.Errorf("status = %q, want it to mention 'pull: done'", m.status)
	}
	if m.pendingHEADHash != pendingHEADSentinel {
		t.Errorf("pendingHEADHash = %q, want sentinel", m.pendingHEADHash)
	}
	if cmd == nil {
		t.Fatal("checkoutThenPullSucceededMsg should batch a reload cmd")
	}
}

func TestModelCheckoutThenPullSucceededWithSkipShowsReason(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	updated, _ = m.Update(checkoutThenPullSucceededMsg{
		ref:         "v1.0",
		pullSkipped: true,
		skipReason:  "tag has no upstream",
	})
	m = updated.(Model)
	if !strings.Contains(m.status, "pull skipped") || !strings.Contains(m.status, "tag has no upstream") {
		t.Errorf("status = %q, want skip reason surfaced", m.status)
	}
}

func TestModelCheckoutThenPullConflictSurfacesAndReloadsWithoutJump(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.checkoutInFlight = true
	m.pendingCheckout = pendingCheckout{ref: "feat", withPull: true}

	updated, cmd := m.Update(checkoutThenPullConflictMsg{
		ref: "feat",
		err: errors.New("git pull: pull conflict: CONFLICT (content)"),
	})
	m = updated.(Model)

	if m.checkoutInFlight {
		t.Error("checkoutInFlight should clear on chain conflict")
	}
	if !strings.Contains(m.status, "checked out feat") && !strings.Contains(m.status, "checkout: feat") {
		t.Errorf("status = %q, should reference the ref", m.status)
	}
	if !strings.Contains(m.status, "CONFLICT") {
		t.Errorf("status = %q, should surface CONFLICT", m.status)
	}
	if m.pendingHEADHash != "" {
		t.Errorf("conflict should not arm HEAD jump, got pendingHEADHash=%q", m.pendingHEADHash)
	}
	if cmd == nil {
		t.Error("conflict should still reload refs+log")
	}
}

func TestModelStashChainSucceededShowsAllLabels(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.checkoutInFlight = true
	m.pendingCheckout = pendingCheckout{ref: "feat", withPull: true}

	updated, cmd := m.Update(stashThenCheckoutThenPullThenPopSucceededMsg{
		ref:        "feat",
		stashLabel: "stash@{0}",
	})
	m = updated.(Model)

	for _, want := range []string{"feat", "stashed", "pull", "popped", "stash@{0}"} {
		if !strings.Contains(m.status, want) {
			t.Errorf("status %q missing %q", m.status, want)
		}
	}
	if m.pendingHEADHash != pendingHEADSentinel {
		t.Errorf("pendingHEADHash = %q, want sentinel", m.pendingHEADHash)
	}
	if cmd == nil {
		t.Fatal("succeeded should reload")
	}
}

func TestModelStashChainSucceededWithSkipShowsReason(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	updated, _ = m.Update(stashThenCheckoutThenPullThenPopSucceededMsg{
		ref:         "v1.0",
		stashLabel:  "stash@{0}",
		pullSkipped: true,
		skipReason:  "tag has no upstream",
	})
	m = updated.(Model)
	for _, want := range []string{"pull skipped", "tag has no upstream", "stashed", "popped", "stash@{0}"} {
		if !strings.Contains(m.status, want) {
			t.Errorf("status %q missing %q", m.status, want)
		}
	}
}

func TestModelStashChainPullConflictPreservesNoJump(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	updated, cmd := m.Update(stashThenCheckoutThenPullThenPopConflictMsg{
		ref:        "feat",
		stashLabel: "stash@{0}",
		phase:      "pull",
		err:        errors.New("git pull: pull conflict: CONFLICT (content)"),
	})
	// Re-wrap in the proper sentinel chain so errors.Is matches.
	_ = updated
	m, _ = m, cmd

	conflictErr := fmt.Errorf("git pull: %w: CONFLICT (content)", git.ErrPullConflict)
	updated, cmd = m.Update(stashThenCheckoutThenPullThenPopConflictMsg{
		ref:        "feat",
		stashLabel: "stash@{0}",
		phase:      "pull",
		err:        conflictErr,
	})
	m = updated.(Model)

	if !strings.Contains(m.status, "CONFLICT") {
		t.Errorf("status = %q, should surface CONFLICT", m.status)
	}
	if !strings.Contains(m.status, "stash preserved") {
		t.Errorf("status = %q, should mention stash preserved", m.status)
	}
	if m.pendingHEADHash != "" {
		t.Errorf("pull conflict should not jump HEAD, got %q", m.pendingHEADHash)
	}
	if cmd == nil {
		t.Error("conflict should still reload")
	}
}

func TestModelStashChainPullFailureKeepsStashAndJumpsHEAD(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	updated, cmd := m.Update(stashThenCheckoutThenPullThenPopConflictMsg{
		ref:        "feat",
		stashLabel: "stash@{0}",
		phase:      "pull",
		err:        errors.New("could not resolve host github.com"),
	})
	m = updated.(Model)

	for _, want := range []string{"checkout: feat", "pull failed", "stash preserved", "stash@{0}"} {
		if !strings.Contains(m.status, want) {
			t.Errorf("status %q missing %q", m.status, want)
		}
	}
	// Generic pull failure → HEAD did move (checkout landed), pop didn't run.
	// Jumping the cursor to the new HEAD lets the user see where they are.
	if m.pendingHEADHash != pendingHEADSentinel {
		t.Errorf("generic pull failure: pendingHEADHash = %q, want sentinel", m.pendingHEADHash)
	}
	if cmd == nil {
		t.Error("should still reload after pull failure")
	}
}

func TestModelStashChainPopConflictSurfacesGuidance(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	updated, cmd := m.Update(stashThenCheckoutThenPullThenPopConflictMsg{
		ref:        "feat",
		stashLabel: "stash@{0}",
		phase:      "stash-pop",
		err:        errors.New("git stash pop: stash pop conflict: CONFLICT"),
	})
	m = updated.(Model)

	for _, want := range []string{"pull: done", "pop conflict", "git stash drop", "stash@{0}"} {
		if !strings.Contains(m.status, want) {
			t.Errorf("status %q missing %q", m.status, want)
		}
	}
	if m.pendingHEADHash != pendingHEADSentinel {
		t.Errorf("pop conflict: pendingHEADHash = %q, want sentinel", m.pendingHEADHash)
	}
	if cmd == nil {
		t.Error("pop conflict should still reload")
	}
}

func TestModelCheckoutConfirmStashKeyDispatchesChainWhenWithPull(t *testing.T) {
	var seq []string
	withChainStubs(t, chainStubs{
		checkout: func(context.Context, string, string) error {
			seq = append(seq, "checkout")
			return nil
		},
		stash: func(context.Context, string, string) error {
			seq = append(seq, "stash")
			return nil
		},
		stashPop: func(context.Context, string) error {
			seq = append(seq, "pop")
			return nil
		},
		pullResolve: noopPullResolveFFOnly,
		pull: func(context.Context, string, git.PullStrategy) error {
			seq = append(seq, "pull")
			return nil
		},
	})

	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.mode = viewModeCheckoutConfirm
	m.pendingCheckout = pendingCheckout{ref: "feat", withPull: true, skipPull: false}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m = updated.(Model)
	if m.mode != viewModeNormal {
		t.Errorf("mode after 's' = %v, want viewModeNormal", m.mode)
	}
	if !m.checkoutInFlight {
		t.Error("checkoutInFlight should re-arm")
	}
	if !strings.Contains(m.status, "pull") {
		t.Errorf("status = %q, want it to mention pull (chain busy)", m.status)
	}
	if cmd == nil {
		t.Fatal("'s' with withPull should dispatch the chain cmd")
	}
	_ = cmd()
	wantSeq := []string{"stash", "checkout", "pull", "pop"}
	if !slices.Equal(seq, wantSeq) {
		t.Errorf("chain seq = %v, want %v", seq, wantSeq)
	}
}

func TestModelCheckoutConfirmStashKeyFallsBackToLegacyWithoutPull(t *testing.T) {
	// pendingCheckout.withPull=false means the user reached the modal via
	// plain Enter (refCheckoutRequestedMsg). The legacy stash → checkout
	// path must keep its existing surface — pop is NOT auto-fired.
	var seq []string
	withChainStubs(t, chainStubs{
		checkout: func(context.Context, string, string) error {
			seq = append(seq, "checkout")
			return nil
		},
		stash: func(context.Context, string, string) error {
			seq = append(seq, "stash")
			return nil
		},
		stashPop: func(context.Context, string) error {
			seq = append(seq, "pop")
			return nil
		},
	})

	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.mode = viewModeCheckoutConfirm
	m.pendingCheckout = pendingCheckout{ref: "feat"}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	_ = updated.(Model)
	if cmd == nil {
		t.Fatal("'s' should still dispatch the legacy stash+checkout cmd")
	}
	_ = cmd()
	wantSeq := []string{"stash", "checkout"}
	if !slices.Equal(seq, wantSeq) {
		t.Errorf("legacy seq = %v, want %v (no pop)", seq, wantSeq)
	}
}

func TestModelCheckoutConfirmModalShowsPullVariant(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.mode = viewModeCheckoutConfirm
	m.pendingCheckout = pendingCheckout{ref: "feat", withPull: true}

	got := m.renderHelpStatus()
	for _, want := range []string{"checkout 'feat' and pull", "stash & checkout & pull"} {
		if !strings.Contains(got, want) {
			t.Errorf("modal text %q missing %q", got, want)
		}
	}
}

func TestModelGraphCNoOpWhenRefsFocused(t *testing.T) {
	stubCheckout(t)
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.focused = paneRefs

	updated, _ = m.Update(commitsAppendedMsg{reqID: 1, done: true, rows: []graphRow{
		{commit: git.Commit{Hash: "abc1234", Subject: "first", AuthorTime: time.Now()}},
	}})
	m = updated.(Model)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'C'}})
	m = updated.(Model)
	if cmd != nil {
		t.Errorf("'C' on refs pane should be a no-op, got cmd=%v", cmd)
	}
	if m.checkoutInFlight {
		t.Error("checkoutInFlight should not latch on refs-focused 'C'")
	}
}
