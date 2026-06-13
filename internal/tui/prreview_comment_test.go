package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestReviewBodyKind(t *testing.T) {
	cases := []struct {
		a    prAction
		want string
	}{
		{prActionComment, "comment"},
		{prActionRequestChanges, "request-changes"},
		{prActionApprove, ""},
		{prActionNone, ""},
	}
	for _, c := range cases {
		if got := c.a.reviewBodyKind(); got != c.want {
			t.Errorf("reviewBodyKind(%v) = %q, want %q", c.a, got, c.want)
		}
	}
}

func TestPRReviewArmCommentAndRequestChanges(t *testing.T) {
	for _, c := range []struct {
		key  rune
		want prAction
	}{{'c', prActionComment}, {'r', prActionRequestChanges}} {
		m := prReviewOpen(t)
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{c.key}})
		m = updated.(Model)
		if m.prAction != c.want {
			t.Errorf("%q armed prAction = %v, want %v", c.key, m.prAction, c.want)
		}
		if m.mode != viewModeDiffWindow {
			t.Errorf("%q must keep the overlay open; mode=%v", c.key, m.mode)
		}
	}
}

// In the editor sub-state, action keys (`a`/`m`/`c`/`r`) are body text, not
// re-arms — they must land in the textarea and leave prAction unchanged.
func TestPRReviewBodyTypingNotReArm(t *testing.T) {
	m := prReviewOpen(t)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = updated.(Model)
	if m.prAction != prActionComment {
		t.Errorf("'a' in the editor re-armed prAction = %v, want comment", m.prAction)
	}
	if !strings.Contains(m.prReviewBody.Value(), "a") {
		t.Errorf("typed 'a' not in body: %q", m.prReviewBody.Value())
	}
}

func TestPRReviewBodyEmptyGuard(t *testing.T) {
	m := prReviewOpen(t)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	m = updated.(Model)
	// ctrl+s with an empty body: rejected inline, no dispatch, editor stays.
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	if cmd != nil {
		t.Error("empty body should not dispatch")
	}
	if m.prReviewInFlight {
		t.Error("empty body should not arm in-flight")
	}
	if m.prAction != prActionComment {
		t.Errorf("editor must stay open on empty-body reject; prAction=%v", m.prAction)
	}
	if !strings.Contains(m.prReviewBodyErr, "body required") {
		t.Errorf("prReviewBodyErr = %q, want 'body required'", m.prReviewBodyErr)
	}
}

func TestPRReviewBodySubmitDispatch(t *testing.T) {
	prev := prReviewBodyExec
	t.Cleanup(func() { prReviewBodyExec = prev })
	var gotNum int
	var gotKind, gotBody string
	prReviewBodyExec = func(_ context.Context, _ string, number int, kind, body string) error {
		gotNum, gotKind, gotBody = number, kind, body
		return nil
	}

	m := prReviewOpen(t)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = updated.(Model)
	m.prReviewBody.SetValue("please split this")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)
	if !m.prReviewInFlight {
		t.Error("ctrl+s should arm prReviewInFlight")
	}
	if cmd == nil {
		t.Fatal("ctrl+s should dispatch reviewBodyCmd")
	}
	msg := cmd()
	done, ok := msg.(prReviewBodyDoneMsg)
	if !ok {
		t.Fatalf("want prReviewBodyDoneMsg, got %T", msg)
	}
	if gotNum != 42 || gotKind != "request-changes" || gotBody != "please split this" {
		t.Errorf("exec got (num=%d kind=%q body=%q), want (42, request-changes, 'please split this')", gotNum, gotKind, gotBody)
	}
	if done.kind != "request-changes" {
		t.Errorf("done.kind = %q, want request-changes", done.kind)
	}
}

// TestPRReviewBodyMsgsRouted guards the dispatcher registration (the exact
// class of bug from #2): prReviewBody{Done,Failed}Msg must reach
// updatePRReviewMsg, else they fall through to `return m, nil` and the
// "submitting…" state sticks forever.
func TestPRReviewBodyMsgsRouted(t *testing.T) {
	// Done: clears in-flight, closes the editor, posts the notice.
	m := prReviewOpen(t)
	m.prAction = prActionComment
	m.prReviewInFlight = true
	updated, _ := m.Update(prReviewBodyDoneMsg{number: 42, kind: "comment"})
	m = updated.(Model)
	if m.prReviewInFlight {
		t.Error("done not routed — in-flight stuck")
	}
	if m.prAction != prActionNone {
		t.Errorf("done should close the editor; prAction=%v", m.prAction)
	}
	if !strings.Contains(m.prReviewNotice, "commented on #42") {
		t.Errorf("notice = %q, want 'commented on #42'", m.prReviewNotice)
	}

	// Failed: clears in-flight, keeps the editor open with the error inline.
	m = prReviewOpen(t)
	m.prAction = prActionRequestChanges
	m.prReviewInFlight = true
	updated, _ = m.Update(prReviewBodyFailedMsg{err: errors.New("gh boom")})
	m = updated.(Model)
	if m.prReviewInFlight {
		t.Error("failed not routed — in-flight stuck")
	}
	if m.prAction != prActionRequestChanges {
		t.Errorf("failed should keep the editor open; prAction=%v", m.prAction)
	}
	if !strings.Contains(m.prReviewBodyErr, "gh boom") {
		t.Errorf("prReviewBodyErr = %q, want gh error", m.prReviewBodyErr)
	}
}
