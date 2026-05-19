package git

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestParseRefLineLocalBranch(t *testing.T) {
	line := "refs/heads/main\x00main\x00commit\x00abc123\x00\x00*\x00origin/main"
	ref, ok, err := parseRefLine(line)
	if err != nil || !ok {
		t.Fatalf("parseRefLine: ok=%v err=%v", ok, err)
	}
	if ref.Kind != RefKindLocal {
		t.Errorf("Kind = %v, want RefKindLocal", ref.Kind)
	}
	if !ref.IsHead {
		t.Error("IsHead should be true for HEAD-marked ref")
	}
	if ref.Upstream != "origin/main" {
		t.Errorf("Upstream = %q, want origin/main", ref.Upstream)
	}
	if ref.ObjectName != "abc123" {
		t.Errorf("ObjectName = %q, want abc123", ref.ObjectName)
	}
}

func TestParseRefLineRemoteBranch(t *testing.T) {
	line := "refs/remotes/origin/feat\x00origin/feat\x00commit\x00def456\x00\x00 \x00"
	ref, ok, err := parseRefLine(line)
	if err != nil || !ok {
		t.Fatalf("parseRefLine: ok=%v err=%v", ok, err)
	}
	if ref.Kind != RefKindRemote {
		t.Errorf("Kind = %v, want RefKindRemote", ref.Kind)
	}
	if ref.IsHead {
		t.Error("IsHead should be false for a non-HEAD ref")
	}
}

func TestParseRefLineDropsSymbolicRemoteHead(t *testing.T) {
	line := "refs/remotes/origin/HEAD\x00origin/HEAD\x00commit\x00abc123\x00\x00 \x00"
	_, ok, err := parseRefLine(line)
	if err != nil {
		t.Fatalf("parseRefLine: %v", err)
	}
	if ok {
		t.Error("symbolic remote HEAD should be dropped")
	}
}

func TestParseRefLineLightweightTag(t *testing.T) {
	// Lightweight tag: objecttype=commit, *objectname empty.
	line := "refs/tags/v1.0\x00v1.0\x00commit\x00aaa111\x00\x00 \x00"
	ref, ok, err := parseRefLine(line)
	if err != nil || !ok {
		t.Fatalf("parseRefLine: ok=%v err=%v", ok, err)
	}
	if ref.Kind != RefKindTag {
		t.Errorf("Kind = %v, want RefKindTag", ref.Kind)
	}
	if ref.ObjectName != "aaa111" {
		t.Errorf("ObjectName = %q, want aaa111 (fallback to objectname)", ref.ObjectName)
	}
}

func TestParseRefLineAnnotatedTagPeels(t *testing.T) {
	// Annotated tag: objecttype=tag, *objectname is the peeled commit hash.
	line := "refs/tags/v2.0\x00v2.0\x00tag\x00bbb222\x00ccc333\x00 \x00"
	ref, ok, err := parseRefLine(line)
	if err != nil || !ok {
		t.Fatalf("parseRefLine: ok=%v err=%v", ok, err)
	}
	if ref.ObjectName != "ccc333" {
		t.Errorf("ObjectName = %q, want ccc333 (peeled commit)", ref.ObjectName)
	}
}

func TestParseRefLineDropsUnknownKind(t *testing.T) {
	// refs/notes/commits is outside the Local/Remote/Tags universe the TUI
	// cares about and should drop.
	line := "refs/notes/commits\x00notes/commits\x00commit\x00abc\x00\x00 \x00"
	_, ok, err := parseRefLine(line)
	if err != nil {
		t.Fatalf("parseRefLine: %v", err)
	}
	if ok {
		t.Error("refs/notes/commits should be dropped (not heads/remotes/tags)")
	}
}

func TestParseRefLineRejectsMalformed(t *testing.T) {
	if _, _, err := parseRefLine("only-three\x00fields\x00here"); err == nil {
		t.Error("expected error on wrong field count")
	}
}

func TestForEachRefIntegration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-b", "main")
	run("commit", "--allow-empty", "-m", "first")
	run("branch", "feat/x")
	run("tag", "v1.0")                          // lightweight
	run("tag", "-a", "v2.0", "-m", "annotated") // annotated
	run("update-ref", "refs/remotes/origin/main", "HEAD")

	refs, err := ForEachRef(context.Background(), ForEachRefOptions{Dir: dir})
	if err != nil {
		t.Fatalf("ForEachRef: %v", err)
	}

	var heads, remotes, tags int
	var headRef *Ref
	for i := range refs {
		switch refs[i].Kind {
		case RefKindLocal:
			heads++
		case RefKindRemote:
			remotes++
		case RefKindTag:
			tags++
		}
		if refs[i].IsHead {
			r := refs[i]
			headRef = &r
		}
	}
	if heads != 2 {
		t.Errorf("heads = %d, want 2 (main + feat/x)", heads)
	}
	if remotes != 1 {
		t.Errorf("remotes = %d, want 1 (origin/main)", remotes)
	}
	if tags != 2 {
		t.Errorf("tags = %d, want 2 (v1.0 + v2.0)", tags)
	}
	if headRef == nil || headRef.ShortName != "main" {
		t.Errorf("HEAD ref not found or wrong: %+v", headRef)
	}

	// Annotated tag should expose the peeled commit hash, not the tag object.
	for _, r := range refs {
		if r.ShortName == "v2.0" {
			head := commitHashAt(t, dir, "HEAD")
			if r.ObjectName != head {
				t.Errorf("annotated tag v2.0 ObjectName = %q, want peeled commit %q", r.ObjectName, head)
			}
		}
	}
}

func TestForEachRefReturnsErrorOutsideRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	_, err := ForEachRef(context.Background(), ForEachRefOptions{Dir: dir})
	if err == nil {
		t.Fatal("expected error running for-each-ref outside a repo")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("error %q should include git's stderr message", err)
	}
}

func commitHashAt(t *testing.T, dir, rev string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", rev)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-parse %s: %v: %s", rev, err, out)
	}
	return strings.TrimSpace(string(out))
}
