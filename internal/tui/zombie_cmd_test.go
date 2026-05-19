package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

type zombieStubs struct {
	detect   func(ctx context.Context, dir, baseline string) ([]git.ZombieBranch, error)
	baseline func(ctx context.Context, dir string) string
}

func withZombieStubs(t *testing.T, s zombieStubs) {
	t.Helper()
	prevDetect := zombieDetectExec
	prevBaseline := zombieBaselineExec
	t.Cleanup(func() {
		zombieDetectExec = prevDetect
		zombieBaselineExec = prevBaseline
	})
	if s.detect != nil {
		zombieDetectExec = s.detect
	}
	if s.baseline != nil {
		zombieBaselineExec = s.baseline
	}
}

func TestDetectZombieBranchesCmdSurfacesEmpty(t *testing.T) {
	withZombieStubs(t, zombieStubs{
		baseline: func(context.Context, string) string { return "develop" },
		detect:   func(context.Context, string, string) ([]git.ZombieBranch, error) { return nil, nil },
	})
	msg, ok := detectZombieBranchesCmd("dir")().(zombieDetectedMsg)
	if !ok {
		t.Fatalf("expected zombieDetectedMsg, got different type")
	}
	if msg.baseline != "develop" {
		t.Errorf("baseline = %q, want develop", msg.baseline)
	}
	if len(msg.branches) != 0 {
		t.Errorf("branches = %+v, want empty", msg.branches)
	}
}

func TestDetectZombieBranchesCmdSurfacesList(t *testing.T) {
	withZombieStubs(t, zombieStubs{
		baseline: func(context.Context, string) string { return "main" },
		detect: func(context.Context, string, string) ([]git.ZombieBranch, error) {
			return []git.ZombieBranch{{Name: "feat-x"}, {Name: "feat-y"}}, nil
		},
	})
	msg, ok := detectZombieBranchesCmd("dir")().(zombieDetectedMsg)
	if !ok {
		t.Fatalf("expected zombieDetectedMsg")
	}
	if msg.baseline != "main" {
		t.Errorf("baseline = %q, want main", msg.baseline)
	}
	if len(msg.branches) != 2 {
		t.Errorf("got %d branches, want 2", len(msg.branches))
	}
}

func TestDetectZombieBranchesCmdSurfacesError(t *testing.T) {
	withZombieStubs(t, zombieStubs{
		baseline: func(context.Context, string) string { return "develop" },
		detect: func(context.Context, string, string) ([]git.ZombieBranch, error) {
			return nil, errors.New("git for-each-ref boom")
		},
	})
	msg, ok := detectZombieBranchesCmd("dir")().(zombieDetectFailedMsg)
	if !ok {
		t.Fatalf("expected zombieDetectFailedMsg")
	}
	if !strings.Contains(msg.err.Error(), "boom") {
		t.Errorf("error = %v, want substring 'boom'", msg.err)
	}
}

func TestDeleteZombieBranchesCmdSplitsSuccessAndFailure(t *testing.T) {
	withRefsActionStubs(t, refsActionStubs{
		branchDelete: func(_ context.Context, _, name string, force bool) error {
			if force {
				t.Errorf("force=true should never propagate from bulk zombie delete")
			}
			if name == "feat-bad" {
				return errors.New("not fully merged")
			}
			return nil
		},
	})
	branches := []git.ZombieBranch{{Name: "feat-a"}, {Name: "feat-bad"}, {Name: "feat-c"}}
	msg, ok := deleteZombieBranchesCmd("dir", branches)().(zombieDeletedMsg)
	if !ok {
		t.Fatalf("expected zombieDeletedMsg")
	}
	if got := msg.deleted; len(got) != 2 || got[0] != "feat-a" || got[1] != "feat-c" {
		t.Errorf("deleted = %+v, want [feat-a feat-c]", got)
	}
	if len(msg.failed) != 1 || msg.failed[0].name != "feat-bad" {
		t.Errorf("failed = %+v, want [feat-bad]", msg.failed)
	}
}

func TestFormatZombieSummaryHappyPath(t *testing.T) {
	got, _ := formatZombieSummary([]string{"feat-a", "feat-b"}, nil)
	if !strings.Contains(got, "deleted 2 branches") || !strings.Contains(got, "feat-a, feat-b") {
		t.Errorf("happy summary unexpected: %q", got)
	}
	if !strings.Contains(got, "git reflog") {
		t.Errorf("happy summary should hint reflog: %q", got)
	}
}

func TestFormatZombieSummarySingularNoun(t *testing.T) {
	got, _ := formatZombieSummary([]string{"feat-a"}, nil)
	if !strings.Contains(got, "1 branch") {
		t.Errorf("singular noun expected: %q", got)
	}
}

func TestFormatZombieSummaryPartialFailure(t *testing.T) {
	got, _ := formatZombieSummary([]string{"feat-a"}, []zombieDeleteFailure{{name: "feat-bad", err: errors.New("not fully merged")}})
	if !strings.Contains(got, "deleted 1") || !strings.Contains(got, "1 failed") || !strings.Contains(got, "feat-bad") {
		t.Errorf("partial summary unexpected: %q", got)
	}
}

func TestFormatZombieSummaryAllFailed(t *testing.T) {
	got, _ := formatZombieSummary(nil, []zombieDeleteFailure{{name: "feat-bad", err: errors.New("boom")}})
	if !strings.Contains(got, "failed for 1") || !strings.Contains(got, "feat-bad") {
		t.Errorf("all-failed summary unexpected: %q", got)
	}
}

func TestRenderZombieCleanupConfirmInnerCapsRows(t *testing.T) {
	branches := make([]git.ZombieBranch, 12)
	for i := range branches {
		branches[i] = git.ZombieBranch{Name: "feat-" + string(rune('a'+i))}
	}
	m := Model{zombieCleanup: zombieCleanupState{baseline: "develop", branches: branches}}
	out := m.renderZombieCleanupConfirmInner()
	if !strings.Contains(out, "Delete 12 zombie branches?") {
		t.Errorf("header missing: %q", out)
	}
	if !strings.Contains(out, "+4 more") {
		t.Errorf("expected `+4 more` overflow footer, got: %q", out)
	}
	if !strings.Contains(out, "git reflog") {
		t.Errorf("expected reflog hint, got: %q", out)
	}
}

func TestRenderZombieCleanupConfirmInnerSingleBranch(t *testing.T) {
	m := Model{zombieCleanup: zombieCleanupState{baseline: "main", branches: []git.ZombieBranch{{Name: "feat-x"}}}}
	out := m.renderZombieCleanupConfirmInner()
	if !strings.Contains(out, "Delete 1 zombie branch?") {
		t.Errorf("singular header expected: %q", out)
	}
	if !strings.Contains(out, "merged into main") {
		t.Errorf("baseline name should appear: %q", out)
	}
}
