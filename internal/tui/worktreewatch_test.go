package tui

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

func TestResolveGitDirMain(t *testing.T) {
	wt := git.Worktree{Path: "/repo/main", IsMain: true}
	got, err := resolveGitDir(wt)
	if err != nil {
		t.Fatalf("resolveGitDir: %v", err)
	}
	want := filepath.Join("/repo/main", ".git")
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestResolveGitDirLinkedAbsolute(t *testing.T) {
	root := t.TempDir()
	wtPath := filepath.Join(root, "linked")
	if err := os.Mkdir(wtPath, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	gitDirAbs := filepath.Join(root, ".git", "worktrees", "linked")
	if err := os.MkdirAll(gitDirAbs, 0o755); err != nil {
		t.Fatalf("mkdir gitdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wtPath, ".git"), []byte("gitdir: "+gitDirAbs+"\n"), 0o644); err != nil {
		t.Fatalf("write .git: %v", err)
	}
	got, err := resolveGitDir(git.Worktree{Path: wtPath})
	if err != nil {
		t.Fatalf("resolveGitDir: %v", err)
	}
	if got != gitDirAbs {
		t.Errorf("got %q want %q", got, gitDirAbs)
	}
}

func TestResolveGitDirLinkedRelative(t *testing.T) {
	// git ≥ 2.36 sometimes writes a relative gitdir. Confirm the resolver
	// joins it against the worktree path.
	root := t.TempDir()
	wtPath := filepath.Join(root, "linked")
	if err := os.Mkdir(wtPath, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	rel := "../.git/worktrees/linked"
	if err := os.WriteFile(filepath.Join(wtPath, ".git"), []byte("gitdir: "+rel+"\n"), 0o644); err != nil {
		t.Fatalf("write .git: %v", err)
	}
	got, err := resolveGitDir(git.Worktree{Path: wtPath})
	if err != nil {
		t.Fatalf("resolveGitDir: %v", err)
	}
	want := filepath.Clean(filepath.Join(wtPath, rel))
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestResolveGitDirLinkedMissing(t *testing.T) {
	got, err := resolveGitDir(git.Worktree{Path: filepath.Join(t.TempDir(), "missing")})
	if err == nil {
		t.Fatalf("expected error for missing .git, got %q", got)
	}
}

func TestResolveGitDirLinkedMalformed(t *testing.T) {
	root := t.TempDir()
	wtPath := filepath.Join(root, "broken")
	if err := os.Mkdir(wtPath, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wtPath, ".git"), []byte("not a gitdir line\n"), 0o644); err != nil {
		t.Fatalf("write .git: %v", err)
	}
	if _, err := resolveGitDir(git.Worktree{Path: wtPath}); err == nil {
		t.Fatalf("expected error for missing gitdir: line")
	}
}

// newTestWatcher builds a watcher without invoking fsnotify.NewWatcher.
// fw stays nil — callers that exercise debounce must avoid Sync (which
// is nil-safe via the fw check) and call onRawEvent directly with a
// pre-populated byGitDir map.
func newTestWatcher(debounce time.Duration, sender func(string)) *worktreeWatcher {
	return &worktreeWatcher{
		byGitDir: make(map[string]string),
		byWtPath: make(map[string]string),
		timers:   make(map[string]*time.Timer),
		debounce: debounce,
		sender:   sender,
	}
}

func TestOnRawEventDebouncesBurst(t *testing.T) {
	var hits atomic.Int32
	done := make(chan string, 4)
	w := newTestWatcher(20*time.Millisecond, func(p string) {
		hits.Add(1)
		done <- p
	})
	w.byGitDir["/wt/.git"] = "/wt"

	// Burst: 5 raw events within the debounce window. Only one send.
	for i := 0; i < 5; i++ {
		w.onRawEvent("/wt/.git/HEAD", fsnotify.Write)
		time.Sleep(2 * time.Millisecond)
	}
	select {
	case p := <-done:
		if p != "/wt" {
			t.Errorf("sender got %q want /wt", p)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("debounce never fired")
	}
	// No second send arrives.
	select {
	case p := <-done:
		t.Errorf("unexpected second send: %q", p)
	case <-time.After(60 * time.Millisecond):
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("sender invocations: got %d want 1", got)
	}
}

func TestOnRawEventFiltersByBasename(t *testing.T) {
	var fired bool
	var mu sync.Mutex
	w := newTestWatcher(10*time.Millisecond, func(string) {
		mu.Lock()
		fired = true
		mu.Unlock()
	})
	w.byGitDir["/wt/.git"] = "/wt"

	// Files git also rewrites that are NOT HEAD or index — must be ignored.
	for _, name := range []string{"COMMIT_EDITMSG", "ORIG_HEAD", "FETCH_HEAD", "packed-refs", "HEAD.lock"} {
		w.onRawEvent("/wt/.git/"+name, fsnotify.Write)
	}
	time.Sleep(40 * time.Millisecond)
	mu.Lock()
	if fired {
		t.Errorf("non-HEAD/non-index event triggered sender")
	}
	mu.Unlock()
}

func TestOnRawEventIndexBasenameTriggers(t *testing.T) {
	done := make(chan string, 1)
	w := newTestWatcher(15*time.Millisecond, func(p string) { done <- p })
	w.byGitDir["/wt/.git"] = "/wt"

	w.onRawEvent("/wt/.git/index", fsnotify.Write)
	select {
	case p := <-done:
		if p != "/wt" {
			t.Errorf("got %q want /wt", p)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("index event did not trigger debounce")
	}
}

// TestOnRawEventIgnoresChmod pins the loop-breaking op filter: a Chmod-only
// event on .git/index must NOT trigger the debounce. `git status` (run by the
// dashboard dirty fan-out on every reload) touches the index's metadata even
// with --no-optional-locks, emitting a lone Chmod; reacting to it feeds a
// reload → status → Chmod → reload flicker loop. Real changes arrive as
// Write/Create/Remove/Rename (covered by the sibling tests) and still fire.
func TestOnRawEventIgnoresChmod(t *testing.T) {
	w := newTestWatcher(10*time.Millisecond, func(string) {
		t.Errorf("Chmod-only event must not trigger sender")
	})
	w.byGitDir["/wt/.git"] = "/wt"

	w.onRawEvent("/wt/.git/index", fsnotify.Chmod)
	w.onRawEvent("/wt/.git/HEAD", fsnotify.Chmod)
	time.Sleep(40 * time.Millisecond)

	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.timers) != 0 {
		t.Errorf("Chmod-only events armed %d debounce timer(s), want 0", len(w.timers))
	}
}

func TestOnRawEventUnknownGitDir(t *testing.T) {
	w := newTestWatcher(10*time.Millisecond, func(string) {
		t.Errorf("sender should not fire for unregistered gitDir")
	})
	// byGitDir empty.
	w.onRawEvent("/somewhere/.git/HEAD", fsnotify.Write)
	time.Sleep(40 * time.Millisecond)
}

// Linked-worktree resolve path edge case worth pinning: TrimSpace on the
// gitdir value matters when git emits a trailing CRLF on Windows-y
// fixtures — the resolver must hand back a clean path.
func TestResolveGitDirTrimsWhitespace(t *testing.T) {
	root := t.TempDir()
	wtPath := filepath.Join(root, "wt")
	if err := os.Mkdir(wtPath, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	target := filepath.Join(root, ".git", "worktrees", "wt")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wtPath, ".git"), []byte("gitdir:   "+target+"   \r\n"), 0o644); err != nil {
		t.Fatalf("write .git: %v", err)
	}
	got, err := resolveGitDir(git.Worktree{Path: wtPath})
	if err != nil {
		t.Fatalf("resolveGitDir: %v", err)
	}
	if got != target {
		t.Errorf("got %q want %q (raw whitespace not stripped?)", got, target)
	}
	if strings.ContainsAny(got, "\r\n") {
		t.Errorf("path retains newline characters: %q", got)
	}
}
