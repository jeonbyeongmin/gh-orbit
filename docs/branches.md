# branches

Two delete-branch entries: refs-pane `d` (inline, on a local-branch cursor) and the global `b` modal (centered overlay listing every local branch). The refs-pane `d` is scheduled for removal in the follow-up PR — the `b` modal is the new single entry post-subtract. Branch creation and rename are handled by worktree workflows (`w` modal) and PR-driven naming; the cockpit doesn't surface modals for them.

## Delete (`d`, refs focus only)

Graph / tab focus keep the existing patch-overlay binding for `d`. Refs-focus arms an inline confirm rendered into the bottom hint line:

```
delete '<branch>'? [y] delete · [Y] force · [esc] cancel
```

No centered overlay — the cursor stays anchored on the row being acted upon, matching the worktree-remove pattern.

Pre-confirm rejections (status bar, no prompt):

- HEAD branch cursor → `cannot delete current branch`.
- Tag / remote-tracking ref cursor → `delete: local branches only` (remote-ref deletion is out of scope; use GitHub or `git push --delete` directly).
- No selection / Local Changes sticky row → status reflects the rejection.

Key matrix:

- `y` → `git branch -d <name>` (safe). On `ErrBranchNotFullyMerged` the prompt re-arms with a hint: `'<branch>' not fully merged — press [Y] to force`. The user can press `Y` to retry with `-D`, or `esc` to back out.
- `Y` → `git branch -D <name>` immediately (force delete; skips the safe path).
- `esc` → cancel; status shows `delete: cancelled`.

`refActionInFlight` gates `d` while a delete cmd is running so a second key press can't queue a parallel git invocation. Successful delete reloads refs + arms `pendingRefCursorAfterDelete` so the cursor lands on the next ref in the same section (or previous, if the deleted entry was last).

## Modal (`b`, global)

`b` opens `viewModeBranchesModal` — a centered overlay listing every local branch with a cursor + HEAD marker (`←`). Independent of focused pane.

Key matrix:

- `j` / `down` / `k` / `up` → move cursor (bounded; no wrap).
- `d` → arms the inline `viewModeRefDeleteConfirm` for the cursor row. HEAD rejected (`cannot delete current branch`).
- `esc` / `q` → close modal.
- `ctrl+c` → quit.

The `d` from the modal reuses the same `pendingRefDelete` + `branchDeleteCmd` chain as the refs-pane `d`. Confirm `esc` lands in `viewModeNormal` (modal does not auto-reopen) — re-press `b` to come back. Empty local list rejects entry with a status line (`branches: no local branches`).

Modal renders via the standard `composeOverlay + renderModalBox` pattern shared with `viewModeBranchPicker`. Source of truth is `m.refs.LocalRefs()` — the refs-pane `loadRefsCmd` cycle keeps it fresh; the modal owns only its cursor.

## Zombie cleanup (`Z`, refs focus only)

`Z` triggers a bulk-clean pass for local branches the cockpit can safely
suggest for deletion. Detection runs in the background; results land in a
centered confirm modal that lists every candidate.

**Three-condition guard** — every condition must hold or the branch stays:

1. Merged into the default branch (`gh repo view -q .defaultBranchRef.name`,
   falling back to `git symbolic-ref refs/remotes/origin/HEAD`, then `develop`).
2. Upstream is gone — `for-each-ref`'s `upstream:track` reports `[gone]`,
   meaning the remote-side counterpart has been deleted (typical post-squash-merge state).
3. Not checked out in any worktree (main + linked).

Branches that fail any one of those drop out before the confirm modal opens,
so the list is never offered something `git branch -d` is going to reject.

Modal flow:

- `y` / `Y` → bulk delete (`git branch -d` per branch, `force=false` —
  any branch that slipped past the guard between detect and execute is
  rejected by git itself instead of silently force-deleted).
- `esc` → cancel; status shows `zombie cleanup: aborted`.
- Empty result skips the modal entirely; status reports `no zombie branches`
  with the baseline named so the user can see what "merged" was tested.

Post-delete summary lands on the bottom status line with the deleted
branch names and a `git reflog` recovery hint. Partial failures (race
against an un-merged commit landing between detect and execute) surface
both halves: `deleted N, M failed: <name> (<reason>)`.

`zombieInFlight` gates the second `Z` press + the modal's `y`/`Y` so a
parallel sweep can't fork. After delete, the refs pane reloads so the
removed branches disappear from the list.
