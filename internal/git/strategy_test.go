package git

import (
	"context"
	"os/exec"
	"testing"
)

func TestPullStrategyFromPrefsKnownValues(t *testing.T) {
	cases := []struct {
		prefs string
		want  PullStrategy
		ok    bool
	}{
		{"ff-only", PullStrategyFFOnly, true},
		{"merge", PullStrategyMerge, true},
		{"rebase", PullStrategyRebase, true},
		{"  rebase  ", PullStrategyRebase, true},
		{"", PullStrategyFFOnly, false},
		{"unknown", PullStrategyFFOnly, false},
	}
	for _, c := range cases {
		got, ok := pullStrategyFromPrefs(c.prefs)
		if ok != c.ok {
			t.Errorf("prefs %q: ok=%v, want %v", c.prefs, ok, c.ok)
		}
		if ok && got != c.want {
			t.Errorf("prefs %q: strategy=%d, want %d", c.prefs, got, c.want)
		}
	}
}

func TestResolvePullStrategyPrefsBeatsGitConfig(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitRun(t, dir, "init", "-b", "main")
	gitRun(t, dir, "config", "pull.rebase", "true")

	got, err := ResolvePullStrategy(context.Background(), dir, "merge")
	if err != nil {
		t.Fatalf("ResolvePullStrategy: %v", err)
	}
	if got != PullStrategyMerge {
		t.Errorf("strategy = %d, want PullStrategyMerge (%d) — prefs should override git config", got, PullStrategyMerge)
	}
}

func TestResolvePullStrategyHonorsGitConfigRebase(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitRun(t, dir, "init", "-b", "main")
	gitRun(t, dir, "config", "pull.rebase", "true")

	got, err := ResolvePullStrategy(context.Background(), dir, "")
	if err != nil {
		t.Fatalf("ResolvePullStrategy: %v", err)
	}
	if got != PullStrategyRebase {
		t.Errorf("strategy = %d, want PullStrategyRebase (%d)", got, PullStrategyRebase)
	}
}

func TestResolvePullStrategyHonorsGitConfigFFOnly(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitRun(t, dir, "init", "-b", "main")
	gitRun(t, dir, "config", "pull.ff", "only")

	got, err := ResolvePullStrategy(context.Background(), dir, "")
	if err != nil {
		t.Fatalf("ResolvePullStrategy: %v", err)
	}
	if got != PullStrategyFFOnly {
		t.Errorf("strategy = %d, want PullStrategyFFOnly (%d)", got, PullStrategyFFOnly)
	}
}

func TestResolvePullStrategyFallsBackToFFOnly(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitRun(t, dir, "init", "-b", "main")

	got, err := ResolvePullStrategy(context.Background(), dir, "")
	if err != nil {
		t.Fatalf("ResolvePullStrategy: %v", err)
	}
	if got != PullStrategyFFOnly {
		t.Errorf("strategy = %d, want PullStrategyFFOnly fallback (%d)", got, PullStrategyFFOnly)
	}
}

func TestResolvePullStrategyPullRebaseFalseFallsThrough(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitRun(t, dir, "init", "-b", "main")
	gitRun(t, dir, "config", "pull.rebase", "false")

	got, err := ResolvePullStrategy(context.Background(), dir, "")
	if err != nil {
		t.Fatalf("ResolvePullStrategy: %v", err)
	}
	if got != PullStrategyFFOnly {
		t.Errorf("strategy = %d, want PullStrategyFFOnly (pull.rebase=false should not pick rebase) (%d)", got, PullStrategyFFOnly)
	}
}
