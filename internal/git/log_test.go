package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParseLog(t *testing.T) {
	in := strings.Join([]string{
		"abc123\x00def456\x00Alice\x00alice@example.com\x001700000000\x00add feature\x00HEAD -> main, origin/main, tag: v0.0.1",
		"def456\x00\x00Bob\x00bob@example.com\x001699999999\x00initial commit\x00",
	}, "\n") + "\n"

	got, err := parseLog(strings.NewReader(in))
	if err != nil {
		t.Fatalf("parseLog: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d commits, want 2", len(got))
	}

	c0 := got[0]
	if c0.Hash != "abc123" || c0.Subject != "add feature" {
		t.Errorf("commit[0] = %+v", c0)
	}
	if len(c0.Parents) != 1 || c0.Parents[0] != "def456" {
		t.Errorf("commit[0].Parents = %v, want [def456]", c0.Parents)
	}
	if !c0.AuthorTime.Equal(time.Unix(1700000000, 0)) {
		t.Errorf("commit[0].AuthorTime = %v", c0.AuthorTime)
	}
	wantRefs := []string{"HEAD -> main", "origin/main", "tag: v0.0.1"}
	if len(c0.RefNames) != len(wantRefs) {
		t.Fatalf("commit[0].RefNames = %v, want %v", c0.RefNames, wantRefs)
	}
	for i, r := range wantRefs {
		if c0.RefNames[i] != r {
			t.Errorf("commit[0].RefNames[%d] = %q, want %q", i, c0.RefNames[i], r)
		}
	}

	if got[1].Parents != nil {
		t.Errorf("root commit should have no parents, got %v", got[1].Parents)
	}
	if got[1].RefNames != nil {
		t.Errorf("root commit RefNames = %v, want nil", got[1].RefNames)
	}
}

func TestParseLogEmptyDecorationYieldsNilRefNames(t *testing.T) {
	in := "abc\x00def\x00A\x00a@x\x001700000000\x00sub\x00\n"
	got, err := parseLog(strings.NewReader(in))
	if err != nil {
		t.Fatalf("parseLog: %v", err)
	}
	if len(got) != 1 || got[0].RefNames != nil {
		t.Errorf("RefNames = %v, want nil", got[0].RefNames)
	}
}

func TestParseLogMergeCommitHasMultipleParents(t *testing.T) {
	in := "merge1\x00p1 p2\x00Carol\x00carol@example.com\x001700000100\x00merge branch 'feat'\x00\n"
	got, err := parseLog(strings.NewReader(in))
	if err != nil {
		t.Fatalf("parseLog: %v", err)
	}
	if len(got) != 1 || len(got[0].Parents) != 2 {
		t.Fatalf("expected 1 commit with 2 parents, got %+v", got)
	}
	if got[0].Parents[0] != "p1" || got[0].Parents[1] != "p2" {
		t.Errorf("parents = %v, want [p1 p2]", got[0].Parents)
	}
}

func TestParseLineRejectsMalformed(t *testing.T) {
	if _, err := parseLine("only-one-field"); err == nil {
		t.Error("expected error on malformed line")
	}
}

// TestLogIntegration exercises the real git binary against a throwaway repo,
// which catches format-string drift between our %H%x00... template and what
// git actually emits.
func TestLogIntegration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()

	gitRun(t, dir, "init", "-b", "main")
	gitRun(t, dir, "commit", "--allow-empty", "-m", "first")
	gitRun(t, dir, "tag", "v0.0.1")
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	gitRun(t, dir, "add", "f")
	gitRun(t, dir, "commit", "-m", "second")

	commits, err := Log(context.Background(), LogOptions{Dir: dir})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(commits) != 2 {
		t.Fatalf("got %d commits, want 2", len(commits))
	}
	if commits[0].Subject != "second" || commits[1].Subject != "first" {
		t.Errorf("subjects = [%q, %q], want [second, first]", commits[0].Subject, commits[1].Subject)
	}
	if len(commits[0].Parents) != 1 {
		t.Errorf("HEAD should have one parent, got %v", commits[0].Parents)
	}
	if len(commits[1].Parents) != 0 {
		t.Errorf("root should have no parents, got %v", commits[1].Parents)
	}
	// HEAD 는 second 를 가리키므로 commits[0] 에 "HEAD -> main" 이 들어 있어야 한다.
	if !slices.Contains(commits[0].RefNames, "HEAD -> main") {
		t.Errorf("commits[0].RefNames = %v, want token %q", commits[0].RefNames, "HEAD -> main")
	}
	// v0.0.1 tag 는 first 에만 붙였으므로 commits[1] 에 "tag: v0.0.1" 이 들어 있어야 한다.
	if !slices.Contains(commits[1].RefNames, "tag: v0.0.1") {
		t.Errorf("commits[1].RefNames = %v, want token %q", commits[1].RefNames, "tag: v0.0.1")
	}
}

func TestLogReturnsErrorWithStderr(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	// A directory that isn't a git repo. git log should fail and we want
	// the wrapped error to carry stderr's message, not just "exit status N".
	dir := t.TempDir()
	_, err := Log(context.Background(), LogOptions{Dir: dir})
	if err == nil {
		t.Fatal("expected error running git log outside a repo")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("error %q should include git's stderr message", err)
	}
}

func TestLogStreamEmitsAllCommits(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()

	gitRun(t, dir, "init", "-b", "main")
	gitRun(t, dir, "commit", "--allow-empty", "-m", "first")
	gitRun(t, dir, "tag", "v0.0.1")
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	gitRun(t, dir, "add", "f")
	gitRun(t, dir, "commit", "-m", "second")

	ch, err := LogStream(context.Background(), LogOptions{Dir: dir})
	if err != nil {
		t.Fatalf("LogStream: %v", err)
	}

	var commits []Commit
	var trailingErr error
	for ev := range ch {
		if ev.Err != nil {
			trailingErr = ev.Err
			continue
		}
		commits = append(commits, ev.Commit)
	}
	if trailingErr != nil {
		t.Fatalf("trailing err: %v", trailingErr)
	}
	if len(commits) != 2 {
		t.Fatalf("got %d commits, want 2", len(commits))
	}
	if commits[0].Subject != "second" || commits[1].Subject != "first" {
		t.Errorf("subjects = [%q, %q], want [second, first]", commits[0].Subject, commits[1].Subject)
	}
	if len(commits[0].Parents) != 1 {
		t.Errorf("HEAD should have one parent, got %v", commits[0].Parents)
	}
	if len(commits[1].Parents) != 0 {
		t.Errorf("root should have no parents, got %v", commits[1].Parents)
	}
	if !slices.Contains(commits[0].RefNames, "HEAD -> main") {
		t.Errorf("commits[0].RefNames = %v, want token %q", commits[0].RefNames, "HEAD -> main")
	}
	if !slices.Contains(commits[1].RefNames, "tag: v0.0.1") {
		t.Errorf("commits[1].RefNames = %v, want token %q", commits[1].RefNames, "tag: v0.0.1")
	}
}

func TestLogStreamSurfacesStderrAsTrailingErr(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	ch, err := LogStream(context.Background(), LogOptions{Dir: dir})
	if err != nil {
		t.Fatalf("LogStream early err: %v", err)
	}
	var trailing error
	var commits int
	for ev := range ch {
		if ev.Err != nil {
			trailing = ev.Err
			continue
		}
		commits++
	}
	if commits != 0 {
		t.Errorf("got %d commits in non-repo dir, want 0", commits)
	}
	if trailing == nil {
		t.Fatal("expected trailing err for non-repo dir, got nil")
	}
	if !strings.Contains(trailing.Error(), "not a git repository") {
		t.Errorf("err %q should include git's stderr message", trailing)
	}
}

func TestLogStreamCancelStopsProducer(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitRun(t, dir, "init", "-b", "main")
	for i := 0; i < 5; i++ {
		gitRun(t, dir, "commit", "--allow-empty", "-m", "c")
	}

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := LogStream(ctx, LogOptions{Dir: dir})
	if err != nil {
		cancel()
		t.Fatalf("LogStream: %v", err)
	}
	select {
	case <-ch:
		// got at least one event; producer is past Start.
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("no event received within 2s")
	}
	cancel()

	// Channel must close in bounded time after cancel; otherwise the
	// producer goroutine has leaked.
	done := make(chan struct{})
	go func() {
		for range ch {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("channel did not close within 2s after cancel — producer leak")
	}
}
