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
	// graphActionNoOp fires when HEAD is already on a branch sitting on the
	// cursor row. Status surfaces "already on <branch>" and no git runs.
	graphActionNoOp graphActionKind = iota
	// graphActionCheckout fires when the lone row candidate is a local
	// branch already on the cursor commit, or a remote name with no local
	// yet (git dwim-creates it at the remote tip = cursor). Plain switch —
	// it already lands synced, so no FF follows.
	graphActionCheckout
	// graphActionPicker fires when the cursor row resolves to several
	// distinct candidates (local chips plus remote chips' tracking locals,
	// deduped). The model enters viewModeBranchPicker; the user picks one
	// with j/k+enter, which checks out + fast-forwards the chosen branch.
	graphActionPicker
	// graphActionFF fires when the action advances HEAD's own branch up to
	// the cursor (a mid-commit row strictly ahead, or a remote chip whose
	// tracking local IS HEAD). Drives ffOnlyCmd.
	graphActionFF
	// graphActionCheckoutAndFF fires when the lone candidate is an existing
	// local branch sitting off the cursor row (e.g. Space on origin/develop
	// while local develop is behind, from another branch). Drives
	// checkoutThenFFCmd — checkout the local then FF it up to the cursor
	// (the remote tip) so the switch lands synced.
	graphActionCheckoutAndFF
	// graphActionDetach fires when the cursor row has no branch candidate
	// and either HEAD is detached or HEAD is not an ancestor of the cursor.
	// Drives CheckoutDetached on the cursor hash.
	graphActionDetach
)

// graphActionMsg is the evaluator's reply. Fields are populated by kind:
//   - NoOp / Checkout / FF / CheckoutAndFF: branch is the target local name.
//   - FF: advance is the +N commit count between HEAD's tip and the cursor.
//   - Picker: candidates is the sorted, deduped list of local names the
//     cursor row resolves to (local chips + remote chips' tracking locals).
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

// evaluateGraphActionCmd runs the full Space/Enter decision tree on a
// goroutine so the model's Update never blocks on git. It resolves the
// cursor row to a set of checkout candidates — every local-branch chip
// plus the local each remote chip maps onto (its upstream-tracking local,
// or git's dwim name-strip when none tracks it) — and dispatches:
//
//   - already on a branch sitting on the cursor row → NoOp.
//   - one candidate → switch to it. A remote-derived candidate whose local
//     sits off the row checks out then fast-forwards up to the cursor (the
//     remote tip) so "Space on origin/xx" lands on a synced local xx. No
//     network pull/rebase runs — the FF to the fetched remote tip is the sync.
//   - several distinct candidates → Picker (local + remote names, deduped).
//   - no candidate (mid-commit / unrelated row) → FF HEAD's own branch up to
//     the cursor if it's strictly ahead, else detach.
//
// A detached HEAD shows as "no ref with IsHead=true" → empty headBranch;
// the remote-derived checkout+FF still fires then. A divergent cursor (FF
// not possible) surfaces later as ffFailedMsg with ErrFFNotPossible — by
// design, so the user sees the rejection instead of a silent detach.
func evaluateGraphActionCmd(dir, hash string, locals, remotes []git.Ref) tea.Cmd {
	return func() tea.Msg {
		var headBranch, headHash string
		localChip := map[string]bool{}   // local-branch names on the cursor row
		localExists := map[string]bool{} // every local-branch name
		for _, r := range locals {
			localExists[r.ShortName] = true
			if r.IsHead {
				headBranch = r.ShortName
				headHash = r.ObjectName
			}
			if r.ObjectName == hash {
				localChip[r.ShortName] = true
			}
		}

		// Already standing on a branch that sits exactly on the cursor row —
		// nothing to switch to or advance.
		if headBranch != "" && headHash == hash {
			return graphActionMsg{hash: hash, kind: graphActionNoOp, branch: headBranch}
		}

		candSet := map[string]bool{}
		for name := range localChip {
			candSet[name] = true
		}
		for _, r := range remotes {
			if r.ObjectName == hash {
				candSet[remoteCheckoutCandidate(r, locals)] = true
			}
		}

		if len(candSet) == 0 {
			return chiplessAction(dir, hash, headBranch, headHash)
		}

		candidates := make([]string, 0, len(candSet))
		for name := range candSet {
			candidates = append(candidates, name)
		}
		sort.Strings(candidates)

		if len(candidates) > 1 {
			log.Printf("graph space: picker (%v)", candidates)
			return graphActionMsg{hash: hash, kind: graphActionPicker, candidates: candidates}
		}
		return singleCandidateAction(dir, hash, candidates[0], headBranch, headHash, localChip, localExists)
	}
}

// singleCandidateAction resolves the lone checkout candidate on the cursor
// row to a concrete kind. b == HEAD's branch advances it (FF) when the
// cursor is strictly ahead; a local already on the row (or a name git will
// dwim-create at the remote tip) is a plain checkout; an existing local
// sitting off the row is checked out then fast-forwarded up to the cursor.
func singleCandidateAction(dir, hash, b, headBranch, headHash string, localChip, localExists map[string]bool) graphActionMsg {
	if b == headBranch {
		advance, err := countAhead(dir, headHash, hash)
		if err != nil || advance == 0 {
			log.Printf("graph space: noop (%s already at/ahead of cursor)", headBranch)
			return graphActionMsg{hash: hash, kind: graphActionNoOp, branch: headBranch}
		}
		log.Printf("graph space: ff (%s +%d)", headBranch, advance)
		return graphActionMsg{hash: hash, kind: graphActionFF, branch: headBranch, advance: advance}
	}
	if localChip[b] || !localExists[b] {
		log.Printf("graph space: checkout (%s)", b)
		return graphActionMsg{hash: hash, kind: graphActionCheckout, branch: b}
	}
	log.Printf("graph space: checkout+ff (%s → %s)", b, shortHash(hash))
	return graphActionMsg{hash: hash, kind: graphActionCheckoutAndFF, branch: b}
}

// chiplessAction handles a cursor row carrying no branch chip (a mid-commit
// or an unrelated tip): advance HEAD's own branch when the cursor is
// strictly ahead, otherwise detach onto the cursor commit.
func chiplessAction(dir, hash, headBranch, headHash string) graphActionMsg {
	if headBranch == "" || headHash == "" {
		log.Printf("graph space: detach (HEAD detached, no candidate)")
		return graphActionMsg{hash: hash, kind: graphActionDetach}
	}
	advance, err := countAhead(dir, headHash, hash)
	if err != nil || advance == 0 {
		log.Printf("graph space: detach (%s not strictly ahead of HEAD %s)", shortHash(hash), headBranch)
		return graphActionMsg{hash: hash, kind: graphActionDetach}
	}
	log.Printf("graph space: ff (%s +%d)", headBranch, advance)
	return graphActionMsg{hash: hash, kind: graphActionFF, branch: headBranch, advance: advance}
}

// countAhead wraps countAheadExec with the shared checkout timeout. It
// returns the number of commits in headHash..hash (how far the cursor is
// ahead of HEAD's tip); 0 means not strictly ahead (behind or diverged).
func countAhead(dir, ancestor, descendant string) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), checkoutTimeout)
	defer cancel()
	return countAheadExec(ctx, dir, ancestor, descendant)
}

// remoteCheckoutCandidate maps a remote chip to the local-branch name a
// checkout should land on: the upstream-tracking local if one exists
// (alphabetically first when several track it), else git's dwim name-strip
// (origin/develop → develop), which git turns into a new tracking local.
func remoteCheckoutCandidate(remote git.Ref, locals []git.Ref) string {
	var tracker string
	for _, l := range locals {
		if l.Upstream == remote.ShortName && (tracker == "" || l.ShortName < tracker) {
			tracker = l.ShortName
		}
	}
	if tracker != "" {
		return tracker
	}
	return git.CheckoutTarget(remote)
}
