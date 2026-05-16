// Async cmds and msg shapes for the refs-pane write actions (n / d / m).
// Mirrors checkout.go's seam pattern: every git wrapper is reachable through
// a package-level var so tests can stub the subprocess calls; every cmd
// emits a typed msg the root model branches on.
package tui

import (
	"context"
	"errors"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// refNameInputMode distinguishes which action the modal will dispatch on
// Enter. The two modes share the same input + validation flow but route to
// different cmds; keeping them on a single state struct lets the bottom
// panel render with one renderer.
type refNameInputMode int

const (
	refNameInputCreate refNameInputMode = iota
	refNameInputRename
)

// refNameInputState backs viewModeRefNameInput. The struct survives across
// the validating async hop: the model arms validating=true on Enter so a
// second Enter while the check-ref-format process is in flight is swallowed
// (mirrors actionInFlight gating on graph Enter). inlineErr is the modal's
// own error line — populated either by validation failure or by a server-
// side rejection that needs the user to fix the typed value before retrying.
type refNameInputState struct {
	mode       refNameInputMode
	target     git.Ref // rename: the source ref. create: zero value.
	base       string  // create only: resolved hash or HEAD ("" → HEAD).
	baseLabel  string  // create only: short label shown to the user.
	input      textinput.Model
	inlineErr  string
	validating bool
}

// refDeleteState backs viewModeRefDeleteConfirm. The flags below decide
// which keys the modal accepts and what label the renderer shows; the model
// stamps them when entering the modal so subsequent key handling stays
// branchless.
type refDeleteState struct {
	target       git.Ref
	localName    string
	remote       string // e.g. "origin"; empty when hasRemote is false.
	remoteBranch string // e.g. "feat/foo"; empty when hasRemote is false.
	hasLocal     bool
	hasRemote    bool
}

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

func (s deleteScope) includesLocal() bool {
	return s == scopeLocalSafe || s == scopeLocalForce ||
		s == scopeBothSafe || s == scopeBothForce
}

func (s deleteScope) includesRemote() bool {
	return s == scopeBothSafe || s == scopeBothForce || s == scopeRemoteOnly
}

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

// deletedRefHandle pairs a deleted ref's display name with the section it
// belonged to, so the post-reload cursor jump can scope its insertion-point
// search to the matching section instead of bleeding across sections.
type deletedRefHandle struct {
	name string
	kind git.RefKind
}

// branchCreateSucceededMsg fires when `git branch <name> [<base>]` returned
// without error. The status-bar handler reads the modal's preserved
// baseLabel to render "from <label>" — round-tripping it through the cmd
// would just be display state crossing the async boundary for nothing.
type branchCreateSucceededMsg struct {
	name string
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
func branchCreateCmd(dir, name, base string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), checkoutTimeout)
		defer cancel()
		if err := branchCreateExec(ctx, dir, name, base); err != nil {
			return branchCreateFailedMsg{err: err, name: name}
		}
		return branchCreateSucceededMsg{name: name}
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

// resolveCreateBase decides what hash the new branch should point at, plus a
// human label for the modal header. Focus order: graph cursor → refs cursor
// ref tip → HEAD. The label preserves the user's mental anchor (short hash
// for graph, ref short name for refs, "HEAD" otherwise) so the modal reads
// as "from where they were looking", not as a resolved hash.
func resolveCreateBase(focused pane, graphCursorHash string, refsCursorRef git.Ref, refsHasCursor bool) (base, label string) {
	switch focused {
	case paneGraph:
		if graphCursorHash != "" {
			return graphCursorHash, shortHash(graphCursorHash)
		}
	case paneRefs:
		if refsHasCursor {
			return refsCursorRef.ObjectName, refsCursorRef.ShortName
		}
	}
	return "", "HEAD"
}

// resolveDeleteState builds the refDeleteState from the cursor ref plus the
// cached refs lists. The two big questions it answers:
//   - For a local cursor: does the upstream resolve to a remote-tracking ref
//     we still see in the cache? If yes, this is the "both sides" matrix.
//   - For a remote cursor: is there a local that tracks this remote ref via
//     its Upstream field? If yes, this is also the "both sides" matrix.
//
// Returns ok=false when the cursor isn't deletable (HEAD branch, tag, or a
// remote ref whose only matching local is HEAD itself — we never auto-delete
// HEAD).
func resolveDeleteState(target git.Ref, locals, remotes []git.Ref) (refDeleteState, bool) {
	if target.Kind == git.RefKindTag || target.Kind == git.RefKindStash {
		// Stash routes to its own drop modal (beginStashDrop in model.go);
		// reaching here means a caller forgot to gate. Fail closed so the
		// branch-delete matrix never sees a stash ref.
		return refDeleteState{}, false
	}
	st := refDeleteState{target: target}

	switch target.Kind {
	case git.RefKindLocal:
		if target.IsHead {
			return refDeleteState{}, false
		}
		st.localName = target.ShortName
		st.hasLocal = true
		if target.Upstream != "" {
			if remote, branch, ok := splitRemoteRef(target.Upstream); ok && hasRemoteRef(remotes, target.Upstream) {
				st.remote = remote
				st.remoteBranch = branch
				st.hasRemote = true
			}
		}
	case git.RefKindRemote:
		remote, branch, ok := splitRemoteRef(target.ShortName)
		if !ok {
			return refDeleteState{}, false
		}
		st.remote = remote
		st.remoteBranch = branch
		st.hasRemote = true
		// Find the local that tracks this remote (upstream-match, not name-match
		// — the user may have renamed the local). HEAD-on-match is treated as
		// "no local match" so the auto-cascade can't delete HEAD.
		for _, l := range locals {
			if l.Upstream == target.ShortName && !l.IsHead {
				st.localName = l.ShortName
				st.hasLocal = true
				break
			}
		}
	default:
		return refDeleteState{}, false
	}

	if !st.hasLocal && !st.hasRemote {
		return refDeleteState{}, false
	}
	return st, true
}

// splitRemoteRef splits "<remote>/<branch>" at the first slash. Names like
// "origin/feat/foo" keep the slashes after the first one in the branch
// portion ("feat/foo"). Returns ok=false when there's no slash (a malformed
// remote-tracking ref name).
func splitRemoteRef(short string) (remote, branch string, ok bool) {
	i := strings.IndexByte(short, '/')
	if i <= 0 || i == len(short)-1 {
		return "", "", false
	}
	return short[:i], short[i+1:], true
}

// hasRemoteRef reports whether the cache contains a remote-tracking ref with
// the given short name. Used to gate hasRemote on local-cursor deletes —
// stale upstream pointers (remote ref already deleted on the server but the
// local hasn't been pruned) should fall back to local-only.
func hasRemoteRef(remotes []git.Ref, short string) bool {
	for _, r := range remotes {
		if r.ShortName == short {
			return true
		}
	}
	return false
}

// formatDeleteSuccess renders the status-bar message for a fully-successful
// delete given the scope and which sides actually fired. Force is named
// explicitly so the user sees the difference between safe and force; remote
// names mirror the modal's "<remote>/<branch>" form for continuity.
func formatDeleteSuccess(target deleteTarget, scope deleteScope, localDeleted, remoteDeleted bool) string {
	switch scope {
	case scopeLocalSafe:
		return "deleted '" + target.localName + "'"
	case scopeLocalForce:
		return "deleted '" + target.localName + "' (forced)"
	case scopeRemoteOnly:
		return "deleted remote '" + target.remote + "/" + target.remoteBranch + "'"
	case scopeBothSafe:
		return "deleted '" + target.localName + "' + remote '" +
			target.remote + "/" + target.remoteBranch + "'"
	case scopeBothForce:
		return "deleted '" + target.localName + "' (forced) + remote '" +
			target.remote + "/" + target.remoteBranch + "'"
	}
	// Fallback uses the booleans (shouldn't fire — every scope is named above).
	switch {
	case localDeleted && remoteDeleted:
		return "deleted '" + target.localName + "' + remote '" +
			target.remote + "/" + target.remoteBranch + "'"
	case localDeleted:
		return "deleted '" + target.localName + "'"
	case remoteDeleted:
		return "deleted remote '" + target.remote + "/" + target.remoteBranch + "'"
	}
	return "delete: nothing happened"
}

// resolveDeleteScope translates a key press in the delete-confirm modal into
// the scope the cmd should run, given the cursor's hasLocal / hasRemote
// state. Returns ok=false when the key isn't valid for this matrix
// (e.g. `Y` on a local-only target).
func resolveDeleteScope(key string, hasLocal, hasRemote bool) (deleteScope, bool) {
	switch key {
	case "y":
		switch {
		case hasLocal:
			return scopeLocalSafe, true
		case hasRemote:
			return scopeRemoteOnly, true
		}
	case "Y":
		if hasLocal && hasRemote {
			return scopeBothSafe, true
		}
	case "f":
		if hasLocal {
			return scopeLocalForce, true
		}
	case "F":
		if hasLocal && hasRemote {
			return scopeBothForce, true
		}
	}
	return 0, false
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
