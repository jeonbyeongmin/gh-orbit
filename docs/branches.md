# branches

Three refs-pane keys for branch lifecycle: `n` create, `d` delete, `m` rename. All open a centered modal that swallows everything outside its key matrix; `esc` cancels.

## Create (`n`)

Base resolved from the focused pane:

- `paneGraph` with a graph cursor → graph cursor commit hash. Header: `[Create branch from '<short hash>']`.
- `paneRefs` with a refs cursor → `cursorRef.ObjectName` (resolved hash, peeled for tags). Header: `[Create branch from '<ref short name>']`.
- otherwise → empty (HEAD). Header: `[Create branch from 'HEAD']`.

Textinput is empty on entry. `enter` runs `git check-ref-format --branch <name>` first; on rejection the modal stays open with an inline error. On a clean format, `git branch <name> [<base>]` fires; on `ErrBranchAlreadyExists` / `ErrInvalidRefName` the modal stays open with the inline error so the user can fix the name.

## Delete (`d`, refs focus only)

Graph / tab focus keep the existing patch-overlay binding for `d`. Refs-focus opens the delete modal with a key matrix derived from the cursor:

| cursor state                                            | hint keys                                                                       |
| ------------------------------------------------------- | ------------------------------------------------------------------------------- |
| local branch + matching remote (upstream resolves)      | `[y] local`, `[Y] local+remote`, `[f] force local`, `[F] force local+remote`    |
| local branch, no upstream (or stale upstream)           | `[y] delete`, `[f] force delete`                                                |
| remote-tracking ref + matching local (upstream-match)   | `[y] local`, `[Y] local+remote`, `[f] force local`, `[F] force local+remote`    |
| remote-tracking ref, no matching local                  | `[y] delete remote`                                                             |

Pre-modal rejections (status bar, no modal):

- HEAD branch cursor → `cannot delete current branch`.
- Tag cursor → `delete: branches only (tags not supported)`.

Lower-case keys (`y` / `f`) target one side; upper-case (`Y` / `F`) include the matching remote. Force is local-only (`-D`); the remote side always invokes `git push <remote> --delete <branch>` without a force option.

Execution order is local → remote, with **no rollback** on partial failure (interview decision):

- local OK + remote OK → `branchDeleteSucceededMsg` → status: `deleted '<name>' + remote '<remote>/<branch>'` (or single-side variants).
- local OK + remote FAIL → `branchDeletePartialMsg` → status: `deleted '<name>'; remote push failed: <reason>` (statusErrS).
- local FAIL (any cause) → remote step skipped. `ErrBranchNotFullyMerged` from a safe delete routes to `branchDeleteNotMergedMsg` → status: `delete: '<name>' not fully merged — press [f] or [F] to force`. Modal closes; pressing `d` again re-opens it so the user can pick the force pair.

## Rename (`m`)

Local branches only. `refs.go` emits `refRenameRejectedMsg` for tags / remote-tracking refs / detached HEAD with `rename: local branch only`.

- Modal pre-fills the textinput with the source name, cursor at end.
- HEAD-on-source rename is allowed. `headWasOld` is observed before the cmd fires so the post-rename refs reload arms `pendingHEADHash = pendingHEADSentinel` and the graph cursor follows the renamed branch.

## Modal mechanics

- `viewModeRefNameInput` (create / rename) and `viewModeRefDeleteConfirm` (delete) gate the screen. The 3-pane layout stays visible above; the modal panel rows are reserved through `paneSizes` so the main area shrinks rather than overlaps.
- `enter` in the name-input modal arms `validating=true` and dispatches `checkRefFormatCmd` asynchronously. A second `enter` while validating is swallowed so a slow `git check-ref-format` can't double-fire.
- After a successful create / rename, `pendingRefCursorName` is stamped; the next `refsLoadedMsg` calls `refModel.SelectByName` so the cursor lands on the new row. Delete sets `pendingRefCursorAfterDelete` and uses `SelectAfterDeleted` (next-in-section, or previous when last).
- `refActionInFlight` gates `n` / `d` / `m` while a write cmd is running so a second key press can't queue a parallel git invocation.
