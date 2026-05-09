package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// initSized returns a Model with the window size pre-set so paneSizes
// produces non-zero dimensions (some handlers early-return on width==0).
func initSized(t *testing.T) Model {
	t.Helper()
	m := New()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	return updated.(Model)
}

// pressRune dispatches a single-rune key message.
func pressRune(t *testing.T, m Model, r rune) (Model, tea.Cmd) {
	t.Helper()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	return updated.(Model), cmd
}

// TestDKeyOnGraphFocusOpensPatchOverlay locks in the existing behavior so
// the new refs-focus override doesn't leak into other panes.
func TestDKeyOnGraphFocusOpensPatchOverlay(t *testing.T) {
	m := initSized(t)
	m = seedRefs(t, m, []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, IsHead: true},
	})
	m = seedGraphCursor(t, m, "abc1234")
	m.focused = paneGraph

	m, _ = pressRune(t, m, 'd')
	if m.mode != viewModeDiffWindow {
		t.Errorf("graph-focus d should open patch overlay, mode = %v", m.mode)
	}
}

// TestDKeyOnRefsFocusOpensDeleteModal is the gating regression: refs focus
// reinterprets `d` as the delete intent.
func TestDKeyOnRefsFocusOpensDeleteModal(t *testing.T) {
	m := initSized(t)
	m = seedRefs(t, m, []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, IsHead: true},
		{ShortName: "feat/foo", Kind: git.RefKindLocal},
	})
	m.focused = paneRefs
	// Move the refs cursor to feat/foo so 'd' has a non-HEAD target.
	m.refs.cursor = 1

	m, _ = pressRune(t, m, 'd')
	if m.mode != viewModeRefDeleteConfirm {
		t.Errorf("refs-focus d should enter viewModeRefDeleteConfirm, got %v", m.mode)
	}
	if m.pendingRefDelete.localName != "feat/foo" {
		t.Errorf("pendingRefDelete.localName = %q, want feat/foo", m.pendingRefDelete.localName)
	}
}

func TestDKeyOnRefsFocusHEADBranchRejected(t *testing.T) {
	m := initSized(t)
	m = seedRefs(t, m, []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, IsHead: true},
	})
	m.focused = paneRefs
	m.refs.cursor = 0 // HEAD branch
	m, _ = pressRune(t, m, 'd')
	if m.mode == viewModeRefDeleteConfirm {
		t.Error("delete modal should not open for HEAD branch")
	}
	if !strings.Contains(m.status, "current branch") {
		t.Errorf("status = %q, want it to mention 'current branch'", m.status)
	}
}

func TestDKeyOnRefsFocusTagRejected(t *testing.T) {
	m := initSized(t)
	m = seedRefs(t, m, []git.Ref{
		{ShortName: "v1.0", Kind: git.RefKindTag},
	})
	m.focused = paneRefs
	m.refs.cursor = 0
	m, _ = pressRune(t, m, 'd')
	if m.mode == viewModeRefDeleteConfirm {
		t.Error("delete modal should not open for a tag")
	}
	if !strings.Contains(m.status, "branches only") {
		t.Errorf("status = %q, want it to mention 'branches only'", m.status)
	}
}

func TestDeleteModalYDispatchesLocalSafe(t *testing.T) {
	m := initSized(t)
	m = seedRefs(t, m, []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, IsHead: true},
		{ShortName: "feat/foo", Kind: git.RefKindLocal},
	})
	m.focused = paneRefs
	m.refs.cursor = 1
	m, _ = pressRune(t, m, 'd')
	if m.mode != viewModeRefDeleteConfirm {
		t.Fatal("delete modal should be open")
	}
	var seenForce bool
	withRefsActionStubs(t, refsActionStubs{
		branchDelete: func(_ context.Context, _, _ string, force bool) error {
			seenForce = force
			return nil
		},
	})
	m, cmd := pressRune(t, m, 'y')
	if cmd == nil {
		t.Fatal("y should dispatch branchDeleteCmd")
	}
	if m.mode != viewModeNormal {
		t.Errorf("dispatch should leave normal mode, got %v", m.mode)
	}
	if !m.refActionInFlight {
		t.Error("refActionInFlight should be true while delete is running")
	}
	// Drain the cmd so the stub fires.
	cmd()
	if seenForce {
		t.Errorf("y should pass force=false to branchDeleteExec")
	}
}

func TestDeleteModalForceKeySwallowedWhenNoRemote(t *testing.T) {
	m := initSized(t)
	m = seedRefs(t, m, []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, IsHead: true},
		{ShortName: "feat/foo", Kind: git.RefKindLocal},
	})
	m.focused = paneRefs
	m.refs.cursor = 1
	m, _ = pressRune(t, m, 'd')
	// Capital F requires hasRemote; this branch has no upstream, so swallow.
	m, cmd := pressRune(t, m, 'F')
	if cmd != nil {
		t.Errorf("F on local-only target should be swallowed, got cmd=%v", cmd())
	}
	if m.mode != viewModeRefDeleteConfirm {
		t.Errorf("modal should stay open after swallowed key, mode=%v", m.mode)
	}
}

func TestDeleteModalCapitalYIncludesRemote(t *testing.T) {
	m := initSized(t)
	m = seedRefs(t, m, []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, IsHead: true},
		{ShortName: "feat/foo", Kind: git.RefKindLocal, Upstream: "origin/feat/foo"},
		{ShortName: "origin/feat/foo", Kind: git.RefKindRemote},
	})
	m.focused = paneRefs
	m.refs.cursor = 1
	m, _ = pressRune(t, m, 'd')
	if !m.pendingRefDelete.hasRemote {
		t.Fatal("pendingRefDelete.hasRemote should be true (upstream resolves)")
	}

	var localCalls, remoteCalls int
	withRefsActionStubs(t, refsActionStubs{
		branchDelete: func(context.Context, string, string, bool) error {
			localCalls++
			return nil
		},
		remoteBranchDelete: func(context.Context, string, string, string) error {
			remoteCalls++
			return nil
		},
	})
	m, cmd := pressRune(t, m, 'Y')
	if cmd == nil {
		t.Fatal("Y on hasRemote target should dispatch")
	}
	cmd() // drain
	if localCalls != 1 || remoteCalls != 1 {
		t.Errorf("local=%d remote=%d, want 1/1", localCalls, remoteCalls)
	}
}

func TestRefNameInputModalEnterDispatchesValidate(t *testing.T) {
	m := initSized(t)
	m = seedRefs(t, m, []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, IsHead: true},
	})
	m.focused = paneRefs

	m, cmd := pressRune(t, m, 'n')
	if cmd != nil {
		// 'n' on the refs pane emits a refCreateRequestedMsg via cmd; drain it.
		updated, _ := m.Update(cmd())
		m = updated.(Model)
	}
	if m.mode != viewModeRefNameInput {
		t.Fatalf("after n, mode = %v, want viewModeRefNameInput", m.mode)
	}

	// Type a name then press Enter. textinput requires KeyRunes inputs.
	for _, r := range "feat/bar" {
		m, _ = pressRune(t, m, r)
	}

	checkCalls := 0
	withRefsActionStubs(t, refsActionStubs{
		checkRefFormat: func(_ context.Context, _, name string) error {
			checkCalls++
			if name != "feat/bar" {
				t.Errorf("forwarded name = %q", name)
			}
			return nil
		},
	})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("enter should dispatch checkRefFormatCmd")
	}
	if !m.refNameInput.validating {
		t.Error("validating should be true after enter")
	}
	cmd()
	if checkCalls != 1 {
		t.Errorf("checkRefFormatExec calls = %d, want 1", checkCalls)
	}
}

func TestRefNameInputEmptyEnterShowsInlineError(t *testing.T) {
	m := initSized(t)
	m = seedRefs(t, m, []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, IsHead: true},
	})
	m.focused = paneRefs
	m, cmd := pressRune(t, m, 'n')
	if cmd != nil {
		updated, _ := m.Update(cmd())
		m = updated.(Model)
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.mode != viewModeRefNameInput {
		t.Errorf("modal should stay open on empty enter, mode = %v", m.mode)
	}
	if m.refNameInput.inlineErr == "" {
		t.Error("inlineErr should be populated for empty-name enter")
	}
}

func TestRefNameInputEscClosesModal(t *testing.T) {
	m := initSized(t)
	m = seedRefs(t, m, []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, IsHead: true},
	})
	m.focused = paneRefs
	m, cmd := pressRune(t, m, 'n')
	if cmd != nil {
		updated, _ := m.Update(cmd())
		m = updated.(Model)
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.mode != viewModeNormal {
		t.Errorf("esc should close modal, mode = %v", m.mode)
	}
	if !strings.Contains(m.status, "cancel") {
		t.Errorf("status = %q, want a cancel message", m.status)
	}
}

func TestRefNameValidatedDispatchesCreate(t *testing.T) {
	m := initSized(t)
	m = seedRefs(t, m, []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, IsHead: true},
	})
	m.focused = paneRefs
	m, cmd := pressRune(t, m, 'n')
	if cmd != nil {
		updated, _ := m.Update(cmd())
		m = updated.(Model)
	}
	for _, r := range "feat/bar" {
		m, _ = pressRune(t, m, r)
	}

	createCalls := 0
	withRefsActionStubs(t, refsActionStubs{
		branchCreate: func(_ context.Context, _, name, _ string) error {
			createCalls++
			if name != "feat/bar" {
				t.Errorf("forwarded name = %q", name)
			}
			return nil
		},
	})
	// Simulate the validator returning success for the typed name.
	updated, cmd := m.Update(refNameValidatedMsg{name: "feat/bar", err: nil})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("validation success should dispatch branchCreateCmd")
	}
	cmd()
	if createCalls != 1 {
		t.Errorf("branchCreateExec calls = %d, want 1", createCalls)
	}
}

func TestRefNameValidatedFailureKeepsModalOpen(t *testing.T) {
	m := initSized(t)
	m = seedRefs(t, m, []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, IsHead: true},
	})
	m.focused = paneRefs
	m, cmd := pressRune(t, m, 'n')
	if cmd != nil {
		updated, _ := m.Update(cmd())
		m = updated.(Model)
	}
	updated, _ := m.Update(refNameValidatedMsg{
		name: "bad", err: errors.New("git check-ref-format: invalid ref name: not allowed"),
	})
	m = updated.(Model)
	if m.mode != viewModeRefNameInput {
		t.Errorf("validation failure should keep modal open, mode = %v", m.mode)
	}
	if m.refNameInput.inlineErr == "" {
		t.Error("inlineErr should hold the validation error")
	}
}

func TestBranchCreateSucceededArmsCursorAndReloads(t *testing.T) {
	m := initSized(t)
	m = seedRefs(t, m, []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, IsHead: true},
	})
	m.focused = paneRefs
	m.mode = viewModeRefNameInput
	m.refNameInput = refNameInputState{baseLabel: "HEAD"}
	updated, cmd := m.Update(branchCreateSucceededMsg{name: "feat/bar"})
	m = updated.(Model)
	if m.mode != viewModeNormal {
		t.Errorf("mode after success = %v, want viewModeNormal", m.mode)
	}
	if m.pendingRefCursorName != "feat/bar" {
		t.Errorf("pendingRefCursorName = %q, want feat/bar", m.pendingRefCursorName)
	}
	if cmd == nil {
		t.Error("success handler should dispatch reloadCmd")
	}
	if !strings.Contains(m.status, "created 'feat/bar'") || !strings.Contains(m.status, "from HEAD") {
		t.Errorf("status = %q", m.status)
	}
}

func TestBranchDeletePartialMsgSurfacesPartial(t *testing.T) {
	m := initSized(t)
	m = seedRefs(t, m, []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, IsHead: true},
	})
	updated, _ := m.Update(branchDeletePartialMsg{
		target:        deleteTarget{localName: "feat/foo", remote: "origin", remoteBranch: "feat/foo"},
		scope:         scopeBothSafe,
		localDeleted:  true,
		remoteDeleted: false,
		err:           errors.New("permission denied"),
	})
	m = updated.(Model)
	if m.mode != viewModeNormal {
		t.Errorf("mode after partial = %v, want normal", m.mode)
	}
	if !strings.Contains(m.status, "deleted 'feat/foo'") || !strings.Contains(m.status, "remote push failed") {
		t.Errorf("partial status = %q", m.status)
	}
}

func TestBranchDeleteNotMergedMsgSurfacesForceHint(t *testing.T) {
	m := initSized(t)
	m = seedRefs(t, m, []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, IsHead: true},
	})
	updated, _ := m.Update(branchDeleteNotMergedMsg{
		target: deleteTarget{localName: "stale"},
		scope:  scopeLocalSafe,
	})
	m = updated.(Model)
	if !strings.Contains(m.status, "not fully merged") {
		t.Errorf("not-merged status = %q", m.status)
	}
	if !strings.Contains(m.status, "[f]") || !strings.Contains(m.status, "[F]") {
		t.Errorf("status should hint both force keys, got %q", m.status)
	}
}

func TestResolveDeleteState_LocalWithUpstream(t *testing.T) {
	target := git.Ref{ShortName: "feat/foo", Kind: git.RefKindLocal, Upstream: "origin/feat/foo"}
	locals := []git.Ref{target}
	remotes := []git.Ref{{ShortName: "origin/feat/foo", Kind: git.RefKindRemote}}
	st, ok := resolveDeleteState(target, locals, remotes)
	if !ok {
		t.Fatal("local with matching remote should resolve")
	}
	if !st.hasLocal || !st.hasRemote {
		t.Errorf("hasLocal=%v hasRemote=%v, want both true", st.hasLocal, st.hasRemote)
	}
	if st.remote != "origin" || st.remoteBranch != "feat/foo" {
		t.Errorf("remote split = (%q,%q)", st.remote, st.remoteBranch)
	}
}

func TestResolveDeleteState_RemoteCursorWithMatchingLocal(t *testing.T) {
	locals := []git.Ref{
		{ShortName: "feat/foo", Kind: git.RefKindLocal, Upstream: "origin/feat/foo"},
	}
	target := git.Ref{ShortName: "origin/feat/foo", Kind: git.RefKindRemote}
	remotes := []git.Ref{target}
	st, ok := resolveDeleteState(target, locals, remotes)
	if !ok {
		t.Fatal("remote cursor with upstream-matching local should resolve")
	}
	if !st.hasLocal || st.localName != "feat/foo" {
		t.Errorf("hasLocal=%v localName=%q, want true/'feat/foo'", st.hasLocal, st.localName)
	}
}

func TestResolveDeleteState_RemoteCursorMatchingHEADTreatedAsNoLocal(t *testing.T) {
	locals := []git.Ref{
		{ShortName: "main", Kind: git.RefKindLocal, IsHead: true, Upstream: "origin/main"},
	}
	target := git.Ref{ShortName: "origin/main", Kind: git.RefKindRemote}
	remotes := []git.Ref{target}
	st, ok := resolveDeleteState(target, locals, remotes)
	if !ok {
		t.Fatal("remote cursor without HEAD-safe local should still resolve as remote-only")
	}
	if st.hasLocal {
		t.Error("HEAD-only match should be treated as no local match (auto-cascade safety)")
	}
	if !st.hasRemote {
		t.Error("hasRemote should still be true")
	}
}

func TestResolveDeleteScope(t *testing.T) {
	cases := []struct {
		key                 string
		hasLocal, hasRemote bool
		want                deleteScope
		wantOk              bool
	}{
		{"y", true, true, scopeLocalSafe, true},
		{"y", true, false, scopeLocalSafe, true},
		{"y", false, true, scopeRemoteOnly, true},
		{"Y", true, true, scopeBothSafe, true},
		{"Y", true, false, 0, false},
		{"Y", false, true, 0, false},
		{"f", true, true, scopeLocalForce, true},
		{"f", false, true, 0, false},
		{"F", true, true, scopeBothForce, true},
		{"F", true, false, 0, false},
		{"x", true, true, 0, false},
	}
	for _, c := range cases {
		got, ok := resolveDeleteScope(c.key, c.hasLocal, c.hasRemote)
		if got != c.want || ok != c.wantOk {
			t.Errorf("resolveDeleteScope(%q, %v, %v) = (%d, %v), want (%d, %v)",
				c.key, c.hasLocal, c.hasRemote, got, ok, c.want, c.wantOk)
		}
	}
}
