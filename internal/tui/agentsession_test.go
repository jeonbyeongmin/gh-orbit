package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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

	msg, ok := agentSessionPollCmd(projects, []string{"/wt/live", "/wt/idle"}, now)().(agentSessionPollMsg)
	if !ok {
		t.Fatal("poll cmd should produce agentSessionPollMsg")
	}
	if !msg.active["/wt/live"] {
		t.Error("/wt/live (1m old) should be active")
	}
	if msg.active["/wt/idle"] {
		t.Error("/wt/idle (30m old) should be inactive")
	}
}

func TestAgentSessionPollMsgAppliesAndRearms(t *testing.T) {
	m := New()
	updated, cmd := m.Update(agentSessionPollMsg{active: map[string]bool{"/wt": true}})
	if !updated.(Model).refs.AgentActive("/wt") {
		t.Error("poll msg handler should set agentActive")
	}
	if cmd == nil {
		t.Error("poll msg handler must re-arm the tick (non-nil cmd) — otherwise the poll dies after one cycle")
	}
}

func TestAgentSessionTickMsgDispatchesPoll(t *testing.T) {
	m := New()
	_, cmd := m.Update(agentSessionTickMsg{})
	if cmd == nil {
		t.Fatal("tick msg handler should dispatch a poll cmd")
	}
	if _, ok := cmd().(agentSessionPollMsg); !ok {
		t.Error("tick cmd should produce agentSessionPollMsg")
	}
}
