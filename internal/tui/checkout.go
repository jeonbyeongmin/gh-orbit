package tui

import (
	"context"
	"errors"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// checkoutTimeout is shorter than pull's 5min — checkout is local-only and
// shouldn't block on network. A long-running case usually means index.lock
// contention; bailing keeps the TUI responsive.
const checkoutTimeout = 60 * time.Second

// stashLabelHEAD is the conventional ref the newest stash entry occupies.
// We surface this string back to the user instead of resolving the actual
// ref — git re-indexes stashes on push, so in a single-user TUI the newest
// is always slot 0.
const stashLabelHEAD = "stash@{0}"

type checkoutSucceededMsg struct {
	ref      string
	detached bool
}

type checkoutFailedMsg struct{ err error }

type checkoutNeedsCleanTreeMsg struct {
	ref      string
	detached bool
}

type stashThenCheckoutMsg struct {
	ref        string
	detached   bool
	stashLabel string
}

// Package-level seams over git.* so tests can stub the subprocess calls
// without touching a real repo. Mirrors pullExec / pullResolveStrategy.
var (
	checkoutExec         = git.Checkout
	checkoutDetachedExec = git.CheckoutDetached
	stashExec            = git.Stash
)

func checkoutCmd(dir, ref string, detached bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), checkoutTimeout)
		defer cancel()
		err := runCheckoutExec(ctx, dir, ref, detached)
		if err == nil {
			return checkoutSucceededMsg{ref: ref, detached: detached}
		}
		if errors.Is(err, git.ErrCheckoutNeedsCleanTree) {
			return checkoutNeedsCleanTreeMsg{ref: ref, detached: detached}
		}
		return checkoutFailedMsg{err: err}
	}
}

func stashThenCheckoutCmd(dir, ref string, detached bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), checkoutTimeout)
		defer cancel()
		if err := stashExec(ctx, dir, "gh-orbit: before checkout "+ref); err != nil {
			return checkoutFailedMsg{err: err}
		}
		if err := runCheckoutExec(ctx, dir, ref, detached); err != nil {
			return checkoutFailedMsg{err: err}
		}
		return stashThenCheckoutMsg{ref: ref, detached: detached, stashLabel: stashLabelHEAD}
	}
}

func runCheckoutExec(ctx context.Context, dir, ref string, detached bool) error {
	if detached {
		return checkoutDetachedExec(ctx, dir, ref)
	}
	return checkoutExec(ctx, dir, ref)
}
