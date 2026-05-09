// Async cmds and msg shapes for the refs-pane write actions (n / d / m).
// Mirrors checkout.go's seam pattern: every git wrapper is reachable through
// a package-level var so tests can stub the subprocess calls; every cmd
// emits a typed msg the root model branches on.
package tui

import (
	"context"
	"errors"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// Package-level seams over git.* — same rationale as checkout.go's set.
// Tests inject in-memory stubs by reassigning these in t.Cleanup'd helpers.
var (
	branchCreateExec       = git.BranchCreate
	branchDeleteExec       = git.BranchDelete
	branchRenameExec       = git.BranchRename
	remoteBranchDeleteExec = git.RemoteBranchDelete
	checkRefFormatExec     = git.CheckRefFormat
)

// deleteScope enumerates the five outcomes of the delete confirm modal's
// 4-axis key matrix (y / Y / f / F) plus the remote-only fallback used when
// the cursor is on a remote-tracking ref with no matching local. The model
// resolves a key press into one scope before dispatching branchDeleteCmd.
type deleteScope int

const (
	scopeLocalSafe deleteScope = iota
	scopeLocalForce
	scopeBothSafe
	scopeBothForce
	scopeRemoteOnly
)

// includesLocal reports whether the scope wants the local branch deleted.
func (s deleteScope) includesLocal() bool {
	return s == scopeLocalSafe || s == scopeLocalForce ||
		s == scopeBothSafe || s == scopeBothForce
}

// includesRemote reports whether the scope wants the remote ref deleted.
func (s deleteScope) includesRemote() bool {
	return s == scopeBothSafe || s == scopeBothForce || s == scopeRemoteOnly
}

// localForce reports whether the local-side delete should pass `-D`.
func (s deleteScope) localForce() bool {
	return s == scopeLocalForce || s == scopeBothForce
}

// deleteTarget bundles the names branchDeleteCmd needs. Either side can be
// empty when the scope only touches the other side; the cmd derives action
// flow from the scope, not from emptiness checks here.
type deleteTarget struct {
	localName    string // empty when scope is remote-only.
	remote       string // e.g. "origin"; empty when scope is local-only.
	remoteBranch string // e.g. "feat/foo" (no remote prefix); empty likewise.
}

// branchCreateSucceededMsg fires when `git branch <name> [<base>]` returned
// without error. baseLabel mirrors what the modal showed to the user
// ("HEAD" / short hash / ref shortname) so the status bar surfaces the same
// wording the user picked rather than the resolved hash.
type branchCreateSucceededMsg struct {
	name      string
	baseLabel string
}

// branchCreateFailedMsg fires for any failure path; the model formats a
// status-bar message from err (which already wraps stderr via wrapGitErr).
type branchCreateFailedMsg struct {
	err  error
	name string
}

// branchDeleteSucceededMsg fires when every requested side succeeded. The
// model uses scope + the two booleans to render "deleted local …" /
// "deleted remote …" / "deleted local + remote …" without re-deriving from
// scope alone (covers the remote-only path uniformly).
type branchDeleteSucceededMsg struct {
	target        deleteTarget
	scope         deleteScope
	localDeleted  bool
	remoteDeleted bool
}

// branchDeletePartialMsg fires when local succeeded but the subsequent remote
// push --delete failed. We deliberately don't roll back: the user asked for
// both; surface the partial outcome and let them retry the remote half on
// their own. Local-only and remote-only scopes never produce this msg —
// they emit Succeeded or Failed.
type branchDeletePartialMsg struct {
	target        deleteTarget
	scope         deleteScope
	localDeleted  bool
	remoteDeleted bool
	err           error
}

// branchDeleteFailedMsg fires when the very first step failed. For local-
// inclusive scopes, this means the local branch is still present; for
// remote-only, the remote ref is still present.
type branchDeleteFailedMsg struct {
	err    error
	target deleteTarget
	scope  deleteScope
}

// branchDeleteNotMergedMsg fires when the safe local delete (`-d`) was
// rejected because the branch isn't fully merged. The scope is preserved so
// the status bar wording can hint "press [f] or [F] to force" and the user
// can re-press d to enter the modal again — we don't auto-retry; destructive
// operations always require an explicit confirm.
type branchDeleteNotMergedMsg struct {
	target deleteTarget
	scope  deleteScope
}

// branchRenameSucceededMsg fires when `git branch -m <old> <new>` returned
// without error. headWasOld carries the model's pre-rename observation —
// the model arms a HEAD-jump on the post-reload stream so the graph cursor
// follows the renamed branch when HEAD pointed at the source.
type branchRenameSucceededMsg struct {
	oldName    string
	newName    string
	headWasOld bool
}

// branchRenameFailedMsg fires for any failure path; err already wraps git's
// stderr (or the ErrBranchAlreadyExists / ErrBranchNotFound sentinels).
type branchRenameFailedMsg struct {
	err     error
	oldName string
	newName string
}

// refNameValidatedMsg fires when checkRefFormatCmd has finished checking the
// modal's typed name. err == nil routes the model into the next phase
// (branchCreateCmd or branchRenameCmd, depending on refNameInputMode); a
// non-nil err keeps the modal open and stamps inlineErr.
type refNameValidatedMsg struct {
	name string
	err  error
}

// branchCreateCmd runs `git branch <name> [<base>]` asynchronously. base
// passes through unchanged — the wrapper omits the arg when base is empty,
// which means HEAD. checkoutTimeout is reused (60s) because branch create
// is local-only.
func branchCreateCmd(dir, name, base, baseLabel string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), checkoutTimeout)
		defer cancel()
		if err := branchCreateExec(ctx, dir, name, base); err != nil {
			return branchCreateFailedMsg{err: err, name: name}
		}
		return branchCreateSucceededMsg{name: name, baseLabel: baseLabel}
	}
}

// branchDeleteCmd runs the local-then-remote sequence dictated by scope.
// Q3 decision: no rollback on partial failure — the user explicitly asked
// for both sides, so surfacing "did A, failed B" is more honest than
// quietly restoring A. The msg routing reflects this:
//
//   - scope-not-includes-local: skip step 1.
//   - local step fails: branchDeleteFailedMsg / branchDeleteNotMergedMsg
//     (the latter wraps the safe-delete unmerged rejection so the model
//     can hint the force keys without showing a generic stderr line).
//   - scope-not-includes-remote: emit Succeeded with the local result.
//   - remote step fails after local-OK: branchDeletePartialMsg.
//   - remote step fails when local was skipped (remote-only): plain
//     branchDeleteFailedMsg.
//   - all succeeded: branchDeleteSucceededMsg.
//
// Local uses checkoutTimeout (60s, local-only). Remote uses pullTimeout
// (5min) because push --delete crosses the network.
func branchDeleteCmd(dir string, target deleteTarget, scope deleteScope) tea.Cmd {
	return func() tea.Msg {
		var localDeleted bool
		if scope.includesLocal() {
			ctx, cancel := context.WithTimeout(context.Background(), checkoutTimeout)
			err := branchDeleteExec(ctx, dir, target.localName, scope.localForce())
			cancel()
			if err != nil {
				if errors.Is(err, git.ErrBranchNotFullyMerged) && !scope.localForce() {
					return branchDeleteNotMergedMsg{target: target, scope: scope}
				}
				return branchDeleteFailedMsg{err: err, target: target, scope: scope}
			}
			localDeleted = true
		}

		if !scope.includesRemote() {
			return branchDeleteSucceededMsg{
				target:       target,
				scope:        scope,
				localDeleted: localDeleted,
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), pullTimeout)
		err := remoteBranchDeleteExec(ctx, dir, target.remote, target.remoteBranch)
		cancel()
		if err != nil {
			if localDeleted {
				return branchDeletePartialMsg{
					target:        target,
					scope:         scope,
					localDeleted:  true,
					remoteDeleted: false,
					err:           err,
				}
			}
			return branchDeleteFailedMsg{err: err, target: target, scope: scope}
		}
		return branchDeleteSucceededMsg{
			target:        target,
			scope:         scope,
			localDeleted:  localDeleted,
			remoteDeleted: true,
		}
	}
}

// branchRenameCmd runs `git branch -m <old> <new>` asynchronously. The
// model passes headWasOld so the success msg can carry the pre-rename HEAD
// observation back — re-querying HEAD after the rename would race with the
// refs reload.
func branchRenameCmd(dir, oldName, newName string, headWasOld bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), checkoutTimeout)
		defer cancel()
		if err := branchRenameExec(ctx, dir, oldName, newName); err != nil {
			return branchRenameFailedMsg{err: err, oldName: oldName, newName: newName}
		}
		return branchRenameSucceededMsg{
			oldName:    oldName,
			newName:    newName,
			headWasOld: headWasOld,
		}
	}
}

// checkRefFormatCmd runs `git check-ref-format --branch <name>` so the modal
// can report invalid input inline before we spend a fork on the actual
// create / rename. Async (rather than inline in Update) because the fork
// itself is ~5ms and we don't want to freeze textinput rendering on slow
// machines or spinning disks.
func checkRefFormatCmd(dir, name string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), checkoutTimeout)
		defer cancel()
		err := checkRefFormatExec(ctx, dir, strings.TrimSpace(name))
		return refNameValidatedMsg{name: name, err: err}
	}
}
