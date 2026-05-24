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

// writeTranscript creates projectsDir/<slug(wtPath)>/<name> with the given
// mtime, mirroring Claude Code's ~/.claude/projects/<slug>/*.jsonl layout.
func writeTranscript(t *testing.T, projectsDir, wtPath, name string, mtime time.Time) {
	t.Helper()
	dir := filepath.Join(projectsDir, agentSessionSlug(wtPath))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
}

func TestAgentActiveForWorktree(t *testing.T) {
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	const window = 10 * time.Minute
	const wt = "/repo/wt"

	t.Run("fresh jsonl is active", func(t *testing.T) {
		projects := t.TempDir()
		writeTranscript(t, projects, wt, "a.jsonl", now.Add(-1*time.Minute))
		if !agentActiveForWorktree(projects, wt, now, window) {
			t.Error("want active for 1m-old transcript")
		}
	})

	t.Run("stale jsonl is inactive", func(t *testing.T) {
		projects := t.TempDir()
		writeTranscript(t, projects, wt, "a.jsonl", now.Add(-11*time.Minute))
		if agentActiveForWorktree(projects, wt, now, window) {
			t.Error("want inactive for 11m-old transcript")
		}
	})

	t.Run("exactly at window boundary is active", func(t *testing.T) {
		projects := t.TempDir()
		writeTranscript(t, projects, wt, "a.jsonl", now.Add(-window))
		if !agentActiveForWorktree(projects, wt, now, window) {
			t.Error("want active at exact 10m boundary (<=)")
		}
	})

	t.Run("newest of several jsonls wins", func(t *testing.T) {
		projects := t.TempDir()
		writeTranscript(t, projects, wt, "old.jsonl", now.Add(-30*time.Minute))
		writeTranscript(t, projects, wt, "new.jsonl", now.Add(-2*time.Minute))
		if !agentActiveForWorktree(projects, wt, now, window) {
			t.Error("want active when the newest of several is fresh")
		}
	})

	t.Run("missing projects dir is inactive", func(t *testing.T) {
		if agentActiveForWorktree(filepath.Join(t.TempDir(), "nope"), wt, now, window) {
			t.Error("want inactive when projectsDir is absent")
		}
	})

	t.Run("empty projectsDir arg is inactive", func(t *testing.T) {
		if agentActiveForWorktree("", wt, now, window) {
			t.Error("want inactive when projectsDir is empty")
		}
	})

	t.Run("slug dir with no jsonl is inactive", func(t *testing.T) {
		projects := t.TempDir()
		dir := filepath.Join(projects, agentSessionSlug(wt))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		if agentActiveForWorktree(projects, wt, now, window) {
			t.Error("want inactive when slug dir holds no .jsonl")
		}
	})

	t.Run("subdir jsonl is ignored (v1 = direct files only)", func(t *testing.T) {
		projects := t.TempDir()
		sub := filepath.Join(projects, agentSessionSlug(wt), "session", "subagents")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		p := filepath.Join(sub, "agent.jsonl")
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		if err := os.Chtimes(p, now, now); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
		if agentActiveForWorktree(projects, wt, now, window) {
			t.Error("want inactive: nested subagent jsonl must not count in v1")
		}
	})
}

func TestAgentSessionPollCmdReportsActiveSet(t *testing.T) {
	projects := t.TempDir()
	now := time.Now()
	writeTranscript(t, projects, "/wt/live", "a.jsonl", now.Add(-1*time.Minute))
	writeTranscript(t, projects, "/wt/idle", "a.jsonl", now.Add(-30*time.Minute))

	msg, ok := agentSessionPollCmd(projects, []string{"/wt/live", "/wt/idle"}, now, 42)().(agentSessionPollMsg)
	if !ok {
		t.Fatal("poll cmd should produce agentSessionPollMsg")
	}
	if msg.reqID != 42 {
		t.Errorf("poll cmd should propagate reqID: got %d want 42", msg.reqID)
	}
	if !msg.active["/wt/live"] {
		t.Error("/wt/live (1m old) should be active")
	}
	if msg.active["/wt/idle"] {
		t.Error("/wt/idle (30m old) should be inactive")
	}
}

func TestAgentSessionPollMsgAppliesFreshReqID(t *testing.T) {
	m := New()
	updated, cmd := m.Update(agentSessionPollMsg{reqID: m.sidebarWorktreesReqID, active: map[string]bool{"/wt": true}})
	if !updated.(Model).refs.AgentActive("/wt") {
		t.Error("matching-reqID poll should set agentActive")
	}
	if cmd != nil {
		t.Error("poll msg must NOT re-arm the tick — re-arm lives in the tick handler")
	}
}

func TestAgentSessionPollMsgDropsStaleReqID(t *testing.T) {
	m := New()
	updated, _ := m.Update(agentSessionPollMsg{reqID: m.sidebarWorktreesReqID + 7, active: map[string]bool{"/gone": true}})
	if updated.(Model).refs.AgentActive("/gone") {
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
