package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// runGit shells out to git with deterministic identity env so the
// integration fixture below doesn't depend on ~/.gitconfig.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s in %s: %v: %s", strings.Join(args, " "), dir, err, out)
	}
}

func TestSwitchWorktreeRejectsMissingPath(t *testing.T) {
	m := New()
	prevWorkdir := m.workdir

	updated, cmd := m.Update(switchWorktreeMsg{path: filepath.Join(t.TempDir(), "missing")})
	got := updated.(Model)

	if got.workdir != prevWorkdir {
		t.Errorf("workdir mutated on validation failure: got %q want %q", got.workdir, prevWorkdir)
	}
	if got.statusStyle.GetForeground() != statusErrS.GetForeground() {
		t.Errorf("expected statusErrS on rejection, got style %v", got.statusStyle)
	}
	if cmd != nil {
		t.Errorf("expected nil cmd on rejection, got %T", cmd)
	}
}

func TestSwitchWorktreeRejectsNonGitDirectory(t *testing.T) {
	m := New()
	// A real directory but no .git child → validateWorktreePath fails.
	dir := t.TempDir()

	updated, _ := m.Update(switchWorktreeMsg{path: dir})
	got := updated.(Model)

	if got.workdir == dir {
		t.Errorf("workdir should not have switched to non-git dir")
	}
	if got.statusStyle.GetForeground() != statusErrS.GetForeground() {
		t.Errorf("expected statusErrS, got %v", got.statusStyle)
	}
}

func TestSwitchWorktreeUpdatesWorkdirAndDispatchesReload(t *testing.T) {
	// Build a real git worktree fixture using the package-level git wrapper
	// so the switch handler's validateWorktreePath gate clears.
	root := t.TempDir()
	main := filepath.Join(root, "main")
	if err := os.Mkdir(main, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	runGit(t, main, "init", "--initial-branch=main")
	runGit(t, main, "config", "user.email", "test@example.com")
	runGit(t, main, "config", "user.name", "Test")
	runGit(t, main, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(main, "f"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	runGit(t, main, "add", "f")
	runGit(t, main, "commit", "-m", "init")

	feat := filepath.Join(root, "feat-a")
	if err := git.WorktreeAdd(context.Background(), main, feat, "feat-a", true); err != nil {
		t.Fatalf("WorktreeAdd: %v", err)
	}

	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.workdir = main

	updated, cmd := m.Update(switchWorktreeMsg{path: feat})
	got := updated.(Model)

	if got.workdir != feat {
		t.Errorf("workdir not updated: got %q want %q", got.workdir, feat)
	}
	if got.pendingHEADHash != pendingHEADSentinel {
		t.Errorf("HEAD jump not armed: got %q", got.pendingHEADHash)
	}
	if got.statusStyle.GetForeground() != statusOkS.GetForeground() {
		t.Errorf("expected statusOkS on success, got %v", got.statusStyle)
	}
	if cmd == nil {
		t.Fatal("expected reload cmd batch, got nil")
	}
}

func TestFormatWorktreeHeader(t *testing.T) {
	tt := []struct {
		name     string
		path     string
		branch   string
		detached bool
		dirty    bool
		want     string
	}{
		{"empty path → blank", "", "main", false, false, ""},
		{"branch clean", "/tmp/main", "main", false, false, "Worktree: main · main"},
		{"branch dirty", "/tmp/feat", "feat", false, true, "Worktree: feat · feat · ●dirty"},
		{"detached", "/tmp/det", "", true, false, "Worktree: det · (detached)"},
		{"no branch no detached (fresh repo)", "/tmp/new", "", false, false, "Worktree: new"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			got := formatWorktreeHeader(tc.path, tc.branch, tc.detached, tc.dirty)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCurrentWorktreeDirtyMsgDropsStaleResult(t *testing.T) {
	m := New()
	m.workdir = "/tmp/A"

	updated, _ := m.Update(currentWorktreeDirtyMsg{dir: "/tmp/B", dirty: true})
	got := updated.(Model)

	if got.currentWorktreeDirty {
		t.Errorf("stale dirty msg should be dropped, got currentWorktreeDirty=true")
	}
}

func TestCurrentWorktreeDirtyMsgAppliesAndRefreshesHeader(t *testing.T) {
	m := New()
	m.workdir = "/tmp/repo"

	updated, _ := m.Update(currentWorktreeDirtyMsg{dir: "/tmp/repo", dirty: true})
	got := updated.(Model)

	if !got.currentWorktreeDirty {
		t.Errorf("dirty msg should apply, got false")
	}
	if !strings.Contains(got.refs.worktreeHeader, "●dirty") {
		t.Errorf("expected header to contain dirty marker, got %q", got.refs.worktreeHeader)
	}
}

func TestWorktreeModalOpenAndClose(t *testing.T) {
	prev := worktreesExec
	defer func() { worktreesExec = prev }()
	worktreesExec = func(context.Context, string) ([]git.Worktree, error) {
		return []git.Worktree{
			{Path: "/tmp/main", Branch: "main", HEAD: "abc", IsMain: true},
			{Path: "/tmp/feat-a", Branch: "feat-a", HEAD: "def"},
		}, nil
	}

	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.workdir = "/tmp/feat-a"

	updated, openCmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	m = updated.(Model)
	if m.mode != viewModeWorktreeList {
		t.Fatalf("expected viewModeWorktreeList, got %v", m.mode)
	}
	if !m.worktreeModal.loading {
		t.Errorf("modal should be loading after open")
	}
	if openCmd == nil {
		t.Fatal("expected loadWorktreesCmd, got nil")
	}

	// Drive the loadWorktreesCmd to completion.
	msg := openCmd()
	updated, _ = m.Update(msg)
	m = updated.(Model)
	if m.worktreeModal.loading {
		t.Errorf("modal still loading after worktreesLoadedMsg")
	}
	if len(m.worktreeModal.entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(m.worktreeModal.entries))
	}
	// Cursor seeds onto the active worktree (feat-a is index 1).
	if m.worktreeModal.cursor != 1 {
		t.Errorf("cursor should land on active worktree row, got %d", m.worktreeModal.cursor)
	}

	// Esc closes + bumps reqID so any late fan-out msgs drop.
	prevReqID := m.worktreeModal.reqID
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.mode != viewModeNormal {
		t.Errorf("esc should return to viewModeNormal, got %v", m.mode)
	}
	if m.worktreeModal.reqID == prevReqID {
		t.Errorf("reqID should bump on close so stale fan-out drops")
	}
}

func TestWorktreeModalDirtyFanoutAppliesAndDropsStale(t *testing.T) {
	prev := worktreesExec
	defer func() { worktreesExec = prev }()
	worktreesExec = func(context.Context, string) ([]git.Worktree, error) {
		return []git.Worktree{{Path: "/tmp/main", Branch: "main", IsMain: true}}, nil
	}

	m := New()
	updated, openCmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	m = updated.(Model)
	m.mode = viewModeWorktreeList

	loadMsg := openCmd()
	updated, _ = m.Update(loadMsg)
	m = updated.(Model)
	reqID := m.worktreeModal.reqID

	// Live fan-out result applies.
	updated, _ = m.Update(worktreeDirtyResultMsg{reqID: reqID, path: "/tmp/main", dirty: true})
	m = updated.(Model)
	if !m.worktreeModal.dirty["/tmp/main"] {
		t.Errorf("expected dirty[/tmp/main]=true")
	}

	// Stale msg (reqID-1) drops.
	updated, _ = m.Update(worktreeDirtyResultMsg{reqID: reqID - 1, path: "/tmp/other", dirty: true})
	m = updated.(Model)
	if _, ok := m.worktreeModal.dirty["/tmp/other"]; ok {
		t.Errorf("stale fan-out msg should not mutate dirty map")
	}
}

func TestRenderWorktreeRow(t *testing.T) {
	tt := []struct {
		name   string
		e      git.Worktree
		active string
		dirty  bool
		want   string
	}{
		{
			name:   "active branch clean",
			e:      git.Worktree{Path: "/r/main", Branch: "main", IsMain: true},
			active: "/r/main",
			dirty:  false,
			want:   "* main · main",
		},
		{
			name:   "non-active feat dirty",
			e:      git.Worktree{Path: "/r/feat", Branch: "feat"},
			active: "/r/main",
			dirty:  true,
			want:   "  feat · feat · ●dirty",
		},
		{
			name:   "locked with reason",
			e:      git.Worktree{Path: "/r/lck", Branch: "lck", Locked: true, LockReason: "ext"},
			active: "/r/main",
			want:   "  lck · lck · locked: ext",
		},
		{
			name:   "detached",
			e:      git.Worktree{Path: "/r/det", Detached: true},
			active: "/r/main",
			want:   "  det · (detached)",
		},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			got := renderWorktreeRow(tc.e, tc.active, tc.dirty, 80)
			// Strip ANSI control bytes from the cursor-style `*` so the
			// table-driven want strings stay plain text.
			plain := stripANSI(got)
			if plain != tc.want {
				t.Errorf("got %q want %q", plain, tc.want)
			}
		})
	}
}

// stripANSI removes ANSI escape sequences (CSI ... letter) so renderRow
// output can be compared against plain strings. Vendored locally to keep
// the worktree tests self-contained — the project doesn't import x/ansi
// here yet.
func stripANSI(s string) string {
	var b strings.Builder
	in := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			in = true
			i++
			continue
		}
		if in {
			// CSI sequences end with a byte in 0x40..0x7e.
			if c >= 0x40 && c <= 0x7e {
				in = false
			}
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func TestWorktreeModalEnterEmitsSwitchMsg(t *testing.T) {
	prev := worktreesExec
	defer func() { worktreesExec = prev }()
	worktreesExec = func(context.Context, string) ([]git.Worktree, error) {
		return []git.Worktree{
			{Path: "/tmp/main", Branch: "main", IsMain: true},
			{Path: "/tmp/feat", Branch: "feat"},
		}, nil
	}
	m := New()
	m.workdir = "/tmp/main"
	updated, openCmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	m = updated.(Model)
	updated, _ = m.Update(openCmd())
	m = updated.(Model)
	m.worktreeModal.cursor = 1 // feat

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.mode != viewModeNormal {
		t.Errorf("expected viewModeNormal after enter, got %v", m.mode)
	}
	if cmd == nil {
		t.Fatal("expected switchWorktreeMsg cmd, got nil")
	}
	// Drive the cmd to confirm it dispatches switchWorktreeMsg{path: /tmp/feat}.
	msg, ok := cmd().(switchWorktreeMsg)
	if !ok {
		t.Fatalf("expected switchWorktreeMsg, got %T", cmd())
	}
	if msg.path != "/tmp/feat" {
		t.Errorf("path: got %q want /tmp/feat", msg.path)
	}
}

func TestWorktreeModalRemoveRefusesActive(t *testing.T) {
	prev := worktreesExec
	defer func() { worktreesExec = prev }()
	worktreesExec = func(context.Context, string) ([]git.Worktree, error) {
		return []git.Worktree{{Path: "/tmp/main", Branch: "main", IsMain: true}}, nil
	}
	m := New()
	m.workdir = "/tmp/main"
	updated, openCmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	m = updated.(Model)
	updated, _ = m.Update(openCmd())
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m = updated.(Model)
	if m.mode != viewModeWorktreeList {
		t.Errorf("d on active should keep modal open, got %v", m.mode)
	}
	if !strings.Contains(m.status, "cannot remove current worktree") {
		t.Errorf("expected refusal status, got %q", m.status)
	}
}

func TestWorktreeModalRemoveCleanFlow(t *testing.T) {
	prevList := worktreesExec
	prevRemove := worktreeRemoveExec
	defer func() {
		worktreesExec = prevList
		worktreeRemoveExec = prevRemove
	}()
	worktreesExec = func(context.Context, string) ([]git.Worktree, error) {
		return []git.Worktree{
			{Path: "/tmp/main", Branch: "main", IsMain: true},
			{Path: "/tmp/feat", Branch: "feat"},
		}, nil
	}
	var seenForce bool
	var seenPath string
	worktreeRemoveExec = func(_ context.Context, _, path string, force bool) error {
		seenPath = path
		seenForce = force
		return nil
	}

	m := New()
	m.workdir = "/tmp/main"
	updated, openCmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	m = updated.(Model)
	updated, _ = m.Update(openCmd())
	m = updated.(Model)
	m.worktreeModal.cursor = 1 // feat

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m = updated.(Model)
	if m.mode != viewModeWorktreeRemoveConfirm {
		t.Fatalf("expected confirm mode, got %v", m.mode)
	}

	// y on a clean unlocked entry executes a non-force remove.
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = updated.(Model)
	if !m.worktreeModal.actionInFlight {
		t.Errorf("actionInFlight should be set during remove")
	}
	if cmd == nil {
		t.Fatal("expected worktreeRemoveCmd, got nil")
	}
	resultMsg := cmd()
	if _, ok := resultMsg.(worktreeRemoveSucceededMsg); !ok {
		t.Fatalf("expected worktreeRemoveSucceededMsg, got %T", resultMsg)
	}
	if seenForce {
		t.Errorf("clean remove should not pass force=true")
	}
	if seenPath != "/tmp/feat" {
		t.Errorf("remove path: got %q want /tmp/feat", seenPath)
	}
}

func TestWorktreeModalRemoveDirtyRequiresUppercaseY(t *testing.T) {
	prevList := worktreesExec
	prevRemove := worktreeRemoveExec
	defer func() {
		worktreesExec = prevList
		worktreeRemoveExec = prevRemove
	}()
	worktreesExec = func(context.Context, string) ([]git.Worktree, error) {
		return []git.Worktree{
			{Path: "/tmp/main", Branch: "main", IsMain: true},
			{Path: "/tmp/feat", Branch: "feat"},
		}, nil
	}
	var seenForce bool
	worktreeRemoveExec = func(_ context.Context, _, _ string, force bool) error {
		seenForce = force
		return nil
	}

	m := New()
	m.workdir = "/tmp/main"
	updated, openCmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	m = updated.(Model)
	updated, _ = m.Update(openCmd())
	m = updated.(Model)
	m.worktreeModal.cursor = 1
	m.worktreeModal.dirty = map[string]bool{"/tmp/feat": true}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m = updated.(Model)

	// lowercase y on dirty entry → cancels (force needed).
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = updated.(Model)
	if cmd != nil {
		t.Errorf("lowercase y on dirty should not dispatch remove cmd")
	}
	if m.mode != viewModeWorktreeList {
		t.Errorf("expected to return to list, got %v", m.mode)
	}
	if !strings.Contains(m.status, "dirty") {
		t.Errorf("status should mention dirty, got %q", m.status)
	}

	// Re-open confirm, this time uppercase Y → force remove.
	m.worktreeModal.removeTarget = git.Worktree{Path: "/tmp/feat", Branch: "feat"}
	m.mode = viewModeWorktreeRemoveConfirm
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Y'}})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("uppercase Y should dispatch force remove")
	}
	_ = cmd()
	if !seenForce {
		t.Errorf("Y on dirty should pass force=true")
	}
}

func TestWorktreeModalAddInputValidationAndSuccess(t *testing.T) {
	prevList := worktreesExec
	prevAdd := worktreeAddExec
	defer func() {
		worktreesExec = prevList
		worktreeAddExec = prevAdd
	}()
	worktreesExec = func(context.Context, string) ([]git.Worktree, error) {
		return []git.Worktree{{Path: "/tmp/main", Branch: "main", IsMain: true}}, nil
	}
	var seenPath, seenBranch string
	var seenCreate bool
	worktreeAddExec = func(_ context.Context, _, path, branch string, createBranch bool) error {
		seenPath, seenBranch, seenCreate = path, branch, createBranch
		return nil
	}

	m := New()
	m.workdir = "/tmp/main"
	updated, openCmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	m = updated.(Model)
	updated, _ = m.Update(openCmd())
	m = updated.(Model)

	// `a` opens add input.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = updated.(Model)
	if m.mode != viewModeWorktreeAddInput {
		t.Fatalf("expected add input mode, got %v", m.mode)
	}

	// Empty branch → inline error.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.worktreeModal.addInlineErr == "" {
		t.Errorf("expected inline error on empty submit")
	}

	// Type "feat-x" and submit.
	m.worktreeModal.addInput.SetValue("feat-x")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("expected worktreeAddCmd")
	}
	if !m.worktreeModal.actionInFlight {
		t.Errorf("actionInFlight should be set during add")
	}
	resultMsg := cmd()
	if _, ok := resultMsg.(worktreeAddSucceededMsg); !ok {
		t.Fatalf("expected worktreeAddSucceededMsg, got %T", resultMsg)
	}
	if seenBranch != "feat-x" {
		t.Errorf("branch: got %q want feat-x", seenBranch)
	}
	if !seenCreate {
		t.Errorf("createBranch should be true for v1")
	}
	wantPath := deriveAddPath("/tmp/main", "feat-x")
	if seenPath != wantPath {
		t.Errorf("path: got %q want %q", seenPath, wantPath)
	}
}

func TestSwitchWorktreeNoopOnSamePath(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "main")
	if err := os.Mkdir(main, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	runGit(t, main, "init", "--initial-branch=main")
	runGit(t, main, "config", "user.email", "test@example.com")
	runGit(t, main, "config", "user.name", "Test")
	runGit(t, main, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(main, "f"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	runGit(t, main, "add", "f")
	runGit(t, main, "commit", "-m", "init")

	m := New()
	m.workdir = main

	updated, cmd := m.Update(switchWorktreeMsg{path: main})
	got := updated.(Model)

	if cmd != nil {
		t.Errorf("same-path switch should not dispatch reload")
	}
	if got.workdir != main {
		t.Errorf("workdir changed on same-path switch")
	}
}
