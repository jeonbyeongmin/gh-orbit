package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

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
			m.pullAfterAction = true

			updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
			m = updated.(Model)

			if m.mode != viewModeNormal {
				t.Errorf("mode = %v, want viewModeNormal", m.mode)
			}
			if !m.pullAfterAction {
				t.Error("[s] must not clear pullAfterAction — the retry carries the pull chain")
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
	m.pullAfterAction = true
	m.pendingCheckout = pendingCheckout{ref: "feat"}
	m.stashNotice = "develop"

	updated, cmd := m.Update(stashFailedMsg{err: errors.New("stash refused")})
	m = updated.(Model)

	if cmd != nil {
		t.Error("stash failure should not dispatch a follow-up cmd")
	}
	if m.checkoutInFlight || m.ffInFlight || m.pullAfterAction {
		t.Errorf("gates should clear (checkout=%v ff=%v pullAfter=%v)",
			m.checkoutInFlight, m.ffInFlight, m.pullAfterAction)
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

// TestModelStashCarriesPullAfterChain asserts the remote-chip Enter's
// pull-after arm survives the dirty-tree detour when the user picks `s`:
// the retried checkout's success must still chain the pull.
func TestModelStashCarriesPullAfterChain(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.pullAfterAction = true
	m.checkoutInFlight = true
	m.pendingCheckout = pendingCheckout{ref: "develop"}

	updated, _ = m.Update(checkoutNeedsCleanTreeMsg{ref: "develop"})
	m = updated.(Model)
	if !m.pullAfterAction {
		t.Fatal("detour should keep pullAfterAction armed")
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("[s] should dispatch the stash retry cmd")
	}
	if !m.pullAfterAction {
		t.Fatal("[s] should carry pullAfterAction into the retry")
	}

	updated, cmd = m.Update(checkoutSucceededMsg{ref: "develop"})
	m = updated.(Model)
	if !m.pullInFlight {
		t.Error("retry success should chain the pull")
	}
	if !strings.Contains(m.status, "pulling…") {
		t.Errorf("status = %q, want the chained pull surfaced", m.status)
	}
	if cmd == nil {
		t.Fatal("retry success should return reload+pull batch")
	}
}
