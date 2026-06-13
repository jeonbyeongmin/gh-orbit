package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestPRDiffID(t *testing.T) {
	if got := prDiffID(95); got != "pr/95" {
		t.Errorf("prDiffID(95) = %q, want pr/95", got)
	}
}

// prReviewOpen presses `O` on browsePRFixture's PR-bearing cursor row so the
// overlay is in PR-review mode (reviewPRNumber=42, prAction=none) for the
// sub-state tests. The diff load is stubbed but never executed here.
func prReviewOpen(t *testing.T) Model {
	t.Helper()
	prev := prDiffExec
	t.Cleanup(func() { prDiffExec = prev })
	prDiffExec = func(_ context.Context, _ string, _ int) (string, error) { return "", nil }
	m := browsePRFixture(t)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'O'}})
	return updated.(Model)
}

// O on a PR-badged cursor row opens the patch overlay in PR-review mode and
// dispatches the PR diff under the synthetic pr/<n> id.
func TestPRReviewOpenFromCursorRow(t *testing.T) {
	prev := prDiffExec
	t.Cleanup(func() { prDiffExec = prev })
	var gotNumber int
	prDiffExec = func(_ context.Context, _ string, number int) (string, error) {
		gotNumber = number
		return "diff --git a/x b/x\n", nil
	}

	m := browsePRFixture(t)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'O'}})
	m = updated.(Model)
	if m.mode != viewModeDiffWindow {
		t.Fatalf("mode = %v, want viewModeDiffWindow", m.mode)
	}
	if m.reviewPRNumber != 42 {
		t.Errorf("reviewPRNumber = %d, want 42", m.reviewPRNumber)
	}
	if m.prAction != prActionNone {
		t.Errorf("prAction = %v, want none on open", m.prAction)
	}
	if cmd == nil {
		t.Fatal("O should dispatch loadPRDiffCmd")
	}
	msg := cmd()
	if gotNumber != 42 {
		t.Errorf("loadPRDiff number = %d, want 42", gotNumber)
	}
	loaded, ok := msg.(diffPatchLoadedMsg)
	if !ok {
		t.Fatalf("want diffPatchLoadedMsg, got %T", msg)
	}
	if loaded.hash != "pr/42" {
		t.Errorf("diff id = %q, want pr/42", loaded.hash)
	}
}

// O without a PR-bearing chip reports instead of opening an empty overlay.
func TestPRReviewOpenWithoutPRReports(t *testing.T) {
	m := rebaseFixture(t)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'O'}})
	m = updated.(Model)
	if cmd != nil {
		t.Fatal("O without a PR-bearing chip should not dispatch")
	}
	if m.mode == viewModeDiffWindow {
		t.Error("mode should stay out of the overlay when no PR is on the cursor row")
	}
	if m.reviewPRNumber != 0 {
		t.Errorf("reviewPRNumber = %d, want 0", m.reviewPRNumber)
	}
	if !strings.Contains(m.status, "no open PR") {
		t.Errorf("status = %q, want 'no open PR'", m.status)
	}
}

func TestPRReviewArmAndCancelApprove(t *testing.T) {
	m := prReviewOpen(t)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = updated.(Model)
	if m.prAction != prActionApprove {
		t.Fatalf("prAction = %v, want approve after 'a'", m.prAction)
	}
	// esc cancels back to browse; the overlay stays open.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.prAction != prActionNone {
		t.Errorf("prAction = %v, want none after esc", m.prAction)
	}
	if m.mode != viewModeDiffWindow {
		t.Errorf("esc from armed approve must not close the overlay; mode=%v", m.mode)
	}
}

func TestPRReviewArmMerge(t *testing.T) {
	m := prReviewOpen(t)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	m = updated.(Model)
	if m.prAction != prActionMerge {
		t.Fatalf("prAction = %v, want merge after 'm'", m.prAction)
	}
}

func TestPRReviewBrowseEscCloses(t *testing.T) {
	m := prReviewOpen(t)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.mode != viewModeNormal {
		t.Errorf("esc from browse should close the overlay; mode=%v", m.mode)
	}
	if m.reviewPRNumber != 0 {
		t.Errorf("close must clear reviewPRNumber, got %d", m.reviewPRNumber)
	}
}

func TestPRReviewApproveDispatch(t *testing.T) {
	prev := prReviewExec
	t.Cleanup(func() { prReviewExec = prev })
	var gotNumber int
	prReviewExec = func(_ context.Context, _ string, number int) error {
		gotNumber = number
		return nil
	}
	m := prReviewOpen(t)
	m.prAction = prActionApprove
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = updated.(Model)
	if !m.prReviewInFlight {
		t.Error("y should arm prReviewInFlight")
	}
	if cmd == nil {
		t.Fatal("y should dispatch approvePRCmd")
	}
	if _, ok := cmd().(prApproveDoneMsg); !ok || gotNumber != 42 {
		t.Errorf("approve cmd: number=%d, want prApproveDoneMsg for 42", gotNumber)
	}
}

func TestPRReviewMergeStrategies(t *testing.T) {
	orig := prMergeExec
	t.Cleanup(func() { prMergeExec = orig })
	cases := []struct {
		key  rune
		want string
	}{
		{'s', "squash"},
		{'m', "merge"},
		{'r', "rebase"},
	}
	for _, c := range cases {
		var gotStrategy string
		var gotNumber int
		prMergeExec = func(_ context.Context, _ string, number int, strategy string) error {
			gotNumber, gotStrategy = number, strategy
			return nil
		}
		m := prReviewOpen(t)
		m.prAction = prActionMerge
		updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{c.key}})
		m = updated.(Model)
		if !m.prReviewInFlight {
			t.Errorf("%c: should arm prReviewInFlight", c.key)
		}
		if cmd == nil {
			t.Fatalf("%c: should dispatch mergePRCmd", c.key)
		}
		done, ok := cmd().(prMergeDoneMsg)
		if !ok || done.strategy != c.want || gotStrategy != c.want || gotNumber != 42 {
			t.Errorf("%c: strategy=%q number=%d, want %q/42", c.key, gotStrategy, gotNumber, c.want)
		}
	}
}

func TestPRReviewInFlightSwallowsKeys(t *testing.T) {
	m := prReviewOpen(t)
	m.prReviewInFlight = true
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = updated.(Model)
	if cmd != nil {
		t.Error("keys should be swallowed while a review action is in flight")
	}
	if m.prAction != prActionNone {
		t.Errorf("in-flight 'a' must not arm approve; prAction=%v", m.prAction)
	}
}

func TestLoadPRDiffCmd(t *testing.T) {
	prev := prDiffExec
	t.Cleanup(func() { prDiffExec = prev })
	prDiffExec = func(_ context.Context, _ string, _ int) (string, error) {
		return "diff --git a/f b/f\n", nil
	}
	loaded, ok := loadPRDiffCmd("/x", 7, 3)().(diffPatchLoadedMsg)
	if !ok {
		t.Fatalf("want diffPatchLoadedMsg")
	}
	if loaded.reqID != 3 || loaded.hash != "pr/7" {
		t.Errorf("got reqID=%d hash=%q, want 3/pr/7", loaded.reqID, loaded.hash)
	}

	prDiffExec = func(_ context.Context, _ string, _ int) (string, error) {
		return "", errors.New("boom")
	}
	if _, ok := loadPRDiffCmd("/x", 7, 3)().(diffPatchFailedMsg); !ok {
		t.Error("error path should return diffPatchFailedMsg")
	}
}

func TestApproveAndMergeCmdErrors(t *testing.T) {
	origR := prReviewExec
	origM := prMergeExec
	t.Cleanup(func() { prReviewExec = origR; prMergeExec = origM })

	prReviewExec = func(_ context.Context, _ string, _ int) error { return errors.New("nope") }
	if _, ok := approvePRCmd("/x", 1)().(prApproveFailedMsg); !ok {
		t.Error("approve error should return prApproveFailedMsg")
	}
	prMergeExec = func(_ context.Context, _ string, _ int, _ string) error { return errors.New("nope") }
	if _, ok := mergePRCmd("/x", 1, "squash")().(prMergeFailedMsg); !ok {
		t.Error("merge error should return prMergeFailedMsg")
	}
}

func TestUpdatePRReviewApproveDone(t *testing.T) {
	m := prReviewOpen(t)
	m.prsInFlight = false // so dispatchPRList returns a real cmd
	m.prReviewInFlight = true
	m.setBusyStatus("approving #42…")
	updated, cmd := m.Update(prApproveDoneMsg{number: 42})
	m = updated.(Model)
	if m.prReviewInFlight {
		t.Error("approve done should clear prReviewInFlight")
	}
	if m.mode != viewModeDiffWindow {
		t.Error("approve keeps the overlay open")
	}
	if m.reviewPRNumber != 42 {
		t.Errorf("approve should not clear reviewPRNumber, got %d", m.reviewPRNumber)
	}
	if !strings.Contains(m.prReviewNotice, "approved") || m.prReviewNoticeErr {
		t.Errorf("notice=%q err=%v, want approved/ok", m.prReviewNotice, m.prReviewNoticeErr)
	}
	if m.statusIsBusy() {
		t.Error("busy status must clear so the spinner stops")
	}
	if cmd == nil {
		t.Error("approve done should refresh the PR list")
	}
}

func TestUpdatePRReviewMergeDone(t *testing.T) {
	m := prReviewOpen(t)
	m.prReviewInFlight = true
	updated, _ := m.Update(prMergeDoneMsg{number: 42, strategy: "squash"})
	m = updated.(Model)
	if m.mode != viewModeNormal {
		t.Errorf("merge done should close the overlay; mode=%v", m.mode)
	}
	if m.reviewPRNumber != 0 {
		t.Errorf("merge done should clear reviewPRNumber, got %d", m.reviewPRNumber)
	}
	if !strings.Contains(m.status, "merged #42 (squash)") {
		t.Errorf("status = %q, want merged #42 (squash)", m.status)
	}
	if m.prReviewInFlight {
		t.Error("merge done should clear prReviewInFlight")
	}
}

func TestUpdatePRReviewApproveFailed(t *testing.T) {
	m := prReviewOpen(t)
	m.prReviewInFlight = true
	updated, _ := m.Update(prApproveFailedMsg{err: errors.New("can not approve your own pull request")})
	m = updated.(Model)
	if m.prReviewInFlight {
		t.Error("failure should clear prReviewInFlight")
	}
	if m.mode != viewModeDiffWindow {
		t.Error("failure keeps the overlay open to read/retry")
	}
	if !m.prReviewNoticeErr || !strings.Contains(m.prReviewNotice, "approve your own") {
		t.Errorf("notice=%q err=%v, want error notice", m.prReviewNotice, m.prReviewNoticeErr)
	}
}

func TestRenderPRReviewHint(t *testing.T) {
	m := prReviewOpen(t) // width 120, reviewPRNumber 42, prAction none
	browse := m.renderPRReviewHint()
	for _, want := range []string{"PR #42", "approve", "merge", "esc close"} {
		if !strings.Contains(browse, want) {
			t.Errorf("browse hint missing %q: %q", want, browse)
		}
	}

	m.prAction = prActionApprove
	app := m.renderPRReviewHint()
	if !strings.Contains(app, "approve PR #42?") || !strings.Contains(app, "[y] yes") {
		t.Errorf("approve hint = %q", app)
	}

	m.prAction = prActionMerge
	mrg := m.renderPRReviewHint()
	for _, want := range []string{"squash", "merge", "rebase"} {
		if !strings.Contains(mrg, want) {
			t.Errorf("merge hint missing %q: %q", want, mrg)
		}
	}

	m.prAction = prActionNone
	m.prReviewNotice = "approved #42"
	if notice := m.renderPRReviewHint(); !strings.Contains(notice, "approved #42") {
		t.Errorf("notice hint = %q", notice)
	}
}
