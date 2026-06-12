package tui

import (
	"context"
	"log"
	"sort"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// graphActionKind partitions the outcomes of pressing Enter on the graph
// pane. The evaluator (evaluateGraphActionCmd) decides which kind applies;
// the model's graphActionMsg handler dispatches to the matching command
// (or modal mode).
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
	// pullAfter marks an action that started from a remote chip on the
	// cursor row (origin/xx). The outcome handlers chain a `git pull`
	// after the checkout/FF lands so "enter on origin/xx" means "get me
	// onto that branch, synced with the network" — not just synced with
	// the last-fetch snapshot the graph happens to show.
	pullAfter bool
}

// branchPickerState backs viewModeBranchPicker. Reset to the zero value
// when the picker exits (esc / enter); the picker reads candidates+cursor
// to render and writes a refCheckoutRequestedMsg-like dispatch on enter.
//
// viewportTop is the index of the first candidate visible inside the
// scroll window. The picker's renderer caps visible rows at ~70% of the
// screen height; j/k handlers slide viewportTop so the cursor stays in
// the window. Zero value (0) is the natural top-of-list start.
type branchPickerState struct {
	candidates  []string
	cursor      int
	hash        string
	viewportTop int
}

// scrollIntoView slides viewportTop so the current cursor sits inside the
// [viewportTop, viewportTop+visibleRows) window. Called from the j/k
// handlers after the cursor moves; visibleRows is computed by
// branchPickerVisibleRows from the model's screen height.
func (s *branchPickerState) scrollIntoView(visibleRows int) {
	if visibleRows <= 0 {
		s.viewportTop = s.cursor
		return
	}
	if s.cursor < s.viewportTop {
		s.viewportTop = s.cursor
	} else if s.cursor >= s.viewportTop+visibleRows {
		s.viewportTop = s.cursor - visibleRows + 1
	}
	if s.viewportTop < 0 {
		s.viewportTop = 0
	}
}

// branchPickerVisibleRows caps the candidate row count at ~70% of the
// screen height minus the modal box's chrome and the picker's fixed
// header + hint + two scroll-marker rows ("↑ N more" / "↓ N more"
// always reserved so the cap stays valid even when overflow kicks in).
// On terminals too small to fit the cap math the floor is 1 — small
// terminals stay usable instead of squashing the box flat.
const (
	branchPickerHeaderRows  = 1
	branchPickerHintRows    = 1
	branchPickerMarkerRows  = 2
	branchPickerHeightRatio = 7
	branchPickerHeightDenom = 10
)

// branchPickerVisibleRows returns the count of candidate rows that fit
// inside the picker's inner content area for a given screen height and
// candidate count. The result is always ≥ 1 so the cursor is reachable.
func branchPickerVisibleRows(screenH, candidates int) int {
	chromeH := modalBoxStyle.GetVerticalFrameSize()
	rows := screenH*branchPickerHeightRatio/branchPickerHeightDenom -
		chromeH - branchPickerHeaderRows - branchPickerHintRows - branchPickerMarkerRows
	if rows < 1 {
		rows = 1
	}
	if candidates < rows {
		return candidates
	}
	return rows
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
		hasRemoteChip := false
		for _, r := range remotes {
			if r.ObjectName == hash {
				hasRemoteChip = true
				break
			}
		}
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
			return graphActionMsg{hash: hash, kind: graphActionCheckoutAndFF, branch: crossBranch, pullAfter: true}
		}

		// New-local path: cursor has a remote chip with no upstream-tracking
		// local. Hand `git checkout <stripped name>` to dwim, which creates
		// the tracking local and switches to it. The new branch is created
		// at the remote's tip, which equals the cursor commit, so no FF is
		// needed afterward.
		if newLocal := findRemoteCheckoutTarget(hash, locals, remotes); newLocal != "" {
			log.Printf("graph enter: checkout (dwim from remote → %s)", newLocal)
			return graphActionMsg{hash: hash, kind: graphActionCheckout, branch: newLocal, pullAfter: true}
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
		return graphActionMsg{hash: hash, kind: graphActionFF, branch: headBranch, advance: advance, pullAfter: hasRemoteChip}
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

// findRemoteCheckoutTarget returns the dwim checkout target derived from
// a remote chip on the cursor row when no local already tracks that
// remote. The returned name is what `git checkout` will turn into a
// local tracking branch — git's dwim creates one when no same-name
// local exists, and just switches when one does. Returns "" if the
// cursor has no remote chip or every remote chip is already tracked.
//
// findCrossBranchTarget gets first refusal in the evaluator, so this
// helper only runs when no upstream-tracker exists; the dwim outcome
// depends on whether a same-name local exists at all (separate from the
// tracker check).
func findRemoteCheckoutTarget(hash string, locals, remotes []git.Ref) string {
	var picked string
	for _, r := range remotes {
		if r.ObjectName != hash {
			continue
		}
		tracked := false
		for _, l := range locals {
			if l.Upstream == r.ShortName {
				tracked = true
				break
			}
		}
		if tracked {
			continue
		}
		target := git.CheckoutTarget(r)
		if picked == "" || target < picked {
			picked = target
		}
	}
	return picked
}
