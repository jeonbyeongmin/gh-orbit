package tui

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// stubAncestry installs counterfeit isAncestorExec / countAheadExec for one
// test, restoring the originals via t.Cleanup. The stubs answer in-memory
// so no test invokes a real git subprocess.
func stubAncestry(t *testing.T, ancestors map[string]bool, advances map[string]int) {
	t.Helper()
	prevIA, prevCA := isAncestorExec, countAheadExec
	isAncestorExec = func(_ context.Context, _ string, ancestor, descendant string) (bool, error) {
		key := ancestor + "->" + descendant
		v, ok := ancestors[key]
		if !ok {
			return false, errors.New("stubAncestry: missing key " + key)
		}
		return v, nil
	}
	countAheadExec = func(_ context.Context, _ string, ancestor, descendant string) (int, error) {
		key := ancestor + "->" + descendant
		v, ok := advances[key]
		if !ok {
			return 0, errors.New("stubAncestry: missing advance key " + key)
		}
		return v, nil
	}
	t.Cleanup(func() {
		isAncestorExec = prevIA
		countAheadExec = prevCA
	})
}

func runGraphEvaluator(t *testing.T, hash string, locals []git.Ref) graphActionMsg {
	t.Helper()
	cmd := evaluateGraphActionCmd("", hash, locals)
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
	stubAncestry(t,
		map[string]bool{headTip + "->" + cursor: true},
		map[string]int{headTip + "->" + cursor: 3},
	)
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

func TestGraphEvaluatorDetachWhenHeadNotAncestor(t *testing.T) {
	cursor := "gggg"
	headTip := "aaaa"
	locals := []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, ObjectName: headTip, IsHead: true},
	}
	stubAncestry(t,
		map[string]bool{headTip + "->" + cursor: false},
		map[string]int{},
	)
	got := runGraphEvaluator(t, cursor, locals)
	if got.kind != graphActionDetach {
		t.Errorf("kind = %v, want Detach", got.kind)
	}
}

func TestGraphEvaluatorDetachOnAdvanceZero(t *testing.T) {
	// Defensive — IsAncestor true but rev-list returns 0. The evaluator
	// downgrades to Detach instead of dispatching a no-progress FF.
	cursor := "hhhh"
	headTip := "aaaa"
	locals := []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, ObjectName: headTip, IsHead: true},
	}
	stubAncestry(t,
		map[string]bool{headTip + "->" + cursor: true},
		map[string]int{headTip + "->" + cursor: 0},
	)
	got := runGraphEvaluator(t, cursor, locals)
	if got.kind != graphActionDetach {
		t.Errorf("kind = %v, want Detach", got.kind)
	}
}

func TestGraphEvaluatorRemoteChipFallsThroughToFF(t *testing.T) {
	// Cursor row carries only an origin/main remote chip (no local chip
	// here because local main is on a behind row). HEAD on local main →
	// natural FF, the Fork "Checkout & Fast Forward" path.
	cursor := "iiii"
	headTip := "aaaa"
	locals := []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, ObjectName: headTip, IsHead: true},
	}
	// Note: remotes aren't in the locals slice the evaluator scans, so we
	// just don't include the origin/main entry — graphaction only consumes
	// locals.
	stubAncestry(t,
		map[string]bool{headTip + "->" + cursor: true},
		map[string]int{headTip + "->" + cursor: 5},
	)
	got := runGraphEvaluator(t, cursor, locals)
	if got.kind != graphActionFF {
		t.Errorf("kind = %v, want FF (remote chip should not block FF path)", got.kind)
	}
	if got.advance != 5 {
		t.Errorf("advance = %d, want 5", got.advance)
	}
}

func TestGraphEvaluatorEchoesHash(t *testing.T) {
	// hash is echoed in every msg so the model can drop stale evaluations.
	hash := "stale-vs-fresh"
	locals := []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, ObjectName: "aaaa", IsHead: true},
	}
	stubAncestry(t,
		map[string]bool{"aaaa->" + hash: false},
		map[string]int{},
	)
	got := runGraphEvaluator(t, hash, locals)
	if got.hash != hash {
		t.Errorf("hash = %q, want %q (evaluator must echo for stale-drop)", got.hash, hash)
	}
}
