# branches

One refs-pane key: `d` to delete a local branch via inline confirm. Branch creation and rename are handled by worktree workflows (`/w` modal) and PR-driven naming; the cockpit doesn't surface modals for them.

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
