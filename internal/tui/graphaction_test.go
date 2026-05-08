package tui

import (
	"context"
	"reflect"
	"testing"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// stubAdvances installs a counterfeit countAheadExec for one test,
// restoring the original via t.Cleanup. The stub answers in-memory so no
// test invokes a real git subprocess; "missing key" returns the divergent
// case (zero advance) rather than an error so a test can omit ancestry
// pairs it doesn't exercise.
func stubAdvances(t *testing.T, advances map[string]int) {
	t.Helper()
	prevCA := countAheadExec
	countAheadExec = func(_ context.Context, _ string, ancestor, descendant string) (int, error) {
		return advances[ancestor+"->"+descendant], nil
	}
	t.Cleanup(func() {
		countAheadExec = prevCA
	})
}

func runGraphEvaluator(t *testing.T, hash string, locals []git.Ref) graphActionMsg {
	t.Helper()
	return runGraphEvaluatorWithRemotes(t, hash, locals, nil)
}

func runGraphEvaluatorWithRemotes(t *testing.T, hash string, locals, remotes []git.Ref) graphActionMsg {
	t.Helper()
	cmd := evaluateGraphActionCmd("", hash, locals, remotes)
	if cmd == nil {
		t.Fatal("evaluateGraphActionCmd returned nil")
	}
	msg := cmd()
	got, ok := msg.(graphActionMsg)
	if !ok {
		t.Fatalf("evaluator returned %T, want graphActionMsg", msg)
	}
	return got
}

func TestGraphEvaluatorNoOpWhenHeadOnSingleChip(t *testing.T) {
	hash := "aaaa"
	locals := []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, ObjectName: hash, IsHead: true},
	}
	got := runGraphEvaluator(t, hash, locals)
	if got.kind != graphActionNoOp {
		t.Errorf("kind = %v, want NoOp", got.kind)
	}
	if got.branch != "main" {
		t.Errorf("branch = %q, want main", got.branch)
	}
}

func TestGraphEvaluatorCheckoutOnForeignSingleChip(t *testing.T) {
	hash := "bbbb"
	locals := []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, ObjectName: "aaaa", IsHead: true},
		{ShortName: "feat", Kind: git.RefKindLocal, ObjectName: hash},
	}
	got := runGraphEvaluator(t, hash, locals)
	if got.kind != graphActionCheckout {
		t.Errorf("kind = %v, want Checkout", got.kind)
	}
	if got.branch != "feat" {
		t.Errorf("branch = %q, want feat", got.branch)
	}
}

func TestGraphEvaluatorPickerOnMultipleChips(t *testing.T) {
	hash := "cccc"
	locals := []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, ObjectName: "aaaa", IsHead: true},
		{ShortName: "topic-z", Kind: git.RefKindLocal, ObjectName: hash},
		{ShortName: "topic-a", Kind: git.RefKindLocal, ObjectName: hash},
	}
	got := runGraphEvaluator(t, hash, locals)
	if got.kind != graphActionPicker {
		t.Errorf("kind = %v, want Picker", got.kind)
	}
	want := []string{"topic-a", "topic-z"}
	if !reflect.DeepEqual(got.candidates, want) {
		t.Errorf("candidates = %v, want %v (alphabetical)", got.candidates, want)
	}
}

func TestGraphEvaluatorNoOpWhenHeadAmongMultipleChips(t *testing.T) {
	// HEAD on B1 with B2 also at the same commit — plan says no-op even
	// though picker could let user switch to B2. The user can checkout B2
	// from the refs pane.
	hash := "dddd"
	locals := []git.Ref{
		{ShortName: "B1", Kind: git.RefKindLocal, ObjectName: hash, IsHead: true},
		{ShortName: "B2", Kind: git.RefKindLocal, ObjectName: hash},
	}
	got := runGraphEvaluator(t, hash, locals)
	if got.kind != graphActionNoOp {
		t.Errorf("kind = %v, want NoOp", got.kind)
	}
	if got.branch != "B1" {
		t.Errorf("branch = %q, want B1", got.branch)
	}
}

func TestGraphEvaluatorFFOnHeadAncestor(t *testing.T) {
	cursor := "eeee"
	headTip := "aaaa"
	locals := []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, ObjectName: headTip, IsHead: true},
	}
	stubAdvances(t, map[string]int{headTip + "->" + cursor: 3})
	got := runGraphEvaluator(t, cursor, locals)
	if got.kind != graphActionFF {
		t.Errorf("kind = %v, want FF", got.kind)
	}
	if got.branch != "main" {
		t.Errorf("branch = %q, want main", got.branch)
	}
	if got.advance != 3 {
		t.Errorf("advance = %d, want 3", got.advance)
	}
}

func TestGraphEvaluatorDetachWhenHeadDetached(t *testing.T) {
	cursor := "ffff"
	locals := []git.Ref{
		// No IsHead=true → HEAD is detached.
		{ShortName: "main", Kind: git.RefKindLocal, ObjectName: "aaaa"},
	}
	got := runGraphEvaluator(t, cursor, locals)
	if got.kind != graphActionDetach {
		t.Errorf("kind = %v, want Detach", got.kind)
	}
}

func TestGraphEvaluatorDetachWhenAdvanceZero(t *testing.T) {
	// CountAhead returns 0 for both "behind ancestor" and "diverged" —
	// the evaluator downgrades both cases to Detach.
	cursor := "gggg"
	headTip := "aaaa"
	locals := []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, ObjectName: headTip, IsHead: true},
	}
	stubAdvances(t, map[string]int{headTip + "->" + cursor: 0})
	got := runGraphEvaluator(t, cursor, locals)
	if got.kind != graphActionDetach {
		t.Errorf("kind = %v, want Detach", got.kind)
	}
}

func TestGraphEvaluatorRemoteChipFallsThroughToFFWhenHeadIsTracker(t *testing.T) {
	// Cursor row has origin/main; HEAD is local main with Upstream
	// pointing at origin/main. Cross-branch path skips main (HEAD is the
	// tracker), so we fall through to plain FF on HEAD's branch.
	cursor := "iiii"
	headTip := "aaaa"
	locals := []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, ObjectName: headTip, IsHead: true, Upstream: "origin/main"},
	}
	remotes := []git.Ref{
		{ShortName: "origin/main", Kind: git.RefKindRemote, ObjectName: cursor},
	}
	stubAdvances(t, map[string]int{headTip + "->" + cursor: 5})
	got := runGraphEvaluatorWithRemotes(t, cursor, locals, remotes)
	if got.kind != graphActionFF {
		t.Errorf("kind = %v, want FF (HEAD on tracker → plain FF, not cross-branch)", got.kind)
	}
	if got.advance != 5 {
		t.Errorf("advance = %d, want 5", got.advance)
	}
}

func TestGraphEvaluatorRemoteChipDispatchesCrossBranchFF(t *testing.T) {
	// Cursor row has origin/develop; HEAD is on feat/foo (different
	// branch). Local develop has Upstream=origin/develop. → checkout
	// develop + FF (Fork "Checkout & Fast Forward"). No git call needed
	// for advance — the FF cmd computes it post-checkout.
	cursor := "jjjj"
	locals := []git.Ref{
		{ShortName: "develop", Kind: git.RefKindLocal, ObjectName: "behind", Upstream: "origin/develop"},
		{ShortName: "feat/foo", Kind: git.RefKindLocal, ObjectName: "feattip", IsHead: true},
	}
	remotes := []git.Ref{
		{ShortName: "origin/develop", Kind: git.RefKindRemote, ObjectName: cursor},
	}
	got := runGraphEvaluatorWithRemotes(t, cursor, locals, remotes)
	if got.kind != graphActionCheckoutAndFF {
		t.Errorf("kind = %v, want CheckoutAndFF", got.kind)
	}
	if got.branch != "develop" {
		t.Errorf("branch = %q, want develop (the upstream-tracking local)", got.branch)
	}
}

func TestGraphEvaluatorCrossBranchPicksAlphabeticalOnMulti(t *testing.T) {
	// Two locals (develop-backup, develop) both track origin/develop. HEAD
	// is on feat/foo so neither is HEAD. Pick alphabetical: "develop".
	cursor := "kkkk"
	locals := []git.Ref{
		{ShortName: "develop-backup", Kind: git.RefKindLocal, ObjectName: "x", Upstream: "origin/develop"},
		{ShortName: "develop", Kind: git.RefKindLocal, ObjectName: "y", Upstream: "origin/develop"},
		{ShortName: "feat/foo", Kind: git.RefKindLocal, ObjectName: "z", IsHead: true},
	}
	remotes := []git.Ref{
		{ShortName: "origin/develop", Kind: git.RefKindRemote, ObjectName: cursor},
	}
	got := runGraphEvaluatorWithRemotes(t, cursor, locals, remotes)
	if got.kind != graphActionCheckoutAndFF {
		t.Errorf("kind = %v, want CheckoutAndFF", got.kind)
	}
	if got.branch != "develop" {
		t.Errorf("branch = %q, want develop (alphabetical first)", got.branch)
	}
}

func TestGraphEvaluatorCrossBranchFiresEvenWhenHeadDetached(t *testing.T) {
	// HEAD detached + cursor on origin/develop chip + local develop with
	// matching upstream → checkout develop + FF. Better UX than detaching
	// to the cursor commit.
	cursor := "llll"
	locals := []git.Ref{
		// No IsHead=true → detached.
		{ShortName: "develop", Kind: git.RefKindLocal, ObjectName: "behind", Upstream: "origin/develop"},
	}
	remotes := []git.Ref{
		{ShortName: "origin/develop", Kind: git.RefKindRemote, ObjectName: cursor},
	}
	got := runGraphEvaluatorWithRemotes(t, cursor, locals, remotes)
	if got.kind != graphActionCheckoutAndFF {
		t.Errorf("kind = %v, want CheckoutAndFF (detached HEAD should still cross-branch)", got.kind)
	}
	if got.branch != "develop" {
		t.Errorf("branch = %q, want develop", got.branch)
	}
}

func TestGraphEvaluatorRemoteChipWithoutTrackerFallsThroughToDetach(t *testing.T) {
	// Cursor has origin/dangling chip but no local has Upstream pointing
	// to it. No cross-branch candidate. HEAD is on feat/foo, advance = 0
	// (cursor not descendant of feat/foo) → detach.
	cursor := "mmmm"
	headTip := "aaaa"
	locals := []git.Ref{
		{ShortName: "feat/foo", Kind: git.RefKindLocal, ObjectName: headTip, IsHead: true},
	}
	remotes := []git.Ref{
		{ShortName: "origin/dangling", Kind: git.RefKindRemote, ObjectName: cursor},
	}
	stubAdvances(t, map[string]int{headTip + "->" + cursor: 0})
	got := runGraphEvaluatorWithRemotes(t, cursor, locals, remotes)
	if got.kind != graphActionDetach {
		t.Errorf("kind = %v, want Detach", got.kind)
	}
}

func TestGraphEvaluatorEchoesHash(t *testing.T) {
	// hash is echoed in every msg so the model can drop stale evaluations.
	hash := "stale-vs-fresh"
	locals := []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, ObjectName: "aaaa", IsHead: true},
	}
	stubAdvances(t, map[string]int{}) // missing key → 0 → Detach
	got := runGraphEvaluator(t, hash, locals)
	if got.hash != hash {
		t.Errorf("hash = %q, want %q (evaluator must echo for stale-drop)", got.hash, hash)
	}
}
