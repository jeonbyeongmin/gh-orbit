// Claude Code agent-session detection for the worktree dashboard. The marker
// on a worktree row reports the state of an agent session on that tree
// (running / parked / unknown-active / none) — answering the cockpit's core
// "which worktree is an agent in right now, and does it need me?" question.
//
// Sessions are located by mtime of the transcripts Claude Code writes under
// ~/.claude/projects/<slug>/*.jsonl, where <slug> is the worktree's absolute
// path with every non-alphanumeric byte replaced by '-' — an UNDOCUMENTED
// Claude Code internal convention. mtime drives presence/staleness; the
// transcript's tail (last entry) and head (worktree-state) refine the state.
// Everything here is silent-degrade: if the convention or schema changes, the
// home dir is unreadable, or no transcript exists, it falls back to a coarser
// state (down to "no marker") — no error, no status line, no crash, exactly
// the spirit of the fsnotify watcher's degrade path.
//
// Process-based detection (claude PID + cwd) was rejected: background-job
// and desktop-app sessions keep their process cwd at the repo root, never
// the worktree, so cwd matching can't see the real usage pattern.
package tui

import (
	"encoding/json"
	"io"
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

// agentState is the per-worktree agent-session state painted on the dashboard
// row. The zero value is agentStateNone so a path the poll has never seen
// renders no marker.
type agentState int

const (
	agentStateNone          agentState = iota // no recent session → no marker
	agentStateRunning                         // last entry tool_use / user → actively working
	agentStateParked                          // last entry end_turn → awaiting user input
	agentStateUnknownActive                   // mtime fresh but state unparseable → v1-level fallback
)

// agentTranscriptWindow caps how many bytes we read from each end of a
// transcript. Active files reach 220KB+, so we seek-read only: the head holds
// the session-start worktree-state entry (worktreePath confirm), the tail
// holds the last assistant / user entry (running vs parked). 16KB comfortably
// covers either side without ever loading the whole file.
const agentTranscriptWindow = 16 << 10

// agentEntry is the partial shape we need from a transcript line to judge the
// current state: the entry type and, for assistant turns, the stop_reason.
// Everything else in the (undocumented) Claude Code schema is ignored — a line
// that doesn't carry these fields simply doesn't match and is skipped.
type agentEntry struct {
	Type    string `json:"type"`
	Message struct {
		StopReason string `json:"stop_reason"`
	} `json:"message"`
}

// agentWorktreeStateEntry is the partial shape of the session-start
// `worktree-state` line, which records the worktree this session is bound to.
// Used to confirm a slug-located transcript actually belongs to the row being
// evaluated (slug-collision false-positive guard).
type agentWorktreeStateEntry struct {
	Type            string `json:"type"`
	WorktreeSession struct {
		WorktreePath string `json:"worktreePath"`
	} `json:"worktreeSession"`
}

// agentStateForWorktree judges the agent-session state of worktreePath from
// the newest transcript under projectsDir/<slug>/. mtime drives presence /
// staleness exactly as v1 did; when fresh, the transcript's head confirms the
// worktree binding (slug-collision guard) and its tail decides running vs
// parked. Every failure path degrades safely: a stale / absent / unreadable
// transcript → agentStateNone; a fresh transcript we can't parse the state of
// → agentStateUnknownActive (never a regression below v1's "something here").
func agentStateForWorktree(projectsDir, worktreePath string, now time.Time, window time.Duration) agentState {
	if projectsDir == "" {
		return agentStateNone
	}
	dir := filepath.Join(projectsDir, agentSessionSlug(worktreePath))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return agentStateNone
	}
	var newestName string
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
			newest, newestName = mt, e.Name()
		}
	}
	if newest.IsZero() || now.Sub(newest) > window {
		return agentStateNone
	}
	path := filepath.Join(dir, newestName)
	// Slug-collision guard: if the active transcript records a worktree-state
	// path that disagrees with this row, two paths collapsed to one slug and
	// the marker would point at the wrong tree — drop it. An absent / unreadable
	// worktree-state entry means we can't confirm, so we trust the slug locate
	// (v1 behavior) rather than hide a real session.
	if match, found := agentTranscriptWorktreeMatches(path, worktreePath); found && !match {
		return agentStateNone
	}
	if st, ok := agentTranscriptTailState(path); ok {
		return st
	}
	return agentStateUnknownActive
}

// agentTranscriptTailState reads the last agentTranscriptWindow bytes and
// returns the state implied by the last complete assistant / user entry,
// scanning backward. ok=false means the tail was unreadable or held no
// recognizable entry — the caller then applies its fresh-mtime fallback. A
// partial first line (the window almost always cuts mid-line) just fails to
// unmarshal and is skipped, so no boundary handling is needed.
func agentTranscriptTailState(path string) (agentState, bool) {
	data, ok := readFileTail(path, agentTranscriptWindow)
	if !ok {
		return agentStateNone, false
	}
	lines := strings.Split(string(data), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var e agentEntry
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		switch e.Type {
		case "assistant":
			if e.Message.StopReason == "end_turn" {
				return agentStateParked, true
			}
			return agentStateRunning, true
		case "user":
			return agentStateRunning, true
		}
		// system / summary / worktree-state / etc. — keep scanning back.
	}
	return agentStateNone, false
}

// agentTranscriptWorktreeMatches scans the head of a transcript for the
// session-start worktree-state entry. Returns (paths-equal, found). found=false
// (absent / unreadable) tells the caller to trust the slug locate; found=true
// with a mismatch is the slug-collision false-positive to suppress.
func agentTranscriptWorktreeMatches(path, worktreePath string) (match, found bool) {
	data, ok := readFileHead(path, agentTranscriptWindow)
	if !ok {
		return false, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e agentWorktreeStateEntry
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		if e.Type == "worktree-state" {
			return e.WorktreeSession.WorktreePath == worktreePath, true
		}
	}
	return false, false
}

// readFileHead returns up to n bytes from the start of path. ok=false on an
// unreadable file (silent-degrade — no marker / trust-slug fallback upstream).
func readFileHead(path string, n int) ([]byte, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer func() { _ = f.Close() }() // read-only; close error is irrelevant
	buf := make([]byte, n)
	c, err := f.Read(buf)
	if c == 0 && err != nil {
		return nil, false
	}
	return buf[:c], true
}

// readFileTail returns up to the last n bytes of path via a single ReadAt from
// a computed offset — never reading the (potentially multi-hundred-KB) body.
func readFileTail(path string, n int) ([]byte, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer func() { _ = f.Close() }() // read-only; close error is irrelevant
	info, err := f.Stat()
	if err != nil {
		return nil, false
	}
	start := int64(0)
	if size := info.Size(); size > int64(n) {
		start = size - int64(n)
	}
	buf := make([]byte, info.Size()-start)
	c, err := f.ReadAt(buf, start)
	if c == 0 && err != nil && err != io.EOF {
		return nil, false
	}
	return buf[:c], true
}

// agentSessionPollInterval is the cadence of the agent-session poll. The
// signal lives outside the worktree's .git (under ~/.claude/projects/), so
// the fsnotify watcher that drives dirty / branch updates can't see it; a
// lightweight tick re-derives every worktree's agent state instead. Fully
// decoupled from the git-status dirty fan-out (which owns a 3s budget and
// touches git index locks) — this only stats + bounded seek-reads transcripts.
const agentSessionPollInterval = 30 * time.Second

// agentSessionProjectsDir resolves ~/.claude/projects. A package-level var
// so tests can repoint it at a fixture dir. Returns "" when the home dir is
// unknown, which makes agentStateForWorktree degrade to "no marker".
var agentSessionProjectsDir = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

// agentSessionTickMsg fires on the poll cadence; its handler dispatches a
// fresh agentSessionPollCmd AND re-arms the next tick (the tick handler is
// the sole re-arm site, so exactly one tick lineage is ever in flight even
// though worktreesLoadedMsg also dispatches event-driven polls).
type agentSessionTickMsg struct{}

// agentSessionPollMsg carries one poll's result (worktree path → state),
// tagged with the sidebarWorktreesReqID it was computed against. Its handler
// applies the map only when the reqID still matches the live inventory — a
// poll computed before a worktree was pruned/added is dropped so it can't
// re-insert an orphan key. It does NOT re-arm the tick (that's the tick
// handler's job), so dropping a stale poll never kills the loop.
type agentSessionPollMsg struct {
	reqID  uint64
	states map[string]agentState
}

// agentSessionTickCmd arms one poll tick. Init fires it once; the
// agentSessionTickMsg handler re-fires it every cadence (the codebase has no
// other recurring tick — every existing tea.Tick is one-shot).
func agentSessionTickCmd() tea.Cmd {
	return tea.Tick(agentSessionPollInterval, func(time.Time) tea.Msg {
		return agentSessionTickMsg{}
	})
}

// agentSpinnerInterval is the cadence the running marker advances frames at.
// It is a SEPARATE lineage from the 30s state poll: state detection stays at
// agentSessionPollInterval (a parked transition surfaces on the next poll),
// while this fast tick only re-renders the spinner glyph. Critically it is
// gated — armed only while a worktree is actually running and never re-armed
// once none are (see the agentSpinnerTickMsg handler), so an idle cockpit
// re-renders zero times.
const agentSpinnerInterval = 100 * time.Millisecond

// agentSpinnerTickMsg advances the running-marker spinner frame. Its handler
// is the sole re-arm site, and it only re-arms while AnyAgentRunning holds, so
// exactly one spinner lineage is ever in flight and it dies on idle.
type agentSpinnerTickMsg struct{}

func agentSpinnerTickCmd() tea.Cmd {
	return tea.Tick(agentSpinnerInterval, func(time.Time) tea.Msg {
		return agentSpinnerTickMsg{}
	})
}

// agentSessionPollCmd reads projectsDir/<slug>/*.jsonl for each path off the
// main loop and reports the state set tagged with reqID. Read-only (stat +
// bounded seek-read): it never mutates refModel. The reqID lets the handler
// drop a result that raced an inventory change.
func agentSessionPollCmd(projectsDir string, paths []string, now time.Time, reqID uint64) tea.Cmd {
	return func() tea.Msg {
		states := make(map[string]agentState, len(paths))
		for _, p := range paths {
			states[p] = agentStateForWorktree(projectsDir, p, now, agentSessionFreshness)
		}
		return agentSessionPollMsg{reqID: reqID, states: states}
	}
}
