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

func TestParseStashList(t *testing.T) {
	// Two records, newest first. NUL-separated fields: %gd, %H, %gs, %aI.
	// Second record's subject contains a colon and quotes to exercise the
	// "no special chars beyond NUL" parsing claim.
	input := strings.NewReader("" +
		"stash@{0}\x00abc123\x00On main: WIP\x002026-05-15T12:34:56+09:00\n" +
		"stash@{1}\x00def456\x00WIP on feat: 'tricky: stuff'\x002026-05-14T08:00:00Z\n")
	got, err := parseStashList(input)
	if err != nil {
		t.Fatalf("parseStashList: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Label != "stash@{0}" || got[0].Hash != "abc123" || got[0].Subject != "On main: WIP" {
		t.Errorf("entry 0 mismatch: %+v", got[0])
	}
	if got[1].Subject != "WIP on feat: 'tricky: stuff'" {
		t.Errorf("entry 1 subject = %q", got[1].Subject)
	}
}

func TestParseStashListEmpty(t *testing.T) {
	got, err := parseStashList(strings.NewReader(""))
	if err != nil {
		t.Fatalf("parseStashList(empty): %v", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil", got)
	}
}

func TestParseStashListRejectsMalformed(t *testing.T) {
	input := strings.NewReader("stash@{0}\x00only-two-fields\n")
	if _, err := parseStashList(input); err == nil {
		t.Error("expected error on wrong field count")
	}
}

func TestStashListIntegration(t *testing.T) {
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

	// Empty stash returns no entries.
	if entries, err := StashList(context.Background(), work); err != nil || len(entries) != 0 {
		t.Fatalf("empty StashList: entries=%v err=%v", entries, err)
	}

	// Create two stashes; newest is stash@{0}.
	if err := os.WriteFile(filepath.Join(work, "f.txt"), []byte("first\n"), 0o644); err != nil {
		t.Fatalf("write first: %v", err)
	}
	if err := Stash(context.Background(), work, "first stash"); err != nil {
		t.Fatalf("Stash 1: %v", err)
	}
	if err := os.WriteFile(filepath.Join(work, "f.txt"), []byte("second\n"), 0o644); err != nil {
		t.Fatalf("write second: %v", err)
	}
	if err := Stash(context.Background(), work, "second stash"); err != nil {
		t.Fatalf("Stash 2: %v", err)
	}

	entries, err := StashList(context.Background(), work)
	if err != nil {
		t.Fatalf("StashList: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len = %d, want 2", len(entries))
	}
	if entries[0].Label != "stash@{0}" || entries[1].Label != "stash@{1}" {
		t.Errorf("labels = %q,%q want stash@{0},stash@{1}", entries[0].Label, entries[1].Label)
	}
	if !strings.Contains(entries[0].Subject, "second stash") {
		t.Errorf("entry 0 subject = %q, want it to contain 'second stash'", entries[0].Subject)
	}
	if entries[0].Hash == "" || entries[1].Hash == "" {
		t.Error("hashes should be non-empty")
	}
}

func TestStashApplyIntegration(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(work, "f.txt"), []byte("stashed\n"), 0o644); err != nil {
		t.Fatalf("write stashed: %v", err)
	}
	if err := Stash(context.Background(), work, "first"); err != nil {
		t.Fatalf("Stash: %v", err)
	}

	if err := StashApply(context.Background(), work, "stash@{0}"); err != nil {
		t.Fatalf("StashApply happy: %v", err)
	}
	// Apply does not drop; entry should still be there.
	if out := gitOutput(t, work, "stash", "list"); !strings.Contains(out, "first") {
		t.Errorf("stash list = %q, want it to still contain the entry after apply", out)
	}
}

func TestStashApplyReturnsConflictSentinel(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(work, "f.txt"), []byte("stash-side\n"), 0o644); err != nil {
		t.Fatalf("write stash: %v", err)
	}
	if err := Stash(context.Background(), work, "conflicting"); err != nil {
		t.Fatalf("Stash: %v", err)
	}
	if err := os.WriteFile(filepath.Join(work, "f.txt"), []byte("tree-side\n"), 0o644); err != nil {
		t.Fatalf("write tree: %v", err)
	}
	gitRun(t, work, "add", "f.txt")
	gitRun(t, work, "commit", "-m", "tree-side")

	err := StashApply(context.Background(), work, "stash@{0}")
	if err == nil {
		t.Fatal("expected stash apply conflict")
	}
	if !errors.Is(err, ErrStashApplyConflict) {
		t.Errorf("error %q should wrap ErrStashApplyConflict", err)
	}
}

func TestStashDropIntegration(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(work, "f.txt"), []byte("droppable\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := Stash(context.Background(), work, "drop me"); err != nil {
		t.Fatalf("Stash: %v", err)
	}

	if err := StashDrop(context.Background(), work, "stash@{0}"); err != nil {
		t.Fatalf("StashDrop happy: %v", err)
	}
	if out := gitOutput(t, work, "stash", "list"); strings.TrimSpace(out) != "" {
		t.Errorf("stash list after drop = %q, want empty", out)
	}

	// Drop on empty stash should error.
	if err := StashDrop(context.Background(), work, "stash@{0}"); err == nil {
		t.Error("expected error dropping from empty stash")
	}
}

func TestStashPopAtIntegration(t *testing.T) {
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
	// Create two stashes; pop stash@{1} (the older one) and verify only the
	// other survives.
	if err := os.WriteFile(filepath.Join(work, "f.txt"), []byte("older\n"), 0o644); err != nil {
		t.Fatalf("write older: %v", err)
	}
	if err := Stash(context.Background(), work, "older"); err != nil {
		t.Fatalf("Stash older: %v", err)
	}
	if err := os.WriteFile(filepath.Join(work, "g.txt"), []byte("newer\n"), 0o644); err != nil {
		t.Fatalf("write newer: %v", err)
	}
	gitRun(t, work, "add", "g.txt")
	if err := Stash(context.Background(), work, "newer"); err != nil {
		t.Fatalf("Stash newer: %v", err)
	}

	if err := StashPopAt(context.Background(), work, "stash@{1}"); err != nil {
		t.Fatalf("StashPopAt happy: %v", err)
	}
	out := gitOutput(t, work, "stash", "list")
	if !strings.Contains(out, "newer") {
		t.Errorf("stash list = %q, want it to still contain 'newer'", out)
	}
	if strings.Contains(out, "older") {
		t.Errorf("stash list = %q, want 'older' to be popped", out)
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

func TestBranchCreateIntegration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	work := t.TempDir()
	gitRun(t, work, "init", "-b", "main")
	gitRun(t, work, "config", "user.name", "Local")
	gitRun(t, work, "config", "user.email", "local@example.com")
	gitRun(t, work, "commit", "--allow-empty", "-m", "first")

	if err := BranchCreate(context.Background(), work, "feat/foo", ""); err != nil {
		t.Fatalf("BranchCreate empty base: %v", err)
	}
	if out := gitOutput(t, work, "branch", "--list", "feat/foo"); !strings.Contains(out, "feat/foo") {
		t.Errorf("branch list = %q, want it to contain feat/foo", out)
	}

	// Re-creating with the same name surfaces ErrBranchAlreadyExists.
	err := BranchCreate(context.Background(), work, "feat/foo", "")
	if err == nil {
		t.Fatal("expected duplicate-branch error")
	}
	if !errors.Is(err, ErrBranchAlreadyExists) {
		t.Errorf("error %q should wrap ErrBranchAlreadyExists", err)
	}

	// Explicit base hash works.
	headHash := gitOutput(t, work, "rev-parse", "HEAD")
	if err := BranchCreate(context.Background(), work, "feat/bar", headHash); err != nil {
		t.Fatalf("BranchCreate explicit base: %v", err)
	}
}

func TestBranchDeleteIntegration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	work := t.TempDir()
	gitRun(t, work, "init", "-b", "main")
	gitRun(t, work, "config", "user.name", "Local")
	gitRun(t, work, "config", "user.email", "local@example.com")
	gitRun(t, work, "commit", "--allow-empty", "-m", "first")

	// Safe delete: branch fully merged into HEAD.
	gitRun(t, work, "branch", "merged-branch")
	if err := BranchDelete(context.Background(), work, "merged-branch", false); err != nil {
		t.Fatalf("BranchDelete safe path: %v", err)
	}
	if out := gitOutput(t, work, "branch", "--list", "merged-branch"); strings.TrimSpace(out) != "" {
		t.Errorf("branch should be gone, got %q", out)
	}

	// Unmerged branch with -d → ErrBranchNotFullyMerged.
	gitRun(t, work, "checkout", "-b", "unmerged")
	gitRun(t, work, "commit", "--allow-empty", "-m", "side")
	gitRun(t, work, "checkout", "main")
	err := BranchDelete(context.Background(), work, "unmerged", false)
	if err == nil {
		t.Fatal("expected not-fully-merged rejection")
	}
	if !errors.Is(err, ErrBranchNotFullyMerged) {
		t.Errorf("error %q should wrap ErrBranchNotFullyMerged", err)
	}

	// -D forces through.
	if err := BranchDelete(context.Background(), work, "unmerged", true); err != nil {
		t.Fatalf("BranchDelete force: %v", err)
	}

	// Missing branch → ErrBranchNotFound.
	err = BranchDelete(context.Background(), work, "no-such-branch", false)
	if err == nil {
		t.Fatal("expected missing-branch error")
	}
	if !errors.Is(err, ErrBranchNotFound) {
		t.Errorf("error %q should wrap ErrBranchNotFound", err)
	}
}

func TestBranchRenameIntegration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	work := t.TempDir()
	gitRun(t, work, "init", "-b", "main")
	gitRun(t, work, "config", "user.name", "Local")
	gitRun(t, work, "config", "user.email", "local@example.com")
	gitRun(t, work, "commit", "--allow-empty", "-m", "first")
	gitRun(t, work, "branch", "old-name")

	if err := BranchRename(context.Background(), work, "old-name", "new-name"); err != nil {
		t.Fatalf("BranchRename happy: %v", err)
	}
	if out := gitOutput(t, work, "branch", "--list", "old-name"); strings.TrimSpace(out) != "" {
		t.Errorf("old-name should be gone, got %q", out)
	}
	if out := gitOutput(t, work, "branch", "--list", "new-name"); !strings.Contains(out, "new-name") {
		t.Errorf("branch list = %q, want it to contain new-name", out)
	}

	// Collision: rename onto an existing branch name.
	gitRun(t, work, "branch", "another")
	err := BranchRename(context.Background(), work, "new-name", "another")
	if err == nil {
		t.Fatal("expected collision error")
	}
	if !errors.Is(err, ErrBranchAlreadyExists) {
		t.Errorf("error %q should wrap ErrBranchAlreadyExists", err)
	}

	// Missing source.
	err = BranchRename(context.Background(), work, "no-such", "whatever")
	if err == nil {
		t.Fatal("expected missing-branch error")
	}
	if !errors.Is(err, ErrBranchNotFound) {
		t.Errorf("error %q should wrap ErrBranchNotFound", err)
	}
}

func TestBranchRenameMovesHEAD(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	work := t.TempDir()
	gitRun(t, work, "init", "-b", "main")
	gitRun(t, work, "config", "user.name", "Local")
	gitRun(t, work, "config", "user.email", "local@example.com")
	gitRun(t, work, "commit", "--allow-empty", "-m", "first")

	// HEAD is on main; renaming main itself should move HEAD with it.
	if err := BranchRename(context.Background(), work, "main", "trunk"); err != nil {
		t.Fatalf("BranchRename HEAD branch: %v", err)
	}
	got := strings.TrimSpace(gitOutput(t, work, "symbolic-ref", "--short", "HEAD"))
	if got != "trunk" {
		t.Errorf("HEAD now points at %q, want %q", got, "trunk")
	}
}

func TestRemoteBranchDeleteIntegration(t *testing.T) {
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
	gitRun(t, work, "config", "user.name", "Local")
	gitRun(t, work, "config", "user.email", "local@example.com")
	gitRun(t, work, "remote", "add", "origin", bare)
	gitRun(t, work, "commit", "--allow-empty", "-m", "first")
	gitRun(t, work, "push", "origin", "main")
	gitRun(t, work, "checkout", "-b", "feat/foo")
	gitRun(t, work, "commit", "--allow-empty", "-m", "feat commit")
	gitRun(t, work, "push", "origin", "feat/foo")
	gitRun(t, work, "checkout", "main")

	if err := RemoteBranchDelete(context.Background(), work, "origin", "feat/foo"); err != nil {
		t.Fatalf("RemoteBranchDelete: %v", err)
	}
	// The remote ref should no longer be advertised after fetch --prune.
	gitRun(t, work, "fetch", "--prune", "origin")
	if out := gitOutput(t, work, "branch", "-r", "--list", "origin/feat/foo"); strings.TrimSpace(out) != "" {
		t.Errorf("remote branch should be gone, got %q", out)
	}

	// Deleting a non-existent remote branch is an error.
	err := RemoteBranchDelete(context.Background(), work, "origin", "feat/foo")
	if err == nil {
		t.Fatal("expected error deleting non-existent remote branch")
	}
}

func TestCheckRefFormat(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	work := t.TempDir() // CheckRefFormat is cwd-independent but we still pass a dir.

	// Valid names pass.
	for _, name := range []string{"feat/foo", "main", "a/b/c", "v1-fix"} {
		if err := CheckRefFormat(context.Background(), work, name); err != nil {
			t.Errorf("CheckRefFormat(%q) = %v, want nil", name, err)
		}
	}

	// Invalid names wrap ErrInvalidRefName.
	for _, name := range []string{"", "foo..bar", "-leading-dash", "with space", "trailing.lock"} {
		err := CheckRefFormat(context.Background(), work, name)
		if err == nil {
			t.Errorf("CheckRefFormat(%q) returned nil, want error", name)
			continue
		}
		if !errors.Is(err, ErrInvalidRefName) {
			t.Errorf("CheckRefFormat(%q) = %v, want it to wrap ErrInvalidRefName", name, err)
		}
	}
}
