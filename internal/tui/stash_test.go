package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

func withStashStub(t *testing.T, stub func(context.Context, string) error) {
	t.Helper()
	prev := stashPushExec
	t.Cleanup(func() { stashPushExec = prev })
	stashPushExec = stub
}

func TestStashThenRetryCmdStashFailure(t *testing.T) {
	withStashStub(t, func(context.Context, string) error {
		return errors.New("git stash push: exit status 1: refused")
	})
	checkoutCalled := false
	withCheckoutStubs(t, func(context.Context, string, string) error {
		checkoutCalled = true
		return nil
	}, nil)

	msg := stashThenRetryCmd("/repo", pendingCheckout{ref: "feat"})()
	got, ok := msg.(stashFailedMsg)
	if !ok {
		t.Fatalf("msg = %T, want stashFailedMsg", msg)
	}
	if !strings.Contains(got.err.Error(), "refused") {
		t.Errorf("err = %q, want git's stderr preserved", got.err)
	}
	if checkoutCalled {
		t.Error("stash failure must not retry the checkout")
	}
}

func TestStashThenRetryCmdReplaysInterruptedChain(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    pendingCheckout
		want string
	}{
		{"plain checkout", pendingCheckout{ref: "feat"}, "checkoutSucceededMsg"},
		{"detached", pendingCheckout{ref: "abc1234", detached: true}, "checkoutSucceededMsg"},
		{"withFF", pendingCheckout{ref: "main", withFF: true, ffHash: "abc1234"}, "ffSucceededMsg"},
		{"withCheckoutFF", pendingCheckout{ref: "develop", withCheckoutFF: true, ffHash: "abc1234"}, "checkoutThenFFSucceededMsg"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stashed := false
			withStashStub(t, func(context.Context, string) error {
				stashed = true
				return nil
			})
			withChainStubs(t, chainStubs{
				checkout: func(context.Context, string, string) error {
					if !stashed {
						t.Error("checkout ran before the stash landed")
					}
					return nil
				},
				checkoutDetached: func(context.Context, string, string) error { return nil },
				mergeFFOnly:      func(context.Context, string, string) error { return nil },
				countAhead:       func(context.Context, string, string, string) (int, error) { return 2, nil },
			})

			msg := stashThenRetryCmd("/repo", tc.p)()
			var got string
			switch msg.(type) {
			case checkoutSucceededMsg:
				got = "checkoutSucceededMsg"
			case ffSucceededMsg:
				got = "ffSucceededMsg"
			case checkoutThenFFSucceededMsg:
				got = "checkoutThenFFSucceededMsg"
			default:
				t.Fatalf("msg = %T, unexpected", msg)
			}
			if got != tc.want {
				t.Errorf("msg = %s, want %s", got, tc.want)
			}
			if !stashed {
				t.Error("stash step never ran")
			}
		})
	}
}

// TestModelCheckoutConfirmStashDispatchesChain covers the `s` half of the
// dirty-tree confirm matrix: every variant must close the modal, arm the
// gate its retry chain reports back through, remember the branch the stash
// was taken on, and keep pendingCheckout for a dirty re-entry.
func TestModelCheckoutConfirmStashDispatchesChain(t *testing.T) {
	for _, variant := range []struct {
		name     string
		p        pendingCheckout
		wantGate string // "checkout" or "ff"
	}{
		{"plain", pendingCheckout{ref: "feat"}, "checkout"},
		{"withFF", pendingCheckout{ref: "main", withFF: true, ffHash: "abc1234"}, "ff"},
		{"withCheckoutFF", pendingCheckout{ref: "develop", withCheckoutFF: true, ffHash: "abc1234"}, "ff"},
	} {
		t.Run(variant.name, func(t *testing.T) {
			m := New()
			updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
			m = updated.(Model)
			m = seedRefs(t, m, []git.Ref{
				{ShortName: "work", Kind: git.RefKindLocal, ObjectName: "abc1234", IsHead: true},
			})
			m.mode = viewModeCheckoutConfirm
			m.pendingCheckout = variant.p

			updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
			m = updated.(Model)

			if m.mode != viewModeNormal {
				t.Errorf("mode = %v, want viewModeNormal", m.mode)
			}
			if cmd == nil {
				t.Fatal("[s] should dispatch the stash retry cmd")
			}
			wantCheckout := variant.wantGate == "checkout"
			if m.checkoutInFlight != wantCheckout || m.ffInFlight == wantCheckout {
				t.Errorf("gates (checkout=%v ff=%v), want %s armed",
					m.checkoutInFlight, m.ffInFlight, variant.wantGate)
			}
			if m.stashNotice != "work" {
				t.Errorf("stashNotice = %q, want HEAD branch 'work'", m.stashNotice)
			}
			if m.pendingCheckout != variant.p {
				t.Errorf("pendingCheckout = %+v, want retained for dirty re-entry", m.pendingCheckout)
			}
			if !strings.HasPrefix(m.status, "stash & ") {
				t.Errorf("status = %q, want 'stash & …' busy line", m.status)
			}
		})
	}
}

func TestModelStashNoticeSuffixAndReturnPopHint(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	// Leaving: the success status names the branch the changes went to.
	m.stashNotice = "develop"
	updated, _ = m.Update(checkoutSucceededMsg{ref: "feat"})
	m = updated.(Model)
	if !strings.Contains(m.status, "stashed on develop") {
		t.Errorf("status = %q, want the 'stashed on develop' suffix", m.status)
	}
	if m.stashNotice != "" {
		t.Errorf("stashNotice = %q, want consumed", m.stashNotice)
	}
	if !m.stashedRefs["develop"] {
		t.Error("stashedRefs should record the branch for the return hint")
	}

	// Returning: checking the stashed branch out again reminds the pop.
	updated, _ = m.Update(checkoutSucceededMsg{ref: "develop"})
	m = updated.(Model)
	if !strings.Contains(m.status, "git stash pop") {
		t.Errorf("status = %q, want the pop reminder", m.status)
	}
}

func TestModelStashNoticeConsumedOnRetryFailure(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)

	// Stash landed but the retry checkout failed: the changes are out of
	// the tree, so the failure line must still say where they went.
	m.stashNotice = "develop"
	m.checkoutInFlight = true
	updated, _ = m.Update(checkoutFailedMsg{err: errors.New("ref vanished")})
	m = updated.(Model)
	if !strings.Contains(m.status, "stashed on develop") {
		t.Errorf("status = %q, want the stash named on failure too", m.status)
	}
}

// TestModelStashNoticeConsumedOnReentryAbort guards the re-entry leak: a
// stash that landed, hit dirty again on the retry, and got aborted must
// surface its notice on the abort line — not on a later unrelated status.
func TestModelStashNoticeConsumedOnReentryAbort(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.mode = viewModeCheckoutConfirm
	m.pendingCheckout = pendingCheckout{ref: "feat"}
	m.stashNotice = "develop"

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = updated.(Model)
	if !strings.Contains(m.status, "stashed on develop") {
		t.Errorf("status = %q, want the armed notice folded into the abort line", m.status)
	}
	if m.stashNotice != "" {
		t.Errorf("stashNotice = %q, want consumed on abort", m.stashNotice)
	}

	// A later unrelated checkout must come out clean.
	updated, _ = m.Update(checkoutSucceededMsg{ref: "other"})
	m = updated.(Model)
	if strings.Contains(m.status, "stashed on") {
		t.Errorf("status = %q, notice leaked past the abort", m.status)
	}
}

func TestModelStashFailedMsgKillsChain(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.checkoutInFlight = true
	m.ffInFlight = true
	m.pendingCheckout = pendingCheckout{ref: "feat"}
	m.stashNotice = "develop"

	updated, cmd := m.Update(stashFailedMsg{err: errors.New("stash refused")})
	m = updated.(Model)

	if cmd != nil {
		t.Error("stash failure should not dispatch a follow-up cmd")
	}
	if m.checkoutInFlight || m.ffInFlight {
		t.Errorf("gates should clear (checkout=%v ff=%v)",
			m.checkoutInFlight, m.ffInFlight)
	}
	if (m.pendingCheckout != pendingCheckout{}) {
		t.Errorf("pendingCheckout = %+v, want zero", m.pendingCheckout)
	}
	if m.stashNotice != "" {
		t.Errorf("stashNotice = %q, want cleared — nothing was stashed", m.stashNotice)
	}
	if !strings.Contains(m.status, "stash failed") {
		t.Errorf("status = %q, want 'stash failed: …'", m.status)
	}
}

// TestModelStashRetriesCheckoutOnContinue asserts the dirty-tree detour's
// `s` (stash & continue) replays the interrupted checkout: the retry runs
// and lands on the target branch.
func TestModelStashRetriesCheckoutOnContinue(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.checkoutInFlight = true
	m.pendingCheckout = pendingCheckout{ref: "develop"}

	updated, _ = m.Update(checkoutNeedsCleanTreeMsg{ref: "develop"})
	m = updated.(Model)
	if m.mode != viewModeCheckoutConfirm {
		t.Fatal("needs-clean-tree should open the confirm modal")
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("[s] should dispatch the stash retry cmd")
	}
	if m.pendingCheckout != (pendingCheckout{ref: "develop"}) {
		t.Errorf("pendingCheckout = %+v, want retained for the retry", m.pendingCheckout)
	}

	updated, cmd = m.Update(checkoutSucceededMsg{ref: "develop"})
	m = updated.(Model)
	if !strings.Contains(m.status, "checkout: develop") {
		t.Errorf("status = %q, want the checkout outcome", m.status)
	}
	if cmd == nil {
		t.Fatal("retry success should dispatch reload")
	}
}

// --- graph stash surface: rendering label, space pop/apply, d drop ---

func TestStashSubjectStripsConventionalPrefix(t *testing.T) {
	cases := map[string]string{
		"On main: debug logging":             "debug logging",
		"WIP on main: abc123 commit subject": "abc123 commit subject",
		"plain message, no prefix":           "plain message, no prefix",
	}
	for in, want := range cases {
		if got := stashSubject(in); got != want {
			t.Errorf("stashSubject(%q) = %q, want %q", in, got, want)
		}
	}
}

// withStashActionStubs swaps the pop / apply / drop git seams for the test;
// nil leaves a seam untouched.
func withStashActionStubs(t *testing.T, pop, apply, drop func(context.Context, string, string) error) {
	t.Helper()
	pp, pa, pd := stashPopExec, stashApplyExec, stashDropExec
	t.Cleanup(func() { stashPopExec, stashApplyExec, stashDropExec = pp, pa, pd })
	if pop != nil {
		stashPopExec = pop
	}
	if apply != nil {
		stashApplyExec = apply
	}
	if drop != nil {
		stashDropExec = drop
	}
}

// seedStashRow seeds the graph cursor on a stash row labelled stash@{0}.
func seedStashRow(t *testing.T) Model {
	t.Helper()
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m = seedRefs(t, m, []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, ObjectName: "base123", IsHead: true},
	})
	updated, _ = m.Update(commitsAppendedMsg{reqID: 1, done: true, rows: []graphRow{
		{commit: git.Commit{Hash: "stashhash", Subject: "debug logging", AuthorTime: time.Now(), RefNames: []string{"stash@{0}"}}},
	}})
	m = updated.(Model)
	updated, _ = m.Update(commitsStreamDoneMsg{reqID: 1})
	return updated.(Model)
}

func TestCursorStashLabel(t *testing.T) {
	m := seedStashRow(t)
	label, ok := m.cursorStashLabel()
	if !ok || label != "stash@{0}" {
		t.Fatalf("cursorStashLabel = (%q, %v), want (stash@{0}, true)", label, ok)
	}
}

func TestGraphSpaceOnStashOpensActionDialog(t *testing.T) {
	m := seedStashRow(t)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = updated.(Model)
	if m.mode != viewModeStashAction {
		t.Errorf("mode = %v, want viewModeStashAction", m.mode)
	}
	if m.stashTarget != "stash@{0}" {
		t.Errorf("stashTarget = %q, want stash@{0}", m.stashTarget)
	}
	if cmd != nil {
		t.Error("arming the dialog should not dispatch a cmd (no checkout evaluator)")
	}
	if m.actionInFlight {
		t.Error("space on a stash must not latch the checkout evaluator")
	}
}

func TestStashActionApplyDispatchesAndKeepsDialog(t *testing.T) {
	applied := ""
	withStashActionStubs(t, nil, func(_ context.Context, _, ref string) error {
		applied = ref
		return nil
	}, nil)

	m := seedStashRow(t)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = updated.(Model)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = updated.(Model)
	if !m.stashInFlight {
		t.Error("[a] should latch stashInFlight")
	}
	if cmd == nil {
		t.Fatal("[a] should dispatch the apply cmd")
	}
	msg := cmd()
	done, ok := msg.(stashActionDoneMsg)
	if !ok {
		t.Fatalf("apply cmd msg = %T, want stashActionDoneMsg", msg)
	}
	if applied != "stash@{0}" || done.verb != "apply" {
		t.Errorf("applied=%q verb=%q, want stash@{0}/apply", applied, done.verb)
	}

	// The done msg closes the dialog and dispatches a reload.
	updated, reload := m.Update(done)
	m = updated.(Model)
	if m.mode != viewModeNormal || m.stashInFlight {
		t.Errorf("after done: mode=%v inFlight=%v, want normal/false", m.mode, m.stashInFlight)
	}
	if reload == nil {
		t.Error("a successful stash action should reload the graph")
	}
	if !strings.Contains(m.status, "applied stash@{0}") {
		t.Errorf("status = %q, want 'applied stash@{0}'", m.status)
	}
}

func TestStashActionPopConflictReportsAndReloads(t *testing.T) {
	// A pop that conflicts leaves markers in the tree with the slot kept, so
	// the conflict reply still reloads and the status names the slot.
	m := seedStashRow(t)
	m.mode = viewModeStashAction
	m.stashInFlight = true
	m.stashTarget = "stash@{0}"

	updated, reload := m.Update(stashActionConflictMsg{verb: "pop", label: "stash@{0}", err: git.ErrStashPopConflict})
	m = updated.(Model)
	if m.mode != viewModeNormal || m.stashInFlight {
		t.Errorf("after conflict: mode=%v inFlight=%v, want normal/false", m.mode, m.stashInFlight)
	}
	if reload == nil {
		t.Error("conflict should still reload (tree changed, slot kept)")
	}
	if !strings.Contains(m.status, "conflict") || !strings.Contains(m.status, "stash@{0}") {
		t.Errorf("status = %q, want conflict guidance naming the slot", m.status)
	}
}

func TestStashActionCmdClassifiesConflictViaSentinel(t *testing.T) {
	withStashActionStubs(t, func(context.Context, string, string) error {
		return git.ErrStashPopConflict
	}, nil, nil)
	msg := stashActionCmd("pop", "/repo", "stash@{0}", stashPopExec, git.ErrStashPopConflict)()
	if _, ok := msg.(stashActionConflictMsg); !ok {
		t.Fatalf("msg = %T, want stashActionConflictMsg", msg)
	}
}

func TestGraphDOnStashOpensDropConfirm(t *testing.T) {
	dropped := ""
	withStashActionStubs(t, nil, nil, func(_ context.Context, _, ref string) error {
		dropped = ref
		return nil
	})

	m := seedStashRow(t)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m = updated.(Model)
	if m.mode != viewModeStashDropConfirm || m.stashTarget != "stash@{0}" {
		t.Fatalf("after d: mode=%v target=%q, want dropConfirm/stash@{0}", m.mode, m.stashTarget)
	}
	// Confirm with d.
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m = updated.(Model)
	if !m.stashInFlight || cmd == nil {
		t.Fatal("confirming the drop should latch inFlight and dispatch the cmd")
	}
	if _, ok := cmd().(stashActionDoneMsg); !ok {
		t.Fatal("drop cmd should produce a stashActionDoneMsg")
	}
	if dropped != "stash@{0}" {
		t.Errorf("dropped = %q, want stash@{0}", dropped)
	}
}

func TestStashDialogsEscCancel(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  rune
		want viewMode
	}{
		{"action", ' ', viewModeStashAction},
		{"drop", 'd', viewModeStashDropConfirm},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := seedStashRow(t)
			var updated tea.Model
			if tc.key == ' ' {
				updated, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
			} else {
				updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{tc.key}})
			}
			m = updated.(Model)
			if m.mode != tc.want {
				t.Fatalf("mode = %v, want %v", m.mode, tc.want)
			}
			updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
			m = updated.(Model)
			if m.mode != viewModeNormal {
				t.Errorf("esc: mode = %v, want normal", m.mode)
			}
			if m.stashTarget != "" {
				t.Errorf("esc: stashTarget = %q, want cleared", m.stashTarget)
			}
		})
	}
}
