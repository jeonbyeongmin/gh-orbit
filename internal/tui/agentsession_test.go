package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestAgentSessionSlug(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"/Users/x/project/gh-orbit/.claude/worktrees/feat+wt-sort",
			"-Users-x-project-gh-orbit--claude-worktrees-feat-wt-sort"},
		{"/repo/main", "-repo-main"},
		{"abc123", "abc123"},
	}
	for _, c := range cases {
		if got := agentSessionSlug(c.in); got != c.want {
			t.Errorf("agentSessionSlug(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// writeTranscriptContent creates projectsDir/<slug(wtPath)>/<name> with the
// given jsonl body + mtime, mirroring Claude Code's ~/.claude/projects layout.
func writeTranscriptContent(t *testing.T, projectsDir, wtPath, name, body string, mtime time.Time) {
	t.Helper()
	dir := filepath.Join(projectsDir, agentSessionSlug(wtPath))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
}

func TestAgentStateForWorktree(t *testing.T) {
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	const window = 10 * time.Minute
	const wt = "/repo/wt"
	fresh := now.Add(-1 * time.Minute)

	cases := []struct {
		name string
		body string
		want agentState
	}{
		{"tool_use is running",
			`{"type":"assistant","message":{"stop_reason":"tool_use"}}` + "\n", agentStateRunning},
		{"user entry is running",
			`{"type":"assistant","message":{"stop_reason":"end_turn"}}` + "\n" +
				`{"type":"user"}` + "\n", agentStateRunning},
		{"end_turn is parked",
			`{"type":"assistant","message":{"stop_reason":"end_turn"}}` + "\n", agentStateParked},
		{"unparseable fresh transcript is unknown-active",
			"not json at all\n", agentStateUnknownActive},
		{"empty fresh transcript is unknown-active",
			"\n", agentStateUnknownActive},
		{"worktree-state match keeps the parsed state",
			`{"type":"worktree-state","worktreeSession":{"worktreePath":"/repo/wt"}}` + "\n" +
				`{"type":"assistant","message":{"stop_reason":"tool_use"}}` + "\n", agentStateRunning},
		{"worktree-state mismatch is suppressed (slug collision)",
			`{"type":"worktree-state","worktreeSession":{"worktreePath":"/repo/OTHER"}}` + "\n" +
				`{"type":"assistant","message":{"stop_reason":"tool_use"}}` + "\n", agentStateNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			projects := t.TempDir()
			writeTranscriptContent(t, projects, wt, "a.jsonl", c.body, fresh)
			if got := agentStateForWorktree(projects, wt, now, window); got != c.want {
				t.Errorf("agentStateForWorktree = %v, want %v", got, c.want)
			}
		})
	}

	t.Run("stale transcript is none regardless of content", func(t *testing.T) {
		projects := t.TempDir()
		writeTranscriptContent(t, projects, wt, "a.jsonl",
			`{"type":"assistant","message":{"stop_reason":"tool_use"}}`+"\n", now.Add(-11*time.Minute))
		if got := agentStateForWorktree(projects, wt, now, window); got != agentStateNone {
			t.Errorf("stale transcript = %v, want none", got)
		}
	})

	t.Run("newest transcript decides the state", func(t *testing.T) {
		projects := t.TempDir()
		writeTranscriptContent(t, projects, wt, "old.jsonl",
			`{"type":"assistant","message":{"stop_reason":"tool_use"}}`+"\n", now.Add(-5*time.Minute))
		writeTranscriptContent(t, projects, wt, "new.jsonl",
			`{"type":"assistant","message":{"stop_reason":"end_turn"}}`+"\n", fresh)
		if got := agentStateForWorktree(projects, wt, now, window); got != agentStateParked {
			t.Errorf("newest (parked) should win, got %v", got)
		}
	})

	t.Run("missing projects dir is none", func(t *testing.T) {
		if got := agentStateForWorktree(filepath.Join(t.TempDir(), "nope"), wt, now, window); got != agentStateNone {
			t.Errorf("missing projectsDir = %v, want none", got)
		}
	})

	t.Run("exactly at window boundary is active", func(t *testing.T) {
		projects := t.TempDir()
		writeTranscriptContent(t, projects, wt, "a.jsonl",
			`{"type":"assistant","message":{"stop_reason":"tool_use"}}`+"\n", now.Add(-window))
		if got := agentStateForWorktree(projects, wt, now, window); got != agentStateRunning {
			t.Errorf("at exact 10m boundary (<=) = %v, want running", got)
		}
	})

	t.Run("empty projectsDir arg is none", func(t *testing.T) {
		if got := agentStateForWorktree("", wt, now, window); got != agentStateNone {
			t.Errorf("empty projectsDir = %v, want none", got)
		}
	})

	t.Run("slug dir with no jsonl is none", func(t *testing.T) {
		projects := t.TempDir()
		dir := filepath.Join(projects, agentSessionSlug(wt))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		if got := agentStateForWorktree(projects, wt, now, window); got != agentStateNone {
			t.Errorf("slug dir with no .jsonl = %v, want none", got)
		}
	})

	t.Run("nested subagent jsonl is ignored (direct files only)", func(t *testing.T) {
		projects := t.TempDir()
		sub := filepath.Join(projects, agentSessionSlug(wt), "session", "subagents")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		p := filepath.Join(sub, "agent.jsonl")
		if err := os.WriteFile(p, []byte(`{"type":"assistant","message":{"stop_reason":"tool_use"}}`), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		if err := os.Chtimes(p, now, now); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
		if got := agentStateForWorktree(projects, wt, now, window); got != agentStateNone {
			t.Errorf("nested subagent jsonl must not count = %v, want none", got)
		}
	})
}

func TestAgentSessionPollCmdReportsStateSet(t *testing.T) {
	projects := t.TempDir()
	now := time.Now()
	const running = `{"type":"assistant","message":{"stop_reason":"tool_use"}}` + "\n"
	writeTranscriptContent(t, projects, "/wt/live", "a.jsonl", running, now.Add(-1*time.Minute))
	writeTranscriptContent(t, projects, "/wt/idle", "a.jsonl", running, now.Add(-30*time.Minute))

	msg, ok := agentSessionPollCmd(projects, []string{"/wt/live", "/wt/idle"}, now, 42)().(agentSessionPollMsg)
	if !ok {
		t.Fatal("poll cmd should produce agentSessionPollMsg")
	}
	if msg.reqID != 42 {
		t.Errorf("poll cmd should propagate reqID: got %d want 42", msg.reqID)
	}
	if msg.states["/wt/live"] != agentStateRunning {
		t.Errorf("/wt/live (1m, tool_use) should be running, got %v", msg.states["/wt/live"])
	}
	if msg.states["/wt/idle"] != agentStateNone {
		t.Errorf("/wt/idle (30m old) should be none, got %v", msg.states["/wt/idle"])
	}
}

func TestAgentSessionPollMsgAppliesFreshReqID(t *testing.T) {
	m := New()
	updated, cmd := m.Update(agentSessionPollMsg{reqID: m.sidebarWorktreesReqID, states: map[string]agentState{"/wt": agentStateParked}})
	if updated.(Model).refs.AgentState("/wt") != agentStateParked {
		t.Error("matching-reqID poll should set agent state")
	}
	if cmd != nil {
		t.Error("poll msg must NOT re-arm the tick — re-arm lives in the tick handler")
	}
}

func TestAgentSessionPollMsgDropsStaleReqID(t *testing.T) {
	m := New()
	updated, _ := m.Update(agentSessionPollMsg{reqID: m.sidebarWorktreesReqID + 7, states: map[string]agentState{"/gone": agentStateRunning}})
	if updated.(Model).refs.AgentState("/gone") != agentStateNone {
		t.Error("stale-reqID poll must be dropped, not applied — no orphan key for a pruned worktree")
	}
}

func TestAgentSessionTickMsgPollsAndRearms(t *testing.T) {
	m := New()
	_, cmd := m.Update(agentSessionTickMsg{})
	if cmd == nil {
		t.Fatal("tick handler should dispatch a poll + re-arm")
	}
	// tea.Batch returns a BatchMsg of the sub-cmds without executing them, so
	// this never blocks on the 30s tick. Expect exactly two: the poll + the
	// re-armed tick.
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("tick handler should return a tea.Batch, got %T", cmd())
	}
	if len(batch) != 2 {
		t.Errorf("tick handler should dispatch both a poll and a re-arm tick: got %d cmds", len(batch))
	}
}
