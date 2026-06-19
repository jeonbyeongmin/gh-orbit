package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func checksPR() prInfo {
	return prInfo{
		Number: 42, HeadRef: "feat-a", Title: "Add the thing", Checks: prChecksFailing,
		CheckRows: []prCheck{
			{Name: "lint", State: prChecksFailing, URL: "https://ci/lint"},
			{Name: "build", State: prChecksPending, URL: "https://ci/build"},
			{Name: "unit", State: prChecksPassing, URL: "https://ci/unit"},
		},
	}
}

// `C` on a PR with checks opens the modal; esc closes back to the launching
// page (here the Pull Requests page).
func TestPRChecksOpenAndClose(t *testing.T) {
	m := initSized(t)
	m.prList = []prInfo{checksPR()}
	m, _ = m.enterPRsPage()

	m, _ = m.beginPRChecksForCursorPR()
	if m.mode != viewModePRChecks {
		t.Fatalf("C: mode = %v, want viewModePRChecks", m.mode)
	}
	if m.prChecks.returnMode != viewModePRsPage {
		t.Errorf("returnMode = %v, want viewModePRsPage", m.prChecks.returnMode)
	}
	if m.currentPageIndex() != 3 {
		t.Errorf("page index while modal open = %d, want 3 (breadcrumb stays on PRs)", m.currentPageIndex())
	}

	out := ansi.Strip(m.View())
	for _, want := range []string{"Checks · #42", "lint", "build", "unit"} {
		if !strings.Contains(out, want) {
			t.Errorf("modal view missing %q\n--- view ---\n%s", want, out)
		}
	}

	m.closePRChecks()
	if m.mode != viewModePRsPage {
		t.Fatalf("esc: mode = %v, want viewModePRsPage", m.mode)
	}
	if m.prChecks.number != 0 {
		t.Errorf("close should zero prChecks, got number %d", m.prChecks.number)
	}
}

// A PR whose rollup is empty has nothing to list — the modal refuses and
// reports on the status line instead of opening empty.
func TestPRChecksEmptyRefuses(t *testing.T) {
	m := initSized(t)
	m, _ = m.beginPRChecksFor(prInfo{Number: 9})
	if m.mode == viewModePRChecks {
		t.Fatal("no-checks PR should not open the modal")
	}
	if !strings.Contains(m.status, "#9") {
		t.Errorf("status = %q, want a no-checks message naming #9", m.status)
	}
}

// ↑/↓ navigate and clamp within the rows.
func TestPRChecksNavigateClamps(t *testing.T) {
	m := initSized(t)
	m, _ = m.beginPRChecksFor(checksPR())

	m = m.prChecksMoveCursor(-1)
	if m.prChecks.cursor != 0 {
		t.Errorf("↑ at top: cursor = %d, want 0", m.prChecks.cursor)
	}
	m = m.prChecksMoveCursor(99)
	if m.prChecks.cursor != len(m.prChecks.rows)-1 {
		t.Errorf("↓ past end: cursor = %d, want %d", m.prChecks.cursor, len(m.prChecks.rows)-1)
	}
}

// enter on a row opens that check's URL via the OS launcher seam.
func TestPRChecksEnterOpensURL(t *testing.T) {
	prev := openURLExec
	t.Cleanup(func() { openURLExec = prev })
	var gotURL string
	openURLExec = func(_ context.Context, url string) error {
		gotURL = url
		return nil
	}

	m := initSized(t)
	m, _ = m.beginPRChecksFor(checksPR()) // cursor on "lint" (failures first)

	_, cmd := m.prChecksOpenSelected()
	if cmd == nil {
		t.Fatal("enter: want an open-URL cmd, got nil")
	}
	cmd()
	if gotURL != "https://ci/lint" {
		t.Errorf("enter opened %q, want https://ci/lint", gotURL)
	}
}

// enter on a check with no URL reports rather than launching an empty tab.
func TestPRChecksEnterNoURLReports(t *testing.T) {
	m := initSized(t)
	m, _ = m.beginPRChecksFor(prInfo{
		Number:    1,
		CheckRows: []prCheck{{Name: "legacy", State: prChecksFailing}},
	})
	m, cmd := m.prChecksOpenSelected()
	if cmd != nil {
		t.Fatal("a URL-less check should not dispatch an open cmd")
	}
	if !strings.Contains(m.status, "legacy") {
		t.Errorf("status = %q, want a no-URL message naming the check", m.status)
	}
}
