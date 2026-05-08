package tui

import (
	"context"
	"log"
	"sort"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// graphActionKind partitions the six outcomes of pressing Enter on the
// graph pane. The evaluator (evaluateGraphActionCmd) decides which kind
// applies; the model's graphActionMsg handler dispatches to the matching
// command (or modal mode).
type graphActionKind int

const (
	// graphActionNoOp fires when the cursor's row carries the same local
	// branch HEAD is already on. Status surfaces "already on <branch>" and
	// no git invocation runs.
	graphActionNoOp graphActionKind = iota
	// graphActionCheckout fires when exactly one local-branch chip sits on
	// the cursor row and HEAD is on a different branch (or is detached).
	graphActionCheckout
	// graphActionPicker fires when multiple local-branch chips share the
	// cursor row and HEAD is on none of them. The model enters
	// viewModeBranchPicker; the user picks one with j/k+enter.
	graphActionPicker
	// graphActionFF fires when the cursor row has no local-branch chip
	// (mid-commit or remote-only) and HEAD's tip is a strict ancestor of
	// the cursor commit. Drives ffOnlyCmd to advance HEAD's branch.
	graphActionFF
	// graphActionCheckoutAndFF fires for the cross-branch case: cursor
	// row has a remote chip (e.g., origin/develop) whose upstream-tracking
	// local (e.g., develop) is NOT HEAD. Drives checkoutThenFFCmd —
	// checkout the local then FF it to the cursor. Fork's "Checkout & Fast
	// Forward" intent, made explicit instead of relying on the chipless
	// FF path to coincidentally do the right thing.
	graphActionCheckoutAndFF
	// graphActionDetach fires when none of the above apply: no chip and
	// either HEAD detached + no cross-branch candidate, or HEAD not an
	// ancestor of cursor. Drives CheckoutDetached on the cursor hash.
	graphActionDetach
)

// graphActionMsg is the evaluator's reply. Fields are populated by kind:
//   - NoOp / Checkout / FF: branch is set (the relevant local-branch name).
//   - FF: advance is the +N commit count between HEAD's tip and the cursor.
//   - Picker: candidates is the sorted list of local-branch ShortNames at
//     the cursor row.
//   - Detach: only hash is consulted by the handler.
//
// hash echoes the cursor commit the evaluation ran against. The model's
// dispatch checks msg.hash against the current cursor before acting so a
// stale evaluation (from an Enter pressed before the cursor moved) is
// dropped instead of acting on the wrong row.
type graphActionMsg struct {
	hash       string
	kind       graphActionKind
	branch     string
	advance    int
	candidates []string
}

// branchPickerState backs viewModeBranchPicker. Reset to the zero value
// when the picker exits (esc / enter); the picker reads candidates+cursor
// to render and writes a refCheckoutRequestedMsg-like dispatch on enter.
type branchPickerState struct {
	candidates []string
	cursor     int
	hash       string
}

// evaluateGraphActionCmd runs the full Enter decision tree on a goroutine
// so the model's Update never blocks on git. The decision splits into
// chip-driven (Checkout / Picker / NoOp) and chipless (CheckoutAndFF /
// FF / Detach) halves; only the chipless half ever invokes git, and only
// once (CountAhead). HEAD info and local chips are gathered in a single
// pass over the locals slice.
//
// Chipless precedence: a remote chip whose upstream-tracking local isn't
// HEAD wins (Fork's "Checkout & Fast Forward" — go to that local and
// advance it). Otherwise fall back to advancing HEAD's branch, or a
// final detach.
//
// A detached HEAD shows as "no ref with IsHead=true" → empty headBranch.
// Cross-branch can still fire then (Enter on origin/develop while
// detached → checkout local develop + FF). Divergent cursor (HEAD shares
// an ancestor but neither is reachable from the other) dispatches as FF
// and surfaces as ffFailedMsg with ErrFFNotPossible — by design, so the
// user sees the rejection reason instead of a silent detach.
func evaluateGraphActionCmd(dir, hash string, locals, remotes []git.Ref) tea.Cmd {
	return func() tea.Msg {
		var headBranch, headHash string
		var chips []string
		for _, r := range locals {
			if r.IsHead {
				headBranch = r.ShortName
				headHash = r.ObjectName
			}
			if r.ObjectName == hash {
				chips = append(chips, r.ShortName)
			}
		}
		sort.Strings(chips)

		if len(chips) > 0 {
			if headBranch != "" {
				for _, b := range chips {
					if b == headBranch {
						return graphActionMsg{hash: hash, kind: graphActionNoOp, branch: headBranch}
					}
				}
			}
			if len(chips) == 1 {
				return graphActionMsg{hash: hash, kind: graphActionCheckout, branch: chips[0]}
			}
			return graphActionMsg{hash: hash, kind: graphActionPicker, candidates: chips}
		}

		// Cross-branch path: cursor row carries a remote chip whose
		// upstream-tracking local is something other than HEAD. Pick the
		// alphabetically first matching local — picker for cross-branch is
		// out of scope (rare; refs panel `p` still works for explicit choice).
		if crossBranch := findCrossBranchTarget(hash, locals, remotes, headBranch); crossBranch != "" {
			log.Printf("graph enter: checkout+ff (%s → %s)", crossBranch, shortHash(hash))
			return graphActionMsg{hash: hash, kind: graphActionCheckoutAndFF, branch: crossBranch}
		}

		if headBranch == "" || headHash == "" {
			log.Printf("graph enter: detach (no HEAD branch in locals; HEAD likely detached)")
			return graphActionMsg{hash: hash, kind: graphActionDetach}
		}
		ctx, cancel := context.WithTimeout(context.Background(), checkoutTimeout)
		defer cancel()
		advance, err := countAheadExec(ctx, dir, headHash, hash)
		if err != nil {
			log.Printf("graph enter: detach (rev-list --count %s..%s failed: %v)", shortHash(headHash), shortHash(hash), err)
			return graphActionMsg{hash: hash, kind: graphActionDetach}
		}
		if advance == 0 {
			log.Printf("graph enter: detach (advance=0; %s..%s — cursor not strictly ahead of HEAD %s)",
				shortHash(headHash), shortHash(hash), headBranch)
			return graphActionMsg{hash: hash, kind: graphActionDetach}
		}
		log.Printf("graph enter: ff (%s +%d, %s..%s)",
			headBranch, advance, shortHash(headHash), shortHash(hash))
		return graphActionMsg{hash: hash, kind: graphActionFF, branch: headBranch, advance: advance}
	}
}

// findCrossBranchTarget returns the local branch name to checkout-and-FF
// when the cursor row has only remote chips. For each remote chip on the
// cursor row, we look for a local whose Upstream points at that remote;
// HEAD's own branch is excluded so the chipless FF path can handle "FF
// my own branch" without going through the cross-branch chain. Multiple
// candidates are resolved alphabetically — the picker UX is reserved for
// multi-local-chip rows where the choice is genuinely ambiguous.
func findCrossBranchTarget(hash string, locals, remotes []git.Ref, headBranch string) string {
	var picked string
	for _, r := range remotes {
		if r.ObjectName != hash {
			continue
		}
		for _, l := range locals {
			if l.Upstream != r.ShortName || l.ShortName == headBranch {
				continue
			}
			if picked == "" || l.ShortName < picked {
				picked = l.ShortName
			}
		}
	}
	return picked
}
