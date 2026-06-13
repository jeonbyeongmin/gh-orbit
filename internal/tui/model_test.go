package tui

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// TestPaneSizesShrinkWhenHelpExpanded: the `?` reference is an inline panel
// that grows out of the footer, so it reserves helpReservedRows() bottom rows
// and the graph shrinks by that much (minus the 1 row normal mode already
// reserves for the collapsed hint).
func TestPaneSizesShrinkWhenHelpExpanded(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	normal := m.paneSizes()
	m.mode = viewModeHelp
	expanded := m.paneSizes()

	wantDelta := m.helpReservedRows() - 1
	if gotDelta := normal.graphH - expanded.graphH; gotDelta != wantDelta {
		t.Errorf("graph delta on viewModeHelp = %d, want %d (helpReservedRows - 1)",
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
	// The inline panel is a reference, not a modal — shortcuts keep working
	// while it is open so the user can act on what they read. F dispatches a
	// fetch; a second `?` toggles the panel closed.
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.mode = viewModeHelp

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'F'}})
	m = updated.(Model)
	if !m.fetchInFlight {
		t.Errorf("F while help panel open should still dispatch fetch")
	}
	if cmd == nil {
		t.Errorf("F while help panel open should return a fetchCmd, got nil")
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m = updated.(Model)
	if m.mode != viewModeNormal {
		t.Errorf("second ? should toggle the panel closed to viewModeNormal, got %v", m.mode)
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

func TestRenderHelpStatusReturnsCollapsedHint(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	// The collapsed bottom line is a single `? help` token regardless of focus
	// — the full key reference grows out of the footer only while `?` is open.
	m.focused = paneGraph
	if got := m.renderHelpStatus(); !strings.Contains(got, "? help") {
		t.Errorf("collapsed bottom line missing '? help': %q", got)
	}
}

func TestHelpExpandedShowsColumns(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.mode = viewModeHelp

	// In help mode the bottom region IS the column reference (inline, not a
	// modal), so renderHelpStatus itself carries all three category titles.
	got := m.renderHelpStatus()
	for _, want := range []string{"Global", "Graph", "Local Changes"} {
		if !strings.Contains(got, want) {
			t.Errorf("help-mode bottom panel missing category title %q\n--- panel ---\n%s", want, got)
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
	if !strings.Contains(got, "? help") {
		t.Errorf("rendered line should still carry '? help' alongside status: %q", got)
	}
}

// loadGraphFixture seeds the graph with a single commit so Selected()
// returns true for y-copy tests.
func loadGraphFixture(t *testing.T, m Model, hash string) Model {
	t.Helper()
	updated, _ := m.Update(commitsAppendedMsg{reqID: 1, done: true, rows: []graphRow{
		{commit: git.Commit{Hash: hash, Subject: "subj", AuthorTime: time.Now()}},
	}})
	return updated.(Model)
}

func TestYKeyCopiesHashFromGraph(t *testing.T) {
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
	m = loadGraphFixture(t, m, "0123456789abcdef0123456789abcdef01234567")

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = updated.(Model)
	if captured != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("clipboard captured = %q, want full hash", captured)
	}
	if !strings.HasPrefix(m.status, "copied ") {
		t.Errorf("status = %q, want 'copied ...'", m.status)
	}
}

func TestYKeyIsNoopWithoutSelection(t *testing.T) {
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
	// No commits loaded — Selected() returns false.

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if captured != "" {
		t.Errorf("clipboard should not be written when no commit is selected, got %q", captured)
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
	m = loadGraphFixture(t, m, "abc1234deadbeefcafe1234567890abcdef12345")

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = updated.(Model)
	if !strings.Contains(m.status, "clipboard unavailable") {
		t.Errorf("status = %q, want clipboard error", m.status)
	}
	if !strings.Contains(m.status, "xclip") {
		t.Errorf("status should carry the underlying error, got %q", m.status)
	}
}

func TestDiffOverlay_NavKeysSwallowed(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.mode = viewModeDiffWindow
	startFocus := m.focused

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
		if cmd != nil {
			t.Errorf("%s in overlay should not dispatch a cmd, got %v", tc.name, cmd)
		}
	}
}

func TestDiffOverlay_BracketKeysJumpFiles(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.mode = viewModeDiffWindow
	m.diff.SetPatchViewportSize(120, 5)
	m.diff.BeginPatchLoad("h", 1)
	m.diff.ApplyPatchLoaded(1, "h", threeFilePatch)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	m = updated.(Model)
	if cmd != nil {
		t.Errorf("] should not dispatch a cmd, got %v", cmd)
	}
	if got := m.diff.viewport.YOffset; got != 7 {
		t.Errorf("] from top, YOffset = %d, want 7 (beta header)", got)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}})
	m = updated.(Model)
	if got := m.diff.viewport.YOffset; got != 0 {
		t.Errorf("[ from beta, YOffset = %d, want 0 (alpha header)", got)
	}
}

func TestRenderDiffOverlayHintCarriesPathAndIndex(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.mode = viewModeDiffWindow
	m.diff.SetPatchViewportSize(120, 5)
	m.diff.BeginPatchLoad("h", 1)
	m.diff.ApplyPatchLoaded(1, "h", threeFilePatch)

	hint := m.renderDiffOverlayHint()
	if !strings.Contains(hint, "alpha.go") {
		t.Errorf("hint missing current path 'alpha.go': %q", hint)
	}
	if !strings.Contains(hint, "[1/3]") {
		t.Errorf("hint missing index '[1/3]': %q", hint)
	}
	if !strings.Contains(hint, "[ ] file") {
		t.Errorf("hint missing keymap '[ ] file': %q", hint)
	}

	m.diff.JumpToNextFile()
	hint = m.renderDiffOverlayHint()
	if !strings.Contains(hint, "beta.go") || !strings.Contains(hint, "[2/3]") {
		t.Errorf("after ], hint = %q, want beta.go + [2/3]", hint)
	}
}

func TestRenderDiffOverlayHintEmptyDiffFallsBackToKeymap(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.mode = viewModeDiffWindow
	m.diff.SetPatchViewportSize(120, 5)
	m.diff.BeginPatchLoad("h", 1)
	m.diff.ApplyPatchLoaded(1, "h", "")

	hint := m.renderDiffOverlayHint()
	if strings.Contains(hint, "[0/0]") {
		t.Errorf("empty diff hint must not show [0/0], got %q", hint)
	}
	if !strings.Contains(hint, "esc close") {
		t.Errorf("empty diff hint should still show keymap, got %q", hint)
	}
}

func TestModelInitSeedsCurrentRefsWithAllSentinel(t *testing.T) {
	m := New()
	if got, want := m.currentRefs, []string{refsAllSentinel}; !slices.Equal(got, want) {
		t.Errorf("currentRefs at New = %v, want %v (unified graph default)", got, want)
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
	// Stale-while-revalidate: the old graph stays on screen (no blank
	// "loading…" flash); pendingSwap is what marks the reload in flight.
	if !m.graph.loaded {
		t.Errorf("r should keep graph.loaded (stale-while-revalidate)")
	}
	if !m.graph.pendingSwap {
		t.Errorf("r should arm graph.pendingSwap")
	}
	if !strings.Contains(m.graph.View(), "first") {
		t.Errorf("graph view should keep old content during reload, got %q", m.graph.View())
	}
	// `r` is deliberately status-silent — the in-place swap is the feedback.
	if m.status != "" {
		t.Errorf("r should not paint a status, got %q", m.status)
	}
	if cmd == nil {
		t.Fatal("r should return a batched load cmd")
	}
	// refs has no View() post-PR-B2 (storage-only). loaded=false is the
	// observable signal that the reload reset took effect.
	if m.refs.loaded {
		t.Error("refs.loaded should be false after r (mid-reload)")
	}

	// The new stream's first batch replaces the old window instead of
	// appending to it.
	updated, _ = m.Update(commitsAppendedMsg{reqID: m.streamReqID, done: true, rows: []graphRow{
		{commit: git.Commit{Hash: "def5678", Subject: "second", AuthorTime: time.Now()}},
	}})
	m = updated.(Model)
	if m.graph.pendingSwap {
		t.Error("first batch should consume pendingSwap")
	}
	view := m.graph.View()
	if !strings.Contains(view, "second") || strings.Contains(view, "first") {
		t.Errorf("first batch should swap the window, got %q", view)
	}
}

func TestSpinnerTickGatedByLoading(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	if !m.spinnerArmed {
		t.Fatal("spinner should arm while the graph is loading")
	}

	updated, cmd := m.Update(spinnerTickMsg{})
	m = updated.(Model)
	if m.spinnerFrame != 1 {
		t.Errorf("tick should advance spinnerFrame, got %d", m.spinnerFrame)
	}
	if cmd == nil {
		t.Error("tick should re-arm while still loading")
	}

	updated, _ = m.Update(commitsAppendedMsg{reqID: m.streamReqID, done: true, rows: []graphRow{
		{commit: git.Commit{Hash: "abc1234", Subject: "first", AuthorTime: time.Now()}},
	}})
	m = updated.(Model)
	updated, cmd = m.Update(spinnerTickMsg{})
	m = updated.(Model)
	if m.spinnerArmed {
		t.Error("spinner should disarm once nothing is loading")
	}
	if cmd != nil {
		t.Errorf("idle tick should not re-arm, got %T", cmd)
	}
	if m.spinnerFrame != 1 {
		t.Errorf("idle tick should not advance the frame, got %d", m.spinnerFrame)
	}
}

func TestBusyStatusGetsSpinnerPrefix(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	updated, _ = m.Update(commitsAppendedMsg{reqID: m.streamReqID, done: true, rows: []graphRow{
		{commit: git.Commit{Hash: "abc1234", Subject: "first", AuthorTime: time.Now()}},
	}})
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'F'}})
	m = updated.(Model)
	if !strings.Contains(m.renderHelpStatus(), spinnerGlyph(m.spinnerFrame)+" fetching…") {
		t.Errorf("busy status should carry the spinner prefix, got %q", m.renderHelpStatus())
	}

	// A terminal status overwrites the text; the prefix must vanish with it.
	updated, _ = m.Update(fetchSucceededMsg{})
	m = updated.(Model)
	if !strings.Contains(m.renderHelpStatus(), "fetch: done") {
		t.Fatalf("expected fetch: done status, got %q", m.renderHelpStatus())
	}
	if strings.Contains(m.renderHelpStatus(), spinnerGlyph(m.spinnerFrame)+" fetch: done") {
		t.Errorf("terminal status must not carry the spinner prefix, got %q", m.renderHelpStatus())
	}
}

func TestModelRKeyPreservesAllSentinel(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
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

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("p should return a pullCmd")
	}
	if !m.pullInFlight {
		t.Error("p should set pullInFlight=true")
	}
	if m.status != "pulling…" {
		t.Errorf("status = %q, want pulling…", m.status)
	}

	updated, cmd2 := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	m = updated.(Model)
	if cmd2 != nil {
		t.Error("second p should not dispatch a parallel pull")
	}
	if m.status != "pulling…" {
		t.Errorf("status should still be pulling…, got %q", m.status)
	}
}

func TestModelPullSucceededReloadsAndJumpsHEAD(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
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

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
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

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
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
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
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

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	_ = updated.(Model)
	if cmd == nil {
		t.Fatal("P should return a pullCmd")
	}
	// Invoke the cmd so the stubbed pullResolveStrategy runs.
	_ = cmd()

	if seenPrefs != "rebase" {
		t.Errorf("pullResolveStrategy got prefs=%q, want rebase", seenPrefs)
	}
	// Post-migration: pullCmd receives m.workdir, which New() seeds from
	// os.Getwd(). Assert the cmd saw the same path so a regression to
	// hardcoded "" surfaces immediately.
	wd, _ := os.Getwd()
	if seenDir != wd {
		t.Errorf("pullResolveStrategy got dir=%q, want %q (m.workdir)", seenDir, wd)
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
	// Stale-while-revalidate: the graph keeps its content and swaps on
	// the new stream's first batch instead of blanking.
	if !m.graph.loaded || !m.graph.pendingSwap {
		t.Error("fetch success should keep graph loaded with pendingSwap armed")
	}
	if m.refs.loaded {
		t.Error("refs.loaded should reset on fetch success")
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

func TestModelQInertInDiffWindow(t *testing.T) {
	// q used to close the patch overlay; it no longer does. Closing is esc
	// (see TestModelEscClosesDiffWindow), quitting is ctrl+c twice. q must be
	// inert: the overlay stays open and nothing quits.
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.mode = viewModeDiffWindow

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	m = updated.(Model)
	if m.mode != viewModeDiffWindow {
		t.Errorf("q must not close the diff window anymore, got mode %v", m.mode)
	}
	if cmd != nil {
		t.Errorf("q in diff window must not dispatch a cmd, got cmd=%v", cmd)
	}
	if m.quitArmed {
		t.Errorf("q must not arm quit")
	}
}

func TestCtrlCArmsThenQuits(t *testing.T) {
	// The first ctrl+c arms quit and paints the hint without quitting; a
	// second consecutive ctrl+c quits.
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = updated.(Model)
	if !m.quitArmed {
		t.Fatalf("first ctrl+c should arm quit")
	}
	if m.status != quitArmHint {
		t.Errorf("first ctrl+c should set the quit hint, got status %q", m.status)
	}
	if cmd != nil {
		t.Errorf("first ctrl+c must not quit, got cmd=%v", cmd)
	}

	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatalf("second ctrl+c should quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("second ctrl+c should dispatch tea.Quit, got %T", cmd())
	}
}

func TestCtrlCDisarmedByOtherKey(t *testing.T) {
	// An intervening non-ctrl+c key clears the armed state + hint, so the
	// next single ctrl+c only re-arms instead of quitting.
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = updated.(Model)
	if !m.quitArmed {
		t.Fatalf("ctrl+c should arm quit")
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = updated.(Model)
	if m.quitArmed {
		t.Errorf("j should disarm quit")
	}
	if m.status != "" {
		t.Errorf("disarm should clear the quit hint, got status %q", m.status)
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = updated.(Model)
	if !m.quitArmed {
		t.Errorf("ctrl+c after disarm should re-arm, not quit")
	}
	if cmd != nil {
		t.Errorf("re-arming ctrl+c must not quit, got cmd=%v", cmd)
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

func stubCheckout(t *testing.T) (
	getRef func() (string, bool),
	getDetached func() (string, bool),
) {
	t.Helper()
	prevC, prevCD := checkoutExec, checkoutDetachedExec
	t.Cleanup(func() {
		checkoutExec = prevC
		checkoutDetachedExec = prevCD
	})
	var ref, detachedRef string
	var refSet, detachedSet bool
	checkoutExec = func(_ context.Context, _, r string) error {
		ref, refSet = r, true
		return nil
	}
	checkoutDetachedExec = func(_ context.Context, _, r string) error {
		detachedRef, detachedSet = r, true
		return nil
	}
	return func() (string, bool) { return ref, refSet },
		func() (string, bool) { return detachedRef, detachedSet }
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

	// `Y` is explicitly listed: it belonged to the (now-cut) force-checkout
	// branch the design retired in subtract-stash. If a future refactor
	// accidentally re-wires it, this test fails immediately rather than
	// silently dispatching a chain. `s` left this list when stash &
	// continue was re-added (narrower than the old stash surface) — its
	// dispatch is covered in stash_test.go.
	for _, k := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'j'}},
		{Type: tea.KeyTab},
		{Type: tea.KeyRunes, Runes: []rune{'F'}},
		{Type: tea.KeyRunes, Runes: []rune{'p'}},
		{Type: tea.KeyRunes, Runes: []rune{'d'}},
		{Type: tea.KeyRunes, Runes: []rune{'Y'}},
		{Type: tea.KeyRunes, Runes: []rune{'y'}},
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
		if m.fetchInFlight || m.pullInFlight || m.checkoutInFlight || m.ffInFlight {
			t.Errorf("swallowed key %v leaked into a dispatch (fetch=%v pull=%v checkout=%v ff=%v)",
				k, m.fetchInFlight, m.pullInFlight, m.checkoutInFlight, m.ffInFlight)
		}
	}
}

// TestModelCheckoutConfirmMatrixAbort asserts the abort half of the
// dirty-tree matrix: `a` / `esc` must exit cleanly across all three
// variants. (Subtract-stash reduced the matrix to abort-only; stash &
// continue later re-added `s` — its dispatch matrix lives in
// stash_test.go. `Y` force-checkout stays retired.)
func TestModelCheckoutConfirmMatrixAbort(t *testing.T) {
	for _, variant := range []struct {
		name string
		p    pendingCheckout
	}{
		{"plain", pendingCheckout{ref: "feat"}},
		{"withFF", pendingCheckout{ref: "main", withFF: true, ffHash: "abc1234"}},
		{"withCheckoutFF", pendingCheckout{ref: "develop", withCheckoutFF: true, ffHash: "abc1234"}},
	} {
		t.Run(variant.name, func(t *testing.T) {
			m := New()
			updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
			m = updated.(Model)
			m.mode = viewModeCheckoutConfirm
			m.pendingCheckout = variant.p

			// `a` exits to viewModeNormal with an aborted-status line.
			updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
			m = updated.(Model)
			if m.mode != viewModeNormal {
				t.Errorf("[a] should exit modal, mode = %v", m.mode)
			}
			if cmd != nil {
				t.Errorf("[a] should not dispatch a cmd, got %v", cmd)
			}
			if !strings.Contains(m.status, "aborted") {
				t.Errorf("[a] should set 'aborted' status, got %q", m.status)
			}
			if (m.pendingCheckout != pendingCheckout{}) {
				t.Errorf("[a] should clear pendingCheckout, got %+v", m.pendingCheckout)
			}

			// `esc` does the same — symmetric escape hatch.
			m.mode = viewModeCheckoutConfirm
			m.pendingCheckout = variant.p
			updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
			m = updated.(Model)
			if m.mode != viewModeNormal {
				t.Errorf("[esc] should exit modal, mode = %v", m.mode)
			}
			if cmd != nil {
				t.Errorf("[esc] should not dispatch a cmd, got %v", cmd)
			}
		})
	}
}

func TestModelGraphCKeyRemoved(t *testing.T) {
	// Regression: 'C' used to detach the cursor commit. The graph Enter
	// flow now subsumes that — chipless rows fall through to detach. Make
	// sure 'C' is no longer wired so a stray keypress doesn't latch a
	// checkout chain.
	_, _ = stubCheckout(t)
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

	if m.checkoutInFlight {
		t.Error("'C' should no longer latch checkoutInFlight (graph Enter handles detach)")
	}
	if m.pendingCheckout.ref != "" {
		t.Errorf("pendingCheckout.ref = %q, want empty (no detach armed)", m.pendingCheckout.ref)
	}
	if cmd != nil {
		t.Error("'C' should return no cmd on graph focus")
	}
}

// E7 regression: after PR 1 (stash cut) + PR 3 (refs `p` cut), the
// dirty-tree confirm-modal abort path must still return the user to the
// graph cleanly — no leaked pendingCheckout state, no leaked in-flight
// gate, no panic across any of the surviving variants (plain / withFF /
// withCheckoutFF). Eng-review iron rule [PR3-CRIT-REG] called for an
// explicit graph-return assertion so a future refactor that "forgets" to
// drop a chain field can't silently strand the user in viewModeNormal
// with stale state.
func TestE7DirtyTreeAbortReturnsToGraphCleanly(t *testing.T) {
	for _, variant := range []struct {
		name  string
		p     pendingCheckout
		entry tea.Msg
	}{
		{
			name:  "plain checkout dirty",
			p:     pendingCheckout{ref: "feat"},
			entry: checkoutNeedsCleanTreeMsg{ref: "feat"},
		},
		{
			name:  "withFF dirty",
			p:     pendingCheckout{ref: "main", withFF: true, ffHash: "abc1234"},
			entry: ffNeedsCleanTreeMsg{branch: "main", hash: "abc1234"},
		},
		{
			name:  "withCheckoutFF dirty",
			p:     pendingCheckout{ref: "develop", withCheckoutFF: true, ffHash: "abc1234"},
			entry: ffCheckoutNeedsCleanTreeMsg{branch: "develop", hash: "abc1234"},
		},
	} {
		t.Run(variant.name, func(t *testing.T) {
			m := New()
			updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
			m = updated.(Model)
			m.focused = paneGraph

			// Arm the in-flight gate the dispatch site would have set, then
			// fire the dirty-tree entry msg. The handler must release the
			// gate, open the confirm, and keep pendingCheckout populated.
			switch variant.entry.(type) {
			case checkoutNeedsCleanTreeMsg:
				m.checkoutInFlight = true
			default:
				m.ffInFlight = true
			}
			m.pendingCheckout = variant.p
			updated, cmd := m.Update(variant.entry)
			m = updated.(Model)
			if cmd != nil {
				t.Errorf("dirty-tree entry should not dispatch a cmd, got %v", cmd)
			}
			if m.mode != viewModeCheckoutConfirm {
				t.Fatalf("entry msg should open confirm modal, mode = %v", m.mode)
			}
			if m.checkoutInFlight || m.ffInFlight {
				t.Errorf("in-flight gates should release when modal owns the next step (co=%v ff=%v)",
					m.checkoutInFlight, m.ffInFlight)
			}

			// Abort via `a` — the graph view must come back without any
			// leftover state.
			updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
			m = updated.(Model)
			if cmd != nil {
				t.Errorf("[a] should not dispatch a cmd, got %v", cmd)
			}
			if m.mode != viewModeNormal {
				t.Errorf("[a] should return to viewModeNormal (graph visible), mode = %v", m.mode)
			}
			if m.focused != paneGraph {
				t.Errorf("[a] should preserve paneGraph focus, focused = %v", m.focused)
			}
			if (m.pendingCheckout != pendingCheckout{}) {
				t.Errorf("[a] must clear pendingCheckout, got %+v", m.pendingCheckout)
			}
			if m.checkoutInFlight || m.ffInFlight {
				t.Errorf("[a] must not re-arm in-flight gates (co=%v ff=%v)",
					m.checkoutInFlight, m.ffInFlight)
			}

			// View() must not panic — the abort path renders the normal
			// 3-pane layout with the post-abort status.
			_ = m.View()
		})
	}
}

// TestOverlayCenters verifies the modal box lands near the screen center
// when the model is in a centered modal mode. We locate the row carrying
// the modal's header text and assert its index is in the neighborhood of
// (height - modalH) / 2 + 1 (header is the first inner row).
func TestOverlayCenters(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	// Branch picker is the simplest centered overlay available post-PR-2.
	m.mode = viewModeBranchPicker
	m.branchPicker = branchPickerState{candidates: []string{"feat/a", "feat/b"}}
	view := m.View()
	rows := strings.Split(view, "\n")

	const headerText = "[Branch select]"
	headerRow := -1
	for i, r := range rows {
		if strings.Contains(ansi.Strip(r), headerText) {
			headerRow = i
			break
		}
	}
	if headerRow == -1 {
		t.Fatalf("modal header %q not found in view\n--- view ---\n%s", headerText, ansi.Strip(view))
	}
	// Branch picker modal: header + 2 candidate rows + hint = 4 inner rows.
	// 4 inner + 2 chrome = 6 outer. On a 30-row screen the centered top
	// edge sits around (30-6)/2 = 12; header is the first inner row near
	// row 13. ±2 slack absorbs future tweaks.
	const wantHeaderRow = 13
	if headerRow < wantHeaderRow-2 || headerRow > wantHeaderRow+2 {
		t.Errorf("modal header row = %d, want around %d", headerRow, wantHeaderRow)
	}
}

func TestModelFocusMsgDispatchesFetch(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	updated, cmd := m.Update(tea.FocusMsg{})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("FocusMsg should return a fetchCmd on first focus")
	}
	if !m.fetchInFlight {
		t.Error("FocusMsg should set fetchInFlight=true")
	}
	if m.lastFetchAt.IsZero() {
		t.Error("FocusMsg should stamp lastFetchAt")
	}
}

func TestModelFocusMsgThrottledWithinWindow(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	// Simulate a recent fetch attempt that just succeeded (so fetchInFlight
	// is reset but the throttle clock is fresh).
	m.lastFetchAt = time.Now().Add(-10 * time.Second)

	updated, cmd := m.Update(tea.FocusMsg{})
	m = updated.(Model)
	if cmd != nil {
		t.Errorf("FocusMsg within %v should be throttled, got cmd=%v", focusFetchThrottle, cmd())
	}
	if m.fetchInFlight {
		t.Error("throttled focus must not flip fetchInFlight")
	}
}

func TestModelFocusMsgPassesThrottleAfterWindow(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	m.lastFetchAt = time.Now().Add(-(focusFetchThrottle + 5*time.Second))

	updated, cmd := m.Update(tea.FocusMsg{})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("FocusMsg past the throttle window should re-dispatch fetch")
	}
	if !m.fetchInFlight {
		t.Error("FocusMsg past the window should flip fetchInFlight=true")
	}
}

func TestModelFocusMsgSuppressedWhileFetchInFlight(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.fetchInFlight = true

	_, cmd := m.Update(tea.FocusMsg{})
	if cmd != nil {
		t.Errorf("FocusMsg should be suppressed while fetch is in flight, got cmd=%v", cmd())
	}
}

func TestModelFKeyStampsLastFetchAt(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'F'}})
	m = updated.(Model)
	if m.lastFetchAt.IsZero() {
		t.Error("F key should stamp lastFetchAt for the footer + throttle")
	}
}

// TestOverlayDimsBackdrop checks that backdrop rows around the modal
// carry the dim 256-color SGR. Without the dim wrap the focused-modal UX
// breaks: the background still reads as the active surface.
func TestOverlayDimsBackdrop(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	m.mode = viewModeBranchPicker
	m.branchPicker = branchPickerState{candidates: []string{"feat/a", "feat/b"}}
	view := m.View()
	rows := strings.Split(view, "\n")

	// Row 0 is far above the modal — must be dimmed.
	if !strings.Contains(rows[0], "\x1b[38;5;240m") {
		t.Errorf("backdrop row 0 missing dim 256-color SGR\nrow=%q", rows[0])
	}
}
