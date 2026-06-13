package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

func samplePRs() []prInfo {
	return []prInfo{
		{Number: 42, HeadRef: "feat-a", Title: "Add the thing", Author: "alice", Checks: prChecksPassing},
		{Number: 41, HeadRef: "feat-b", Title: "Fix the bug", Author: "bob", Checks: prChecksFailing},
		{Number: 40, HeadRef: "feat-c", Title: "Tidy up", Author: "carol", Checks: prChecksNone},
	}
}

// TestPRsModalEmptyGuard locks the "never enter an empty modal" guard: with
// no open PRs, `l` reports on the status line and stays in viewModeNormal.
func TestPRsModalEmptyGuard(t *testing.T) {
	m := initSized(t)
	m, _ = m.beginPRsModal()
	if m.mode != viewModeNormal {
		t.Fatalf("empty prList: mode = %v, want viewModeNormal", m.mode)
	}
	if m.status == "" {
		t.Error("empty prList: want a status message, got none")
	}
}

// TestPRsModalOpenNavigateClose drives the modal through the full Update path:
// `l` opens it at cursor 0, j/k navigate and clamp, `l` again closes it.
func TestPRsModalOpenNavigateClose(t *testing.T) {
	m := initSized(t)
	m.prList = samplePRs()

	m, _ = pressRune(t, m, 'l')
	if m.mode != viewModePRsModal {
		t.Fatalf("after l: mode = %v, want viewModePRsModal", m.mode)
	}
	if m.prsModal.cursor != 0 {
		t.Fatalf("open cursor = %d, want 0", m.prsModal.cursor)
	}

	// k at the top clamps to 0.
	m = m.prsModalMoveCursor(-1)
	if m.prsModal.cursor != 0 {
		t.Errorf("k at top: cursor = %d, want 0", m.prsModal.cursor)
	}
	// j past the end clamps to len-1.
	m = m.prsModalMoveCursor(99)
	if m.prsModal.cursor != len(m.prList)-1 {
		t.Errorf("j past end: cursor = %d, want %d", m.prsModal.cursor, len(m.prList)-1)
	}

	// l toggles closed and resets the cursor.
	m, _ = pressRune(t, m, 'l')
	if m.mode != viewModeNormal {
		t.Fatalf("after second l: mode = %v, want viewModeNormal", m.mode)
	}
	if m.prsModal.cursor != 0 {
		t.Errorf("close cursor = %d, want reset to 0", m.prsModal.cursor)
	}
}

// TestPRsModalEnterOpensReview asserts enter on a row routes into the shared
// review overlay — the same surface `O` uses — for the cursor PR.
func TestPRsModalEnterOpensReview(t *testing.T) {
	m := initSized(t)
	m.prList = samplePRs()
	m, _ = m.beginPRsModal()
	m = m.prsModalMoveCursor(1) // cursor → PR #41

	m2, cmd := m.prsModalEnter()
	if m2.mode != viewModeDiffWindow {
		t.Fatalf("enter: mode = %v, want viewModeDiffWindow", m2.mode)
	}
	if m2.reviewPRNumber != 41 {
		t.Errorf("enter: reviewPRNumber = %d, want 41", m2.reviewPRNumber)
	}
	if cmd == nil {
		t.Error("enter: want a diff-load cmd, got nil")
	}
}

// TestPRsModalCursorClampsOnShrink locks the defensive clamp: a refreshed PR
// list landing while the modal is open (a fetch/pull that was in flight) must
// not leave the cursor past the new end — otherwise it vanishes and enter
// dead-no-ops.
func TestPRsModalCursorClampsOnShrink(t *testing.T) {
	m := initSized(t)
	m.prList = samplePRs() // 3 PRs
	m, _ = m.beginPRsModal()
	m = m.prsModalMoveCursor(2) // cursor → last (index 2)

	// Refresh arrives with a shorter list while the modal is open.
	updated, _ := m.Update(prsLoadedMsg{prs: map[string]prInfo{}, list: samplePRs()[:1]})
	m = updated.(Model)
	if m.prsModal.cursor != 0 {
		t.Errorf("cursor after shrink to 1 = %d, want 0 (clamped)", m.prsModal.cursor)
	}

	// An empty refresh clamps to 0, never negative.
	updated, _ = m.Update(prsLoadedMsg{prs: map[string]prInfo{}, list: nil})
	m = updated.(Model)
	if m.prsModal.cursor != 0 {
		t.Errorf("cursor after empty refresh = %d, want 0", m.prsModal.cursor)
	}
}

func TestRenderPRModalRow(t *testing.T) {
	pr := samplePRs()[0] // #42, passing, alice

	// Unselected row: leading "  ", number, glyph, title, author all present.
	plain := ansi.Strip(renderPRModalRow(pr, false, 76))
	for _, want := range []string{"#42", "✓", "Add the thing", "alice"} {
		if !strings.Contains(plain, want) {
			t.Errorf("row %q missing %q", plain, want)
		}
	}
	if !strings.HasPrefix(plain, "  ") {
		t.Errorf("unselected row should start with two spaces: %q", plain)
	}

	// Selected row carries the cursor marker.
	if sel := ansi.Strip(renderPRModalRow(pr, true, 76)); !strings.HasPrefix(sel, "> ") {
		t.Errorf("selected row should start with %q: %q", "> ", sel)
	}

	// Narrow width truncates with an ellipsis and never exceeds the budget.
	narrow := ansi.Strip(renderPRModalRow(pr, false, 16))
	if !strings.Contains(narrow, "…") {
		t.Errorf("narrow row should be truncated with …: %q", narrow)
	}
	if w := runewidth.StringWidth(narrow); w > 16 {
		t.Errorf("narrow row width = %d, want <= 16: %q", w, narrow)
	}

	// A PR with no author omits the trailing " · ".
	noAuthor := ansi.Strip(renderPRModalRow(prInfo{Number: 7, Title: "Solo", Checks: prChecksNone}, false, 76))
	if strings.Contains(noAuthor, "·") {
		t.Errorf("row with no author should omit the separator: %q", noAuthor)
	}
}
