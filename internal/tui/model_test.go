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
		t.Fatal("commitSelectedMsg should return a debounce tick cmd")
	}
	if m.diffReqID != priorReqID+1 {
		t.Errorf("diffReqID should advance by 1, got %d (was %d)", m.diffReqID, priorReqID)
	}
	if !m.diff.loadingStat {
		t.Error("diff sub-model should be marked loading after commitSelectedMsg")
	}
	if m.diff.currentHash != "aaa1111" {
		t.Errorf("diff.currentHash = %q, want aaa1111", m.diff.currentHash)
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

	// Pretend we dispatched two cursor moves; the latest reqID is 7 and the
	// diff sub-model is loading for hash "current".
	m.diffReqID = 7
	m.diff.MarkLoadingStat("current", 7)

	updated, _ = m.Update(diffStatLoadedMsg{reqID: 3, hash: "old", files: []git.FileStat{
		{Path: "stale.txt", Insertions: 1},
	}})
	m = updated.(Model)
	if m.diff.statLoaded {
		t.Error("stale diffStatLoadedMsg must not flip statLoaded")
	}
	if !m.diff.loadingStat {
		t.Error("stale response should leave loadingStat=true since the in-flight call is still pending")
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
