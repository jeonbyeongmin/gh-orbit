// External-change detection for worktrees. fsnotify watches each
// worktree's `.git/HEAD` and `.git/index` so a commit / checkout / rebase
// from another shell or another worktree's AI agent flips the dashboard
// row without the user pressing `r`. Burst events from a single git op
// (HEAD.lock → rename, index rewrite, COMMIT_EDITMSG churn) coalesce via
// a 200ms trailing debounce keyed by worktree path.
//
// Init failure is silent-degrade: fsnotify.NewWatcher() returning err
// leaves the cockpit fully functional on the v1 manual-`r` reload path,
// only paints one status line so the user knows automatic detection is
// off. fsnotify dependency is import-isolated to this file — model.go
// only touches the typed msg and a small handle interface.
package tui

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// worktreeWatchedChangeMsg is dispatched by the watcher goroutine into
// the bubbletea main loop when a watched worktree's HEAD or index has
// settled (200ms idle since the last raw event for that worktree). The
// path is the worktree's working-tree path (matches git.Worktree.Path),
// not the .git/ directory that fsnotify saw the event on — the Update
// handler keys all worktree state by working-tree path.
type worktreeWatchedChangeMsg struct {
	path string
}

// worktreeWatchDebounce is the trailing-debounce window per worktree.
// Single git ops emit HEAD/index events in bursts (commit ≈ 3-5 events,
// rebase ≈ tens over seconds); 200ms is the threshold at which a user
// perceives "responded" without firing during the burst. Interview Q4 A.
const worktreeWatchDebounce = 200 * time.Millisecond

// worktreeWatcher coordinates one fsnotify.Watcher across all known
// worktrees. It owns two maps that mirror each other (byGitDir for event
// dispatch, byWtPath for Sync diff) and a per-worktree timer map for the
// trailing debounce. The sender hook is set once by Bind and is the only
// path from the watcher goroutine back into the bubbletea program.
type worktreeWatcher struct {
	fw *fsnotify.Watcher

	mu       sync.Mutex
	byGitDir map[string]string // resolved gitDir → worktree path
	byWtPath map[string]string // worktree path → resolved gitDir
	timers   map[string]*time.Timer

	debounce time.Duration
	sender   func(path string) // set by Bind; nil-safe in onRawEvent
}

// newWorktreeWatcher is the package-level seam tests swap to inject a
// fsnotify-free stub. Production path calls fsnotify.NewWatcher and
// returns a struct ready for Bind() + Sync().
var newWorktreeWatcher = func() (*worktreeWatcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("fsnotify.NewWatcher: %w", err)
	}
	return &worktreeWatcher{
		fw:       fw,
		byGitDir: make(map[string]string),
		byWtPath: make(map[string]string),
		timers:   make(map[string]*time.Timer),
		debounce: worktreeWatchDebounce,
	}, nil
}

// Bind attaches the bubbletea Program so the watcher goroutine can push
// worktreeWatchedChangeMsg into Update. Called once from cmd/orbit main
// after tea.NewProgram so the Program reference plumbing stays explicit
// — no global. Bind also starts the events goroutine; calling Bind twice
// would spawn duplicate goroutines, so it's invoked at most once.
func (w *worktreeWatcher) Bind(p *tea.Program) {
	if w == nil || w.fw == nil {
		return
	}
	w.mu.Lock()
	w.sender = func(path string) { p.Send(worktreeWatchedChangeMsg{path: path}) }
	w.mu.Unlock()
	go w.run()
}

// run drains fsnotify's Events and Errors channels until the watcher is
// Closed. Errors get logged (fsnotify guarantees the channels close on
// shutdown, ending the loop). The basename + path lookup live in
// onRawEvent so unit tests can exercise the filter + debounce path
// without a real fsnotify subscription.
func (w *worktreeWatcher) run() {
	for {
		select {
		case ev, ok := <-w.fw.Events:
			if !ok {
				return
			}
			w.onRawEvent(ev.Name)
		case err, ok := <-w.fw.Errors:
			if !ok {
				return
			}
			log.Printf("worktreewatch: fsnotify error: %v", err)
		}
	}
}

// onRawEvent applies the file-name filter (HEAD/index only) and starts
// or resets the per-worktree debounce timer. Exposed at package level
// (not exported) so tests can drive the debounce path without setting
// up a real fsnotify producer. Safe to call concurrently from the
// fsnotify goroutine and from tests.
func (w *worktreeWatcher) onRawEvent(eventName string) {
	base := filepath.Base(eventName)
	if base != "HEAD" && base != "index" {
		return
	}
	gitDir := filepath.Dir(eventName)

	w.mu.Lock()
	wtPath, ok := w.byGitDir[gitDir]
	if !ok {
		w.mu.Unlock()
		return
	}
	if t, exists := w.timers[wtPath]; exists {
		t.Reset(w.debounce)
		w.mu.Unlock()
		return
	}
	send := w.sender
	w.timers[wtPath] = time.AfterFunc(w.debounce, func() {
		w.mu.Lock()
		delete(w.timers, wtPath)
		w.mu.Unlock()
		if send != nil {
			send(wtPath)
		}
	})
	w.mu.Unlock()
}

// Sync reconciles the watcher's active set with the inventory of
// worktrees the model just received. Diff-based add/remove keeps the
// fsnotify watcher's interest list aligned with `git worktree list`
// without churning the timer map. Called from Update's
// worktreesLoadedMsg handler — every refresh of the worktree inventory
// drives a Sync.
//
// Linked-worktree gitDir resolution failure on one entry skips that
// entry only (logged once) — other worktrees continue to be watched.
func (w *worktreeWatcher) Sync(entries []git.Worktree) {
	if w == nil || w.fw == nil {
		return
	}
	desired := make(map[string]string, len(entries)) // wtPath → gitDir
	for _, e := range entries {
		gitDir, err := resolveGitDir(e)
		if err != nil {
			log.Printf("worktreewatch: resolveGitDir(%s): %v", e.Path, err)
			continue
		}
		desired[e.Path] = gitDir
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// Remove watches for worktrees that disappeared or whose gitDir changed.
	for wtPath, oldGitDir := range w.byWtPath {
		if newGitDir, ok := desired[wtPath]; ok && newGitDir == oldGitDir {
			continue
		}
		_ = w.fw.Remove(oldGitDir)
		delete(w.byGitDir, oldGitDir)
		delete(w.byWtPath, wtPath)
		if t, exists := w.timers[wtPath]; exists {
			t.Stop()
			delete(w.timers, wtPath)
		}
	}

	// Add watches for new worktrees or moved gitDirs.
	for wtPath, gitDir := range desired {
		if _, exists := w.byWtPath[wtPath]; exists {
			continue
		}
		if err := w.fw.Add(gitDir); err != nil {
			log.Printf("worktreewatch: fsnotify.Add(%s): %v", gitDir, err)
			continue
		}
		w.byGitDir[gitDir] = wtPath
		w.byWtPath[wtPath] = gitDir
	}
}

// Close releases the fsnotify watcher and stops any pending debounce
// timers. Idempotent and nil-safe — main.go's `defer w.Close()` is the
// normal shutdown path.
func (w *worktreeWatcher) Close() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	for _, t := range w.timers {
		t.Stop()
	}
	w.timers = nil
	w.mu.Unlock()
	if w.fw == nil {
		return nil
	}
	return w.fw.Close()
}

// resolveGitDir maps a Worktree to the directory fsnotify should watch.
// Main worktrees own the `.git` directory directly; linked worktrees
// have `.git` as a one-line file pointing at the per-tree storage under
// the main repo (`gitdir: <abspath>`). Parsing that pointer keeps the
// watcher aimed at the *actual* HEAD/index files git writes — watching
// the linked tree's `.git` file would just emit one event when git
// rewrites the pointer, which never happens during normal ops.
func resolveGitDir(wt git.Worktree) (string, error) {
	if wt.Path == "" {
		return "", fmt.Errorf("empty worktree path")
	}
	dotGit := filepath.Join(wt.Path, ".git")
	if wt.IsMain {
		return dotGit, nil
	}
	body, err := os.ReadFile(dotGit)
	if err != nil {
		return "", fmt.Errorf("read .git pointer: %w", err)
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "gitdir:") {
			path := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
			if path == "" {
				return "", fmt.Errorf(".git pointer missing gitdir value")
			}
			if !filepath.IsAbs(path) {
				path = filepath.Join(wt.Path, path)
			}
			return filepath.Clean(path), nil
		}
	}
	return "", fmt.Errorf(".git pointer has no gitdir: line")
}
