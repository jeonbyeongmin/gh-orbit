// Claude Code agent-session detection for the worktree dashboard. A 🤖
// marker on a worktree row means an agent session has touched that tree
// recently — answering the cockpit's core "which worktree is an agent in
// right now?" question.
//
// The only filesystem signal that tracks activity is the mtime of the
// session transcripts Claude Code writes under
// ~/.claude/projects/<slug>/*.jsonl. The <slug> is the worktree's absolute
// path with every non-alphanumeric byte replaced by '-' — an UNDOCUMENTED
// Claude Code internal convention. Everything here is silent-degrade: if
// the convention changes, the home dir is unreadable, or no transcript
// exists, the marker simply doesn't render. No error, no status line, no
// crash — exactly the spirit of the fsnotify watcher's degrade path.
//
// Process-based detection (claude PID + cwd) was rejected: background-job
// and desktop-app sessions keep their process cwd at the repo root, never
// the worktree, so cwd matching can't see the real usage pattern.
package tui

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// agentSessionFreshness is how recent a worktree's newest session-transcript
// mtime must be for the row to count as agent-active. The transcript is
// appended on every assistant / tool event, so an actively working agent
// refreshes it every few seconds; an idle (awaiting-input) session stops
// touching it. 10m tolerates a brief idle gap without flicker while still
// dropping a session that has clearly ended — and doubles as the
// stale-correction mechanism (the marker ages out on its own once mtime
// passes the window, so no separate false-positive cleanup is needed).
const agentSessionFreshness = 10 * time.Minute

// agentSessionSlug maps a worktree's absolute path to the directory name
// Claude Code uses under ~/.claude/projects/: every non-alphanumeric byte
// becomes '-'. e.g. ".../worktrees/feat+x" → "...-worktrees-feat-x". Byte
// iteration matches the ASCII paths git produces; a non-ASCII path that
// slugs differently just yields no match → silent-degrade (no marker).
func agentSessionSlug(worktreePath string) string {
	b := make([]byte, len(worktreePath))
	for i := 0; i < len(worktreePath); i++ {
		switch c := worktreePath[i]; {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			b[i] = c
		default:
			b[i] = '-'
		}
	}
	return string(b)
}

// agentActiveForWorktree reports whether an agent session touched
// worktreePath within window of now, judged by the newest mtime among
// projectsDir/<slug>/*.jsonl. A missing projectsDir, an unresolvable slug
// dir, an unreadable entry, or no transcript at all all return false — the
// caller renders no marker rather than surfacing an error.
func agentActiveForWorktree(projectsDir, worktreePath string, now time.Time, window time.Duration) bool {
	if projectsDir == "" {
		return false
	}
	entries, err := os.ReadDir(filepath.Join(projectsDir, agentSessionSlug(worktreePath)))
	if err != nil {
		return false
	}
	var newest time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if mt := info.ModTime(); mt.After(newest) {
			newest = mt
		}
	}
	if newest.IsZero() {
		return false
	}
	return now.Sub(newest) <= window
}

// agentSessionPollInterval is the cadence of the agent-session poll. The
// signal lives outside the worktree's .git (under ~/.claude/projects/), so
// the fsnotify watcher that drives dirty / branch updates can't see it; a
// lightweight stat-only tick re-derives every worktree's 🤖 state instead.
// Fully decoupled from the git-status dirty fan-out (which owns a 3s budget
// and touches git index locks) — this only stat()s transcript files.
const agentSessionPollInterval = 30 * time.Second

// agentSessionProjectsDir resolves ~/.claude/projects. A package-level var
// so tests can repoint it at a fixture dir. Returns "" when the home dir is
// unknown, which makes agentActiveForWorktree degrade to "no marker".
var agentSessionProjectsDir = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

// agentSessionTickMsg fires on the poll cadence; its handler dispatches a
// fresh agentSessionPollCmd for the current worktree set.
type agentSessionTickMsg struct{}

// agentSessionPollMsg carries one poll's result (worktree path → active).
// Its handler writes the map into refModel and re-arms the tick.
type agentSessionPollMsg struct {
	active map[string]bool
}

// agentSessionTickCmd arms the next poll tick. Init fires it once; the
// agentSessionPollMsg handler re-fires it after each poll, so exactly one
// tick is ever in flight (the codebase has no other recurring tick — every
// existing tea.Tick is one-shot, so this self-rearm is the whole loop).
func agentSessionTickCmd() tea.Cmd {
	return tea.Tick(agentSessionPollInterval, func(time.Time) tea.Msg {
		return agentSessionTickMsg{}
	})
}

// agentSessionPollCmd stats projectsDir/<slug>/*.jsonl for each path off the
// main loop and reports the active set. Pure read-only stat: it never
// mutates and carries no reqID — a stale result (e.g. computed across a
// worktree switch) is harmless because the next tick re-derives from the
// then-current inventory.
func agentSessionPollCmd(projectsDir string, paths []string, now time.Time) tea.Cmd {
	return func() tea.Msg {
		active := make(map[string]bool, len(paths))
		for _, p := range paths {
			active[p] = agentActiveForWorktree(projectsDir, p, now, agentSessionFreshness)
		}
		return agentSessionPollMsg{active: active}
	}
}
