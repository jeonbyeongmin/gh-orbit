package tui

import (
	"context"
	"errors"
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
	dir := t.TempDir()
	updated, _ := m.Update(switchWorktreeMsg{path: dir})
	got := updated.(Model)
	if got.workdir == dir {
		t.Errorf("workdir should not have switched to non-git dir")
	}
}

func TestSwitchWorktreeUpdatesWorkdirAndDispatchesReload(t *testing.T) {
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
	if cmd == nil {
		t.Fatal("expected reload+sidebar cmd batch, got nil")
	}
	// PR C: switch confirmation polish.
	if !strings.HasPrefix(got.status, "→ switched:") {
		t.Errorf("status = %q, want '→ switched:' prefix", got.status)
	}
	if !strings.Contains(got.status, "main → feat-a") {
		t.Errorf("status = %q, want 'main → feat-a'", got.status)
	}
	if got.statusTickSeq != 1 {
		t.Errorf("statusTickSeq = %d, want 1 (bumped once)", got.statusTickSeq)
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

// TestSidebarWorktreesLoadedAppliesToRefs verifies that worktreesLoadedMsg
// flows into the refs sidebar (not a separate modal state) so the rows
// render from refModel.worktrees, and the dirty fan-out is dispatched.
func TestSidebarWorktreesLoadedAppliesToRefs(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.workdir = "/tmp/feat"

	entries := []git.Worktree{
		{Path: "/tmp/main", Branch: "main", IsMain: true},
		{Path: "/tmp/feat", Branch: "feat"},
	}
	updated, cmd := m.Update(worktreesLoadedMsg{reqID: m.sidebarWorktreesReqID, entries: entries})
	got := updated.(Model)

	if !reflectDeepEqualPaths(got.refs.Worktrees(), entries) {
		t.Errorf("refs.Worktrees = %+v, want %+v", got.refs.Worktrees(), entries)
	}
	if cmd == nil {
		t.Fatal("expected dirty fan-out cmd")
	}
}

// TestSidebarWorktreesLoadedRescalesGraphViewport locks in that an
// async worktreesLoadedMsg, which grows dashboardLines from 0 to N+2,
// also propagates the new graph pane height to m.graph. Without
// applyPaneSizes the bubbles list keeps the pre-dashboard viewport
// height and the cursor falls out of view — the "scroll 깨짐" symptom.
func TestSidebarWorktreesLoadedRescalesGraphViewport(t *testing.T) {
	m := initSized(t)
	m = seedGraphCursor(t, m, "abc1234")
	m.workdir = "/tmp/feat"

	prev := m.paneSizes()
	if m.graph.height != prev.graphH {
		t.Fatalf("precondition: graph.height %d != paneSizes.graphH %d after initSized",
			m.graph.height, prev.graphH)
	}

	entries := []git.Worktree{
		{Path: "/tmp/main", Branch: "main", IsMain: true},
		{Path: "/tmp/feat", Branch: "feat"},
		{Path: "/tmp/qa", Branch: "qa"},
	}
	updated, _ := m.Update(worktreesLoadedMsg{reqID: m.sidebarWorktreesReqID, entries: entries})
	m = updated.(Model)

	now := m.paneSizes()
	if now.graphH >= prev.graphH {
		t.Fatalf("paneSizes should shrink graphH after dashboard appears: prev=%d new=%d",
			prev.graphH, now.graphH)
	}
	if m.graph.height != now.graphH {
		t.Errorf("graph.height not rescaled after worktreesLoadedMsg: graph.height=%d, paneSizes.graphH=%d",
			m.graph.height, now.graphH)
	}
}

func TestSidebarWorktreesLoadedDropsStaleReqID(t *testing.T) {
	m := New()
	preReqID := m.sidebarWorktreesReqID
	updated, cmd := m.Update(worktreesLoadedMsg{reqID: preReqID - 1, entries: []git.Worktree{{Path: "/x"}}})
	got := updated.(Model)
	if len(got.refs.Worktrees()) != 0 {
		t.Errorf("stale msg should not populate sidebar, got %+v", got.refs.Worktrees())
	}
	if cmd != nil {
		t.Errorf("stale msg should not dispatch fan-out, got cmd")
	}
}

func TestSidebarDirtyFanoutAppliesAndDropsStale(t *testing.T) {
	m := New()
	reqID := m.sidebarWorktreesReqID
	m.refs.SetWorktrees([]git.Worktree{{Path: "/tmp/feat", Branch: "feat"}}, "/tmp/feat")

	updated, _ := m.Update(worktreeDirtyResultMsg{reqID: reqID, path: "/tmp/feat", dirty: true})
	got := updated.(Model)
	if !got.refs.WorktreeDirty("/tmp/feat") {
		t.Errorf("expected dirty=true after applied msg")
	}

	updated, _ = got.Update(worktreeDirtyResultMsg{reqID: reqID - 1, path: "/tmp/other", dirty: true})
	got = updated.(Model)
	if got.refs.WorktreeDirty("/tmp/other") {
		t.Errorf("stale fan-out msg should not mutate sidebar dirty map")
	}
}

func TestDirtyFanoutTimeoutMarksWorktreeMap(t *testing.T) {
	// Post-PR-B2 there is no sidebar render to assert against. The
	// timedOut signal is observable on the storage map; the dashboard
	// renders `?` from there (covered in dashboard_test.go).
	m := New()
	reqID := m.sidebarWorktreesReqID
	m.refs.SetWorktrees([]git.Worktree{{Path: "/slow", Branch: "feat"}}, "/somewhere")
	updated, _ := m.Update(worktreeDirtyResultMsg{
		reqID: reqID, path: "/slow", dirty: false, timedOut: true,
	})
	got := updated.(Model)
	if !got.refs.worktreeTimedOut["/slow"] {
		t.Error("timedOut should be recorded on the worktreeTimedOut map")
	}
}

// TestWorktreesModalEnterDispatchesSwitch verifies the post-PR-B2 entry:
// `w` opens the worktrees modal, j moves to a non-current entry, enter
// emits switchWorktreeMsg.
func TestWorktreesModalEnterDispatchesSwitch(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.workdir = "/r/main"
	m.refs.SetWorktrees([]git.Worktree{
		{Path: "/r/main", Branch: "main", IsMain: true},
		{Path: "/r/feat", Branch: "feat"},
	}, "/r/main")

	// `w` → modal opens with cursor on the current worktree (/r/main).
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	m = updated.(Model)
	if m.mode != viewModeWorktreesModal {
		t.Fatalf("w should open viewModeWorktreesModal, mode = %v", m.mode)
	}
	// j → cursor to /r/feat.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = updated.(Model)
	// enter → emits switchWorktreeMsg for /r/feat.
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_ = updated.(Model)
	if cmd == nil {
		t.Fatal("enter should dispatch switchWorktreeMsg")
	}
	msg := cmd()
	sw, ok := msg.(switchWorktreeMsg)
	if !ok {
		t.Fatalf("expected switchWorktreeMsg, got %T", msg)
	}
	if sw.path != "/r/feat" {
		t.Errorf("path = %q, want /r/feat", sw.path)
	}
}

// TestWorktreesModalDOpensRemoveConfirm — d on a non-current cursor entry
// inside the modal arms viewModeWorktreeRemoveConfirm for that path.
func TestWorktreesModalDOpensRemoveConfirm(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.workdir = "/r/main"
	m.refs.SetWorktrees([]git.Worktree{
		{Path: "/r/main", Branch: "main", IsMain: true},
		{Path: "/r/feat", Branch: "feat"},
	}, "/r/main")

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m = updated.(Model)
	if m.mode != viewModeWorktreeRemoveConfirm {
		t.Errorf("d in modal on non-current entry should open remove confirm, mode = %v", m.mode)
	}
	if m.worktreeAction.removeTarget.Path != "/r/feat" {
		t.Errorf("removeTarget = %+v, want /r/feat", m.worktreeAction.removeTarget)
	}
}

// TestWorktreesModalDOnCurrentRejects — d on the current worktree (the
// one m.workdir lives in) is rejected with a status line; modal closes
// (worktreesModalRemove resets state).
func TestWorktreesModalDOnCurrentRejects(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.workdir = "/r/main"
	m.refs.SetWorktrees([]git.Worktree{
		{Path: "/r/main", Branch: "main", IsMain: true},
	}, "/r/main")

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m = updated.(Model)
	if m.mode == viewModeWorktreeRemoveConfirm {
		t.Error("d on current worktree should NOT open remove confirm")
	}
	if !strings.Contains(m.status, "cannot remove current worktree") {
		t.Errorf("expected rejection status, got %q", m.status)
	}
}

// TestWorktreesModalAOpensAddInput — a inside the modal opens the
// add-input sub-modal regardless of cursor position.
func TestWorktreesModalAOpensAddInput(t *testing.T) {
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(Model)
	m.workdir = "/r/main"
	m.refs.SetWorktrees([]git.Worktree{
		{Path: "/r/main", Branch: "main", IsMain: true},
	}, "/r/main")

	// `w` → modal, then `a` → add-input. The modal closes (worktreesModal
	// state resets in worktreesModalAdd) and viewModeWorktreeAddInput
	// takes over.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = updated.(Model)
	if m.mode != viewModeWorktreeAddInput {
		t.Errorf("a in modal should open add input, mode = %v", m.mode)
	}
}

func TestWorktreeRemoveConfirmCleanFlow(t *testing.T) {
	prev := worktreeRemoveExec
	defer func() { worktreeRemoveExec = prev }()
	var seenForce bool
	var seenPath string
	worktreeRemoveExec = func(_ context.Context, _, path string, force bool) error {
		seenPath = path
		seenForce = force
		return nil
	}

	m := New()
	m.workdir = "/r/main"
	m.refs.SetWorktrees([]git.Worktree{
		{Path: "/r/main", Branch: "main", IsMain: true},
		{Path: "/r/feat", Branch: "feat"},
	}, "/r/main")
	// Arm the remove confirm directly so the test doesn't depend on the
	// sidebar cursor traversal nuances (covered above).
	m = m.beginWorktreeRemove(git.Worktree{Path: "/r/feat", Branch: "feat"})
	if m.mode != viewModeWorktreeRemoveConfirm {
		t.Fatalf("setup: expected confirm mode, got %v", m.mode)
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = updated.(Model)
	if !m.worktreeAction.actionInFlight {
		t.Errorf("actionInFlight should be set during remove")
	}
	if cmd == nil {
		t.Fatal("expected remove cmd")
	}
	resultMsg := cmd()
	if _, ok := resultMsg.(worktreeRemoveSucceededMsg); !ok {
		t.Fatalf("expected worktreeRemoveSucceededMsg, got %T", resultMsg)
	}
	if seenForce {
		t.Errorf("clean remove should not pass force=true")
	}
	if seenPath != "/r/feat" {
		t.Errorf("remove path: got %q want /r/feat", seenPath)
	}
}

func TestWorktreeRemoveConfirmDirtyRequiresUppercaseY(t *testing.T) {
	prev := worktreeRemoveExec
	defer func() { worktreeRemoveExec = prev }()
	var seenForce bool
	worktreeRemoveExec = func(_ context.Context, _, _ string, force bool) error {
		seenForce = force
		return nil
	}

	m := New()
	m.workdir = "/r/main"
	m.refs.SetWorktrees([]git.Worktree{
		{Path: "/r/main", Branch: "main", IsMain: true},
		{Path: "/r/feat", Branch: "feat"},
	}, "/r/main")
	m.refs.SetWorktreeDirty("/r/feat", true, false)
	m = m.beginWorktreeRemove(git.Worktree{Path: "/r/feat", Branch: "feat"})

	// lowercase y on dirty → cancels and surfaces "use [Y] to force".
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = updated.(Model)
	if cmd != nil {
		t.Errorf("lowercase y on dirty should not dispatch remove cmd")
	}
	if m.mode != viewModeNormal {
		t.Errorf("expected to return to normal, got %v", m.mode)
	}
	if !strings.Contains(m.status, "dirty") {
		t.Errorf("status should mention dirty, got %q", m.status)
	}

	// Re-open and use Y → force.
	m = m.beginWorktreeRemove(git.Worktree{Path: "/r/feat", Branch: "feat"})
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Y'}})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("Y on dirty should dispatch force remove")
	}
	_ = cmd()
	if !seenForce {
		t.Errorf("Y on dirty should pass force=true")
	}
}

func TestWorktreeAddInputValidationAndSuccess(t *testing.T) {
	prev := worktreeAddExec
	defer func() { worktreeAddExec = prev }()
	var seenPath, seenBranch string
	var seenCreate bool
	worktreeAddExec = func(_ context.Context, _, path, branch string, createBranch bool) error {
		seenPath = path
		seenBranch = branch
		seenCreate = createBranch
		return nil
	}

	m := New()
	m.workdir = "/tmp/main"
	m, _ = m.beginWorktreeAdd()
	if m.mode != viewModeWorktreeAddInput {
		t.Fatalf("expected add input mode, got %v", m.mode)
	}

	// Empty branch → inline error.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.worktreeAction.addInlineErr == "" {
		t.Errorf("expected inline error on empty submit")
	}

	m.worktreeAction.addInput.SetValue("feat-x")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("expected worktreeAddCmd")
	}
	if !m.worktreeAction.actionInFlight {
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
		t.Errorf("createBranch should be true")
	}
	wantPath := deriveAddPath("/tmp/main", "feat-x")
	if seenPath != wantPath {
		t.Errorf("path: got %q want %q", seenPath, wantPath)
	}
}

// TestQ5RemoteFilterHidesMirroredOrigin asserts the Q5 remote filter:
// a remote-tracking ref whose stripped name matches a local branch is
// hidden from the sidebar.
func TestQ5RemoteFilterHidesMirroredOrigin(t *testing.T) {
	out := partitionByKind([]git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal},
		{ShortName: "feat/a", Kind: git.RefKindLocal},
		{ShortName: "origin/main", Kind: git.RefKindRemote},
		{ShortName: "origin/feat/a", Kind: git.RefKindRemote},
		{ShortName: "origin/zombie", Kind: git.RefKindRemote},
	})
	remotes := out[1]
	if len(remotes) != 1 {
		t.Fatalf("expected 1 surviving remote, got %d: %+v", len(remotes), remotes)
	}
	if remotes[0].ShortName != "origin/zombie" {
		t.Errorf("expected origin/zombie to survive (no local match), got %+v", remotes[0])
	}
}

// reflectDeepEqualPaths is a lightweight slice equality check by path for
// the small sidebar tests above. Avoids importing reflect.
func reflectDeepEqualPaths(a, b []git.Worktree) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Path != b[i].Path {
			return false
		}
	}
	return true
}

// Avoid an "errors unused" lint when the file's only path through the
// errors package is the (now-private) E3 timeout assertion.
var _ = errors.New

func TestStatusClearTickClearsMatchingSeq(t *testing.T) {
	m := New()
	m.statusTickSeq = 7
	m.status = "→ switched: main → feat-a"
	m.statusStyle = statusOkS

	updated, cmd := m.Update(statusClearTickMsg{seq: 7})
	m = updated.(Model)
	if m.status != "" {
		t.Errorf("matching seq tick should clear status, got %q", m.status)
	}
	if cmd != nil {
		t.Errorf("clear-tick handler should not dispatch a cmd, got %v", cmd)
	}
}

func TestStatusClearTickIgnoresStaleSeq(t *testing.T) {
	m := New()
	m.statusTickSeq = 8 // a follow-up switch bumped past the tick's seq
	m.status = "→ switched: feat-a → feat-b"

	updated, _ := m.Update(statusClearTickMsg{seq: 7})
	m = updated.(Model)
	if m.status == "" {
		t.Error("stale seq tick should NOT clear status")
	}
}

func TestStatusClearTickRespectsStatusReplacement(t *testing.T) {
	// Same seq, but the status was overwritten by another action (e.g.
	// fetch). The prefix gate prevents wiping the new line.
	m := New()
	m.statusTickSeq = 7
	m.status = "fetching…"
	m.statusStyle = statusBusyS

	updated, _ := m.Update(statusClearTickMsg{seq: 7})
	m = updated.(Model)
	if m.status != "fetching…" {
		t.Errorf("non-'switched' status should survive the tick, got %q", m.status)
	}
}

// TestReloadCmdRefreshesWorktrees pins the contract that the global `r`
// reload also refreshes the worktree inventory. Without this, `r` would
// leave the dashboard stale while reloading graph + refs, contradicting
// the user's "r = reload everything" mental model.
func TestReloadCmdRefreshesWorktrees(t *testing.T) {
	m := New()
	prev := m.sidebarWorktreesReqID
	cmd := m.reloadCmd()
	if m.sidebarWorktreesReqID <= prev {
		t.Errorf("sidebarWorktreesReqID did not bump after reloadCmd: got %d (was %d)", m.sidebarWorktreesReqID, prev)
	}
	if cmd == nil {
		t.Errorf("reloadCmd returned nil")
	}
}

// TestSucceededMsgRefreshesWorktrees guards the 6 dispatch sites that
// must bump sidebarWorktreesReqID after a HEAD-changing or working-tree-
// changing success message. "reqID bumped + non-nil cmd" is the same
// definition the stale-drop fan-out uses, so checking it here is
// equivalent to asserting loadWorktreesCmd was dispatched without
// peeling apart the opaque tea.Cmd batch.
func TestSucceededMsgRefreshesWorktrees(t *testing.T) {
	cases := []struct {
		name string
		msg  tea.Msg
	}{
		{"CheckoutSucceeded", checkoutSucceededMsg{ref: "main", detached: false}},
		{"PullSucceeded", pullSucceededMsg{}},
		{"FFSucceeded", ffSucceededMsg{branch: "main", advance: 1}},
		{"CheckoutThenFFSucceeded", checkoutThenFFSucceededMsg{branch: "main", advance: 1}},
		{"LocalChangesAdd", localChangesAddSucceededMsg{path: "f"}},
		{"LocalChangesRestore", localChangesRestoreSucceededMsg{path: "f"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := New()
			prev := m.sidebarWorktreesReqID
			updated, cmd := m.Update(tc.msg)
			got := updated.(Model)
			if got.sidebarWorktreesReqID <= prev {
				t.Errorf("sidebarWorktreesReqID did not bump after %T: got %d (was %d)", tc.msg, got.sidebarWorktreesReqID, prev)
			}
			if cmd == nil {
				t.Errorf("expected non-nil cmd after %T", tc.msg)
			}
		})
	}
}
