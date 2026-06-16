package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func samplePRs() []prInfo {
	return []prInfo{
		{Number: 42, HeadRef: "feat-a", BaseRef: "develop", Title: "Add the thing", Author: "alice", Checks: prChecksPassing, Review: prReviewApproved, Additions: 120, Deletions: 8, Files: 3},
		{Number: 41, HeadRef: "feat-b", BaseRef: "develop", Title: "Fix the bug", Author: "bob", Checks: prChecksFailing, Review: prReviewChangesRequested, Conflicting: true, Additions: 4, Deletions: 2, Files: 1},
		{Number: 40, HeadRef: "feat-c", BaseRef: "develop", Title: "Tidy up", Author: "carol", Checks: prChecksNone},
	}
}

// The Pull Requests tab is the wrap-around reverse neighbor of the graph:
// shift+tab from the graph lands on it (it's the 4th, last page).
func TestPRsPageReachedByShiftTab(t *testing.T) {
	m := initSized(t)
	m.prList = samplePRs()

	m, _ = pressShiftTab(t, m)
	if m.mode != viewModePRsPage {
		t.Fatalf("shift+tab from graph: mode = %v, want viewModePRsPage", m.mode)
	}
	if m.currentPageIndex() != 3 {
		t.Errorf("PR page index = %d, want 3", m.currentPageIndex())
	}
}

// An empty PR list still shows the tab (no bounce to normal) — unlike the old
// `l` modal's empty guard.
func TestPRsPageEntersEvenWhenEmpty(t *testing.T) {
	m := initSized(t)
	m, _ = m.enterPRsPage()
	if m.mode != viewModePRsPage {
		t.Fatalf("empty prList: mode = %v, want viewModePRsPage", m.mode)
	}
	if !strings.Contains(ansi.Strip(m.View()), "no open PRs") {
		t.Error("empty PR page should render the empty-state line")
	}
}

// ↑/↓ navigate and clamp within the list.
func TestPRsPageNavigateClamps(t *testing.T) {
	m := initSized(t)
	m.prList = samplePRs()
	m, _ = m.enterPRsPage()
	if m.prsPage.cursor != 0 {
		t.Fatalf("open cursor = %d, want 0", m.prsPage.cursor)
	}

	m = m.prsPageMoveCursor(-1)
	if m.prsPage.cursor != 0 {
		t.Errorf("↑ at top: cursor = %d, want 0", m.prsPage.cursor)
	}
	m = m.prsPageMoveCursor(99)
	if m.prsPage.cursor != len(m.prList)-1 {
		t.Errorf("↓ past end: cursor = %d, want %d", m.prsPage.cursor, len(m.prList)-1)
	}
}

// enter on a row opens that PR on the web for the cursor PR.
func TestPRsPageEnterOpensWeb(t *testing.T) {
	prev := prViewWebExec
	t.Cleanup(func() { prViewWebExec = prev })
	var gotNumber int
	prViewWebExec = func(_ context.Context, _ string, number int) error {
		gotNumber = number
		return nil
	}

	m := initSized(t)
	m.prList = samplePRs()
	m, _ = m.enterPRsPage()
	m = m.prsPageMoveCursor(1) // cursor → PR #41

	_, cmd := m.prsPageOpenWeb()
	if cmd == nil {
		t.Fatal("enter: want an open-web cmd, got nil")
	}
	cmd()
	if gotNumber != 41 {
		t.Errorf("enter opened #%d, want 41", gotNumber)
	}
}

// m on a row arms the merge confirm for the cursor PR, with the PR page as the
// return mode so the dialog composes over — and closes back to — the page.
func TestPRsPageMergeArmsConfirm(t *testing.T) {
	m := initSized(t)
	m.prList = samplePRs()
	m, _ = m.enterPRsPage()
	m = m.prsPageMoveCursor(2) // cursor → PR #40

	m2, _ := m.prsPageMerge()
	if m2.mode != viewModeMergeConfirm {
		t.Fatalf("m: mode = %v, want viewModeMergeConfirm", m2.mode)
	}
	if m2.mergeConfirm.number != 40 {
		t.Errorf("mergeConfirm.number = %d, want 40", m2.mergeConfirm.number)
	}
	if m2.mergeReturnMode != viewModePRsPage {
		t.Errorf("PR-page merge should return to the PR page, got %v", m2.mergeReturnMode)
	}
	if m2.currentPageIndex() != 3 {
		t.Errorf("PR-page merge keeps the Pull Requests tab, got page %d", m2.currentPageIndex())
	}
}

// A refreshed PR list landing while the page is open (a fetch/pull that was in
// flight) must not leave the cursor past the new end.
func TestPRsPageCursorClampsOnShrink(t *testing.T) {
	m := initSized(t)
	m.prList = samplePRs() // 3 PRs
	m, _ = m.enterPRsPage()
	m = m.prsPageMoveCursor(2) // cursor → last (index 2)

	updated, _ := m.Update(prsLoadedMsg{prs: map[string]prInfo{}, list: samplePRs()[:1]})
	m = updated.(Model)
	if m.prsPage.cursor != 0 {
		t.Errorf("cursor after shrink to 1 = %d, want 0 (clamped)", m.prsPage.cursor)
	}

	updated, _ = m.Update(prsLoadedMsg{prs: map[string]prInfo{}, list: nil})
	m = updated.(Model)
	if m.prsPage.cursor != 0 {
		t.Errorf("cursor after empty refresh = %d, want 0", m.prsPage.cursor)
	}
}

// A refresh that shrinks the list while the merge confirm is composed over the
// PR page must still clamp the cursor — closeMergeConfirm returns to the page
// without re-clamping, so a stale out-of-range cursor would dead-no-op there.
func TestPRsPageCursorClampsWithMergeConfirmOpen(t *testing.T) {
	m := initSized(t)
	m.prList = samplePRs() // 3 PRs
	m, _ = m.enterPRsPage()
	m = m.prsPageMoveCursor(2) // cursor → last (index 2)
	m, _ = m.prsPageMerge()    // arm merge confirm over the PR page
	if m.mode != viewModeMergeConfirm || m.mergeReturnMode != viewModePRsPage {
		t.Fatalf("setup: want merge confirm over PR page, mode=%v return=%v", m.mode, m.mergeReturnMode)
	}

	updated, _ := m.Update(prsLoadedMsg{prs: map[string]prInfo{}, list: samplePRs()[:1]})
	m = updated.(Model)
	if m.prsPage.cursor != 0 {
		t.Errorf("cursor after shrink with merge confirm open = %d, want 0 (clamped)", m.prsPage.cursor)
	}
}

// renderPRsView fills exactly `height` lines (the box frame must never jump)
// and surfaces the header plus a cursor row.
func TestRenderPRsViewFillsHeight(t *testing.T) {
	m := initSized(t)
	m.prList = samplePRs()
	m, _ = m.enterPRsPage()
	out := m.renderPRsView(60, 20)
	if lines := strings.Split(out, "\n"); len(lines) != 20 {
		t.Errorf("view should stay exactly 20 lines, got %d", len(lines))
	}
	plain := ansi.Strip(out)
	if !strings.Contains(plain, "Pull requests · 3") {
		t.Errorf("header missing the PR count: %q", plain)
	}
	if !strings.Contains(plain, "Add the thing") {
		t.Errorf("view should list the cursor PR row: %q", plain)
	}
}

// At degenerate small heights the padded body must not overflow the box.
func TestRenderPRsViewTinyHeightNoOverflow(t *testing.T) {
	m := initSized(t)
	m.prList = samplePRs()
	m, _ = m.enterPRsPage()
	for _, h := range []int{1, 2, 3, 4, 5} {
		if n := len(strings.Split(m.renderPRsView(40, h), "\n")); n > h {
			t.Errorf("height %d: produced %d lines (overflows the box)", h, n)
		}
	}
}

// A list taller than the window flags the clipped overflow with a ↓ marker and
// still never exceeds the height.
func TestRenderPRsViewOverflowMarker(t *testing.T) {
	m := initSized(t)
	many := make([]prInfo, 0, 20)
	for i := 0; i < 20; i++ {
		many = append(many, prInfo{Number: 100 + i, Title: "PR", Checks: prChecksNone})
	}
	m.prList = many
	m, _ = m.enterPRsPage()
	out := m.renderPRsView(60, 10)
	if lines := strings.Split(out, "\n"); len(lines) != 10 {
		t.Errorf("view should stay exactly 10 lines, got %d", len(lines))
	}
	if !strings.Contains(ansi.Strip(out), "more") {
		t.Errorf("a clipped list should flag the overflow with a ↓ N more marker: %q", ansi.Strip(out))
	}
}

// The ↓ marker must survive the final height clamp at heights where the card
// math leaves no slack — (height-2) ≡ 3 (mod 4), e.g. 9 — otherwise an
// overflowing list shows no "more below" affordance. Regresses against the
// single-line layout that reserved marker rows.
func TestRenderPRsViewOverflowMarkerSurvivesClamp(t *testing.T) {
	m := initSized(t)
	many := make([]prInfo, 0, 20)
	for i := 0; i < 20; i++ {
		many = append(many, prInfo{Number: 100 + i, Title: "PR", Checks: prChecksNone})
	}
	m.prList = many
	m, _ = m.enterPRsPage() // cursor at 0 → only a ↓ marker, never a ↑ one
	for _, h := range []int{9, 13} {
		out := m.renderPRsView(60, h)
		if lines := strings.Split(out, "\n"); len(lines) != h {
			t.Errorf("height %d: view = %d lines, want %d", h, len(lines), h)
		}
		if !strings.Contains(ansi.Strip(out), "↓") {
			t.Errorf("height %d: clipped list lost its ↓ overflow marker: %q", h, ansi.Strip(out))
		}
	}
}

func TestBuildPRCard(t *testing.T) {
	now := time.Now()
	pr := samplePRs()[0] // #42, passing, approved, alice, feat-a → develop, +120 -8, 3 files

	card := buildPRCard(pr, false, 76, now)
	if len(card) != prCardLines {
		t.Fatalf("card = %d lines, want %d", len(card), prCardLines)
	}
	plain := ansi.Strip(strings.Join(card, "\n"))
	for _, want := range []string{"#42", "✓", "Add the thing", "@alice", "feat-a → develop", "● approved", "+120 -8", "3 files"} {
		if !strings.Contains(plain, want) {
			t.Errorf("card %q missing %q", plain, want)
		}
	}
	// Unselected card carries no cursor glyphs.
	if strings.ContainsAny(plain, "▌▶") {
		t.Errorf("unselected card should have no cursor bar/marker: %q", plain)
	}

	// Selected card shows the cursor bar + marker.
	sel := ansi.Strip(strings.Join(buildPRCard(pr, true, 76, now), "\n"))
	if !strings.Contains(sel, "▌") || !strings.Contains(sel, "▶") {
		t.Errorf("selected card should show the cursor bar + marker: %q", sel)
	}

	// A conflicting PR surfaces the ⚠ mark; a singular file count reads "file".
	conflict := ansi.Strip(strings.Join(buildPRCard(samplePRs()[1], false, 76, now), "\n"))
	if !strings.Contains(conflict, "⚠") {
		t.Errorf("conflicting PR should show the ⚠ mark: %q", conflict)
	}
	if !strings.Contains(conflict, "1 file") || strings.Contains(conflict, "1 files") {
		t.Errorf("single changed file should read \"1 file\": %q", conflict)
	}

	// Every line stays padded to exactly width — the box frame must never wrap,
	// even at a narrow width that truncates the title.
	for _, line := range buildPRCard(pr, true, 36, now) {
		if w := lipgloss.Width(line); w != 36 {
			t.Errorf("line width = %d, want 36 (padded, no overflow): %q", w, ansi.Strip(line))
		}
	}
	if !strings.Contains(ansi.Strip(strings.Join(buildPRCard(pr, false, 24, now), "\n")), "…") {
		t.Error("a narrow card should truncate the title with …")
	}
}

// The PR page poll re-pulls the list on a 30s tick, but only while on the page
// and focused — blurred or off-page it makes no gh round-trip.
func TestPRsPagePollFocusGated(t *testing.T) {
	m := initSized(t)
	m.prList = samplePRs()
	m, _ = m.enterPRsPage()
	m.prsPollArmed = true // steady state: page already entered, tick already armed

	// Focused poll on the page dispatches a refresh (dispatchPRList arms the gate).
	m.windowFocused = true
	m.prsInFlight = false
	updated, _ := m.Update(prsPollMsg{})
	m = updated.(Model)
	if !m.prsInFlight {
		t.Error("focused poll on the PR page should dispatch a PR-list refresh")
	}

	// Blurred poll keeps the tick alive but makes no gh call.
	m.windowFocused = false
	m.prsInFlight = false
	updated, _ = m.Update(prsPollMsg{})
	m = updated.(Model)
	if m.prsInFlight {
		t.Error("blurred poll must not dispatch a gh round-trip")
	}
	if !m.prsPollArmed {
		t.Error("blurred poll should keep the tick armed")
	}

	// Leaving the page lets the tick die (no re-arm).
	m.mode = viewModeNormal
	updated, _ = m.Update(prsPollMsg{})
	m = updated.(Model)
	if m.prsPollArmed {
		t.Error("poll off the PR page should clear prsPollArmed")
	}
}

// Blur/Focus events toggle windowFocused, which gates the PR poll's gh call.
func TestWindowFocusToggle(t *testing.T) {
	m := initSized(t)
	if !m.windowFocused {
		t.Fatal("model should start focused")
	}
	updated, _ := m.Update(tea.BlurMsg{})
	if updated.(Model).windowFocused {
		t.Error("BlurMsg should clear windowFocused")
	}
	updated, _ = updated.(Model).Update(tea.FocusMsg{})
	if !updated.(Model).windowFocused {
		t.Error("FocusMsg should set windowFocused")
	}
}
