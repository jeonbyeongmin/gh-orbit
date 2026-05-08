package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestFetchReturnsErrorWithStderr(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	err := Fetch(context.Background(), dir)
	if err == nil {
		t.Fatal("expected error running git fetch outside a repo")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("error %q should include git's stderr message", err)
	}
}

// TestFetchIntegration wires up a self-hosted bare remote so the happy path
// stays deterministic — no network, no credentials.
func TestFetchIntegration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	bare := filepath.Join(root, "bare.git")
	work := filepath.Join(root, "work")

	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatalf("mkdir bare: %v", err)
	}
	gitRun(t, bare, "init", "--bare", "-b", "main")

	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatalf("mkdir work: %v", err)
	}
	gitRun(t, work, "init", "-b", "main")
	gitRun(t, work, "remote", "add", "origin", bare)
	gitRun(t, work, "commit", "--allow-empty", "-m", "first")
	gitRun(t, work, "push", "origin", "main")

	if err := Fetch(context.Background(), work); err != nil {
		t.Fatalf("Fetch happy path: %v", err)
	}
}

func TestPullStrategyArgs(t *testing.T) {
	cases := []struct {
		s    PullStrategy
		want []string
	}{
		{PullStrategyFFOnly, []string{"--ff-only"}},
		{PullStrategyMerge, []string{"--no-rebase"}},
		{PullStrategyRebase, []string{"--rebase"}},
	}
	for _, c := range cases {
		got := c.s.args()
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("PullStrategy(%d).args() = %v, want %v", c.s, got, c.want)
		}
	}
}

func TestPullReturnsErrorWithStderr(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	err := Pull(context.Background(), dir, PullStrategyFFOnly)
	if err == nil {
		t.Fatal("expected error running git pull outside a repo")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("error %q should include git's stderr message", err)
	}
}

func TestPullIntegrationFastForward(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	bare := filepath.Join(root, "bare.git")
	work := filepath.Join(root, "work")
	other := filepath.Join(root, "other")

	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatalf("mkdir bare: %v", err)
	}
	gitRun(t, bare, "init", "--bare", "-b", "main")

	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatalf("mkdir work: %v", err)
	}
	gitRun(t, work, "init", "-b", "main")
	gitRun(t, work, "remote", "add", "origin", bare)
	gitRun(t, work, "commit", "--allow-empty", "-m", "first")
	gitRun(t, work, "push", "origin", "main")
	gitRun(t, work, "branch", "--set-upstream-to=origin/main", "main")

	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatalf("mkdir other: %v", err)
	}
	gitRun(t, other, "clone", bare, ".")
	gitRun(t, other, "config", "user.name", "Other")
	gitRun(t, other, "config", "user.email", "other@example.com")
	gitRun(t, other, "commit", "--allow-empty", "-m", "second from other")
	gitRun(t, other, "push", "origin", "main")

	if err := Pull(context.Background(), work, PullStrategyFFOnly); err != nil {
		t.Fatalf("Pull happy path: %v", err)
	}
}

func TestCheckoutTarget(t *testing.T) {
	cases := []struct {
		name string
		ref  Ref
		want string
	}{
		{"local", Ref{ShortName: "main", Kind: RefKindLocal}, "main"},
		{"local with slash", Ref{ShortName: "feat/foo", Kind: RefKindLocal}, "feat/foo"},
		{"tag", Ref{ShortName: "v1.0", Kind: RefKindTag}, "v1.0"},
		{"remote single segment", Ref{ShortName: "origin/feat", Kind: RefKindRemote}, "feat"},
		{"remote nested", Ref{ShortName: "origin/feat/foo", Kind: RefKindRemote}, "feat/foo"},
		{"remote no slash (degenerate)", Ref{ShortName: "weird", Kind: RefKindRemote}, "weird"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CheckoutTarget(c.ref); got != c.want {
				t.Errorf("CheckoutTarget(%+v) = %q, want %q", c.ref, got, c.want)
			}
		})
	}
}

func TestCheckoutReturnsErrorOutsideRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	err := Checkout(context.Background(), dir, "main")
	if err == nil {
		t.Fatal("expected error running git checkout outside a repo")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("error %q should include git's stderr message", err)
	}
}

func TestCheckoutIntegrationCleanTree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	work := t.TempDir()
	gitRun(t, work, "init", "-b", "main")
	gitRun(t, work, "config", "user.name", "Local")
	gitRun(t, work, "config", "user.email", "local@example.com")
	gitRun(t, work, "commit", "--allow-empty", "-m", "first")
	gitRun(t, work, "branch", "feat")

	if err := Checkout(context.Background(), work, "feat"); err != nil {
		t.Fatalf("clean-tree checkout: %v", err)
	}
	if got := readHEAD(t, work); got != "refs/heads/feat" {
		t.Errorf("HEAD = %q, want refs/heads/feat", got)
	}
}

func TestCheckoutIntegrationDirtyTreeWraps(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	work := t.TempDir()
	gitRun(t, work, "init", "-b", "main")
	gitRun(t, work, "config", "user.name", "Local")
	gitRun(t, work, "config", "user.email", "local@example.com")
	if err := os.WriteFile(filepath.Join(work, "f.txt"), []byte("main\n"), 0o644); err != nil {
		t.Fatalf("write main f.txt: %v", err)
	}
	gitRun(t, work, "add", "f.txt")
	gitRun(t, work, "commit", "-m", "main")
	gitRun(t, work, "checkout", "-b", "feat")
	if err := os.WriteFile(filepath.Join(work, "f.txt"), []byte("feat\n"), 0o644); err != nil {
		t.Fatalf("write feat f.txt: %v", err)
	}
	gitRun(t, work, "add", "f.txt")
	gitRun(t, work, "commit", "-m", "feat change")
	gitRun(t, work, "checkout", "main")
	// Uncommitted modification on main that would be clobbered by switching
	// back to feat — git refuses with the canonical "Please commit your
	// changes or stash them" / "would be overwritten" message.
	if err := os.WriteFile(filepath.Join(work, "f.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("write dirty f.txt: %v", err)
	}

	err := Checkout(context.Background(), work, "feat")
	if err == nil {
		t.Fatal("expected dirty-tree error")
	}
	if !errors.Is(err, ErrCheckoutNeedsCleanTree) {
		t.Errorf("error %q should wrap ErrCheckoutNeedsCleanTree", err)
	}
}

func TestCheckoutIntegrationRemoteTrackingDWIM(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	bare := filepath.Join(root, "bare.git")
	work := filepath.Join(root, "work")
	other := filepath.Join(root, "other")
	for _, d := range []string{bare, work, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	gitRun(t, bare, "init", "--bare", "-b", "main")

	gitRun(t, work, "init", "-b", "main")
	gitRun(t, work, "config", "user.name", "Local")
	gitRun(t, work, "config", "user.email", "local@example.com")
	gitRun(t, work, "remote", "add", "origin", bare)
	gitRun(t, work, "commit", "--allow-empty", "-m", "first")
	gitRun(t, work, "push", "origin", "main")

	gitRun(t, other, "clone", bare, ".")
	gitRun(t, other, "config", "user.name", "Other")
	gitRun(t, other, "config", "user.email", "other@example.com")
	gitRun(t, other, "checkout", "-b", "feat")
	gitRun(t, other, "commit", "--allow-empty", "-m", "feat work")
	gitRun(t, other, "push", "-u", "origin", "feat")

	gitRun(t, work, "fetch", "origin")
	// dwim: "git checkout feat" with no local branch but exactly one
	// "<remote>/feat" creates a local tracking branch.
	if err := Checkout(context.Background(), work, "feat"); err != nil {
		t.Fatalf("dwim checkout: %v", err)
	}
	if got := readHEAD(t, work); got != "refs/heads/feat" {
		t.Errorf("HEAD = %q, want refs/heads/feat (dwim should create local branch)", got)
	}
	if got := gitOutput(t, work, "config", "--get", "branch.feat.remote"); got != "origin" {
		t.Errorf("branch.feat.remote = %q, want origin", got)
	}
}

func TestCheckoutDetachedIntegration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	work := t.TempDir()
	gitRun(t, work, "init", "-b", "main")
	gitRun(t, work, "config", "user.name", "Local")
	gitRun(t, work, "config", "user.email", "local@example.com")
	gitRun(t, work, "commit", "--allow-empty", "-m", "first")
	hash := gitOutput(t, work, "rev-parse", "HEAD")
	gitRun(t, work, "commit", "--allow-empty", "-m", "second")

	if err := CheckoutDetached(context.Background(), work, hash); err != nil {
		t.Fatalf("CheckoutDetached: %v", err)
	}
	// symbolic-ref fails on detached HEAD — the absence of an exit-0 means
	// HEAD is no longer pointing at any branch.
	cmd := exec.Command("git", "-C", work, "symbolic-ref", "-q", "HEAD")
	if err := cmd.Run(); err == nil {
		t.Errorf("expected detached HEAD; symbolic-ref unexpectedly succeeded")
	}
	if got := gitOutput(t, work, "rev-parse", "HEAD"); got != hash {
		t.Errorf("HEAD = %q, want %q", got, hash)
	}
}

func TestStashIntegration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	work := t.TempDir()
	gitRun(t, work, "init", "-b", "main")
	gitRun(t, work, "config", "user.name", "Local")
	gitRun(t, work, "config", "user.email", "local@example.com")
	if err := os.WriteFile(filepath.Join(work, "f.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write f.txt: %v", err)
	}
	gitRun(t, work, "add", "f.txt")
	gitRun(t, work, "commit", "-m", "base")
	if err := os.WriteFile(filepath.Join(work, "f.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("dirty f.txt: %v", err)
	}

	if err := Stash(context.Background(), work, "test stash"); err != nil {
		t.Fatalf("Stash: %v", err)
	}
	out := gitOutput(t, work, "stash", "list")
	if !strings.Contains(out, "test stash") {
		t.Errorf("stash list = %q, want it to contain the stash message", out)
	}
}

func TestStashPopReturnsErrorWithStderr(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	err := StashPop(context.Background(), dir)
	if err == nil {
		t.Fatal("expected error running git stash pop outside a repo")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("error %q should include git's stderr message", err)
	}
}

func TestStashPopReturnsConflictSentinel(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	work := t.TempDir()
	gitRun(t, work, "init", "-b", "main")
	gitRun(t, work, "config", "user.name", "Local")
	gitRun(t, work, "config", "user.email", "local@example.com")
	if err := os.WriteFile(filepath.Join(work, "f.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write base f.txt: %v", err)
	}
	gitRun(t, work, "add", "f.txt")
	gitRun(t, work, "commit", "-m", "base")

	// Stash a working-tree change at the same line.
	if err := os.WriteFile(filepath.Join(work, "f.txt"), []byte("stash-side\n"), 0o644); err != nil {
		t.Fatalf("write stash f.txt: %v", err)
	}
	if err := Stash(context.Background(), work, "test stash"); err != nil {
		t.Fatalf("Stash: %v", err)
	}

	// Commit a conflicting change at the same line on top of the stash's base.
	// stash pop will 3-way merge stash-side vs tree-side using base as common
	// ancestor — git writes conflict markers + the CONFLICT line to stdout.
	if err := os.WriteFile(filepath.Join(work, "f.txt"), []byte("tree-side\n"), 0o644); err != nil {
		t.Fatalf("write tree f.txt: %v", err)
	}
	gitRun(t, work, "add", "f.txt")
	gitRun(t, work, "commit", "-m", "tree-side change")

	err := StashPop(context.Background(), work)
	if err == nil {
		t.Fatal("expected stash pop conflict")
	}
	if !errors.Is(err, ErrStashPopConflict) {
		t.Errorf("error %q should wrap ErrStashPopConflict", err)
	}
	// Stash should still be present (git preserves the entry on conflict).
	if out := gitOutput(t, work, "stash", "list"); !strings.Contains(out, "test stash") {
		t.Errorf("stash list = %q, want it to still contain the stash entry on conflict", out)
	}
}

func TestIsAncestorIntegration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	work := t.TempDir()
	gitRun(t, work, "init", "-b", "main")
	gitRun(t, work, "config", "user.name", "Local")
	gitRun(t, work, "config", "user.email", "local@example.com")
	gitRun(t, work, "commit", "--allow-empty", "-m", "first")
	first := gitOutput(t, work, "rev-parse", "HEAD")
	gitRun(t, work, "commit", "--allow-empty", "-m", "second")
	second := gitOutput(t, work, "rev-parse", "HEAD")

	// first is ancestor of second.
	got, err := IsAncestor(context.Background(), work, first, second)
	if err != nil {
		t.Fatalf("IsAncestor(first, second): %v", err)
	}
	if !got {
		t.Errorf("IsAncestor(first, second) = false, want true")
	}

	// second is NOT an ancestor of first (descendant of self isn't ancestor).
	got, err = IsAncestor(context.Background(), work, second, first)
	if err != nil {
		t.Fatalf("IsAncestor(second, first): %v", err)
	}
	if got {
		t.Errorf("IsAncestor(second, first) = true, want false")
	}

	// Self-ancestry is true (git's contract).
	got, err = IsAncestor(context.Background(), work, first, first)
	if err != nil {
		t.Fatalf("IsAncestor(first, first): %v", err)
	}
	if !got {
		t.Errorf("IsAncestor(first, first) = false, want true")
	}
}

func TestIsAncestorBadRefReturnsError(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	work := t.TempDir()
	gitRun(t, work, "init", "-b", "main")
	gitRun(t, work, "config", "user.name", "Local")
	gitRun(t, work, "config", "user.email", "local@example.com")
	gitRun(t, work, "commit", "--allow-empty", "-m", "first")

	// Bad ref → git exits with code 128, IsAncestor surfaces it as a wrapped err.
	_, err := IsAncestor(context.Background(), work, "definitely-not-a-ref", "HEAD")
	if err == nil {
		t.Fatal("expected error on bad ancestor ref")
	}
}

func TestMergeFFOnlyIntegration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	work := t.TempDir()
	gitRun(t, work, "init", "-b", "main")
	gitRun(t, work, "config", "user.name", "Local")
	gitRun(t, work, "config", "user.email", "local@example.com")
	gitRun(t, work, "commit", "--allow-empty", "-m", "first")
	first := gitOutput(t, work, "rev-parse", "HEAD")
	gitRun(t, work, "commit", "--allow-empty", "-m", "second")
	second := gitOutput(t, work, "rev-parse", "HEAD")
	// Reset HEAD back to first so MergeFFOnly has somewhere to advance to.
	gitRun(t, work, "reset", "--hard", first)

	if err := MergeFFOnly(context.Background(), work, second); err != nil {
		t.Fatalf("MergeFFOnly happy path: %v", err)
	}
	if got := gitOutput(t, work, "rev-parse", "HEAD"); got != second {
		t.Errorf("HEAD = %q, want %q (second commit) after FF", got, second)
	}
}

func TestMergeFFOnlyDivergentReturnsErrFFNotPossible(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	work := t.TempDir()
	gitRun(t, work, "init", "-b", "main")
	gitRun(t, work, "config", "user.name", "Local")
	gitRun(t, work, "config", "user.email", "local@example.com")
	if err := os.WriteFile(filepath.Join(work, "f.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write base: %v", err)
	}
	gitRun(t, work, "add", "f.txt")
	gitRun(t, work, "commit", "-m", "base")
	base := gitOutput(t, work, "rev-parse", "HEAD")

	// Diverge: side branch with its own commit, main with a different commit.
	gitRun(t, work, "checkout", "-b", "side")
	if err := os.WriteFile(filepath.Join(work, "f.txt"), []byte("side\n"), 0o644); err != nil {
		t.Fatalf("write side: %v", err)
	}
	gitRun(t, work, "add", "f.txt")
	gitRun(t, work, "commit", "-m", "side change")
	sideHash := gitOutput(t, work, "rev-parse", "HEAD")

	gitRun(t, work, "checkout", "main")
	gitRun(t, work, "reset", "--hard", base)
	if err := os.WriteFile(filepath.Join(work, "f.txt"), []byte("main-other\n"), 0o644); err != nil {
		t.Fatalf("write main-other: %v", err)
	}
	gitRun(t, work, "add", "f.txt")
	gitRun(t, work, "commit", "-m", "main-other change")

	err := MergeFFOnly(context.Background(), work, sideHash)
	if err == nil {
		t.Fatal("expected FF rejection on divergent histories")
	}
	if !errors.Is(err, ErrFFNotPossible) {
		t.Errorf("error %q should wrap ErrFFNotPossible", err)
	}
}

func TestMergeFFOnlyDirtyTreeWraps(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	work := t.TempDir()
	gitRun(t, work, "init", "-b", "main")
	gitRun(t, work, "config", "user.name", "Local")
	gitRun(t, work, "config", "user.email", "local@example.com")
	if err := os.WriteFile(filepath.Join(work, "f.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write base: %v", err)
	}
	gitRun(t, work, "add", "f.txt")
	gitRun(t, work, "commit", "-m", "base")
	first := gitOutput(t, work, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(work, "f.txt"), []byte("second\n"), 0o644); err != nil {
		t.Fatalf("write second: %v", err)
	}
	gitRun(t, work, "add", "f.txt")
	gitRun(t, work, "commit", "-m", "second")
	second := gitOutput(t, work, "rev-parse", "HEAD")

	// Reset to first, then dirty f.txt so the FF would have to clobber it.
	gitRun(t, work, "reset", "--hard", first)
	if err := os.WriteFile(filepath.Join(work, "f.txt"), []byte("dirty local\n"), 0o644); err != nil {
		t.Fatalf("write dirty: %v", err)
	}

	err := MergeFFOnly(context.Background(), work, second)
	if err == nil {
		t.Fatal("expected dirty-tree refusal")
	}
	if !errors.Is(err, ErrCheckoutNeedsCleanTree) {
		t.Errorf("error %q should wrap ErrCheckoutNeedsCleanTree", err)
	}
}

func TestCountAheadIntegration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	work := t.TempDir()
	gitRun(t, work, "init", "-b", "main")
	gitRun(t, work, "config", "user.name", "Local")
	gitRun(t, work, "config", "user.email", "local@example.com")
	gitRun(t, work, "commit", "--allow-empty", "-m", "c1")
	c1 := gitOutput(t, work, "rev-parse", "HEAD")
	gitRun(t, work, "commit", "--allow-empty", "-m", "c2")
	gitRun(t, work, "commit", "--allow-empty", "-m", "c3")
	c3 := gitOutput(t, work, "rev-parse", "HEAD")

	got, err := CountAhead(context.Background(), work, c1, c3)
	if err != nil {
		t.Fatalf("CountAhead(c1, c3): %v", err)
	}
	if got != 2 {
		t.Errorf("CountAhead(c1, c3) = %d, want 2", got)
	}

	// Self-range is empty.
	got, err = CountAhead(context.Background(), work, c1, c1)
	if err != nil {
		t.Fatalf("CountAhead(c1, c1): %v", err)
	}
	if got != 0 {
		t.Errorf("CountAhead(c1, c1) = %d, want 0", got)
	}

	// Reverse range: c3 is ahead of c1, so c1 has 0 commits not reachable
	// from c3 — `<c3>..<c1>` is empty.
	got, err = CountAhead(context.Background(), work, c3, c1)
	if err != nil {
		t.Fatalf("CountAhead(c3, c1): %v", err)
	}
	if got != 0 {
		t.Errorf("CountAhead(c3, c1) = %d, want 0 (descendant behind ancestor)", got)
	}
}

func TestPullIntegrationConflictWraps(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	bare := filepath.Join(root, "bare.git")
	work := filepath.Join(root, "work")
	other := filepath.Join(root, "other")

	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatalf("mkdir bare: %v", err)
	}
	gitRun(t, bare, "init", "--bare", "-b", "main")

	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatalf("mkdir work: %v", err)
	}
	gitRun(t, work, "init", "-b", "main")
	// Pull() inherits the parent's environment, not gitRun's per-call env, so
	// merge commits made during pull need user.email/user.name set on the
	// repo itself. CI runners have no global identity.
	gitRun(t, work, "config", "user.name", "Local")
	gitRun(t, work, "config", "user.email", "local@example.com")
	gitRun(t, work, "remote", "add", "origin", bare)
	if err := os.WriteFile(filepath.Join(work, "f.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write f.txt: %v", err)
	}
	gitRun(t, work, "add", "f.txt")
	gitRun(t, work, "commit", "-m", "base")
	gitRun(t, work, "push", "origin", "main")
	gitRun(t, work, "branch", "--set-upstream-to=origin/main", "main")

	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatalf("mkdir other: %v", err)
	}
	gitRun(t, other, "clone", bare, ".")
	gitRun(t, other, "config", "user.name", "Other")
	gitRun(t, other, "config", "user.email", "other@example.com")
	if err := os.WriteFile(filepath.Join(other, "f.txt"), []byte("remote-side\n"), 0o644); err != nil {
		t.Fatalf("write other f.txt: %v", err)
	}
	gitRun(t, other, "add", "f.txt")
	gitRun(t, other, "commit", "-m", "remote change")
	gitRun(t, other, "push", "origin", "main")

	if err := os.WriteFile(filepath.Join(work, "f.txt"), []byte("local-side\n"), 0o644); err != nil {
		t.Fatalf("write work f.txt: %v", err)
	}
	gitRun(t, work, "add", "f.txt")
	gitRun(t, work, "commit", "-m", "local change")

	err := Pull(context.Background(), work, PullStrategyMerge)
	if err == nil {
		t.Fatal("expected pull conflict error")
	}
	if !errors.Is(err, ErrPullConflict) {
		t.Errorf("error %q should wrap ErrPullConflict", err)
	}
}
