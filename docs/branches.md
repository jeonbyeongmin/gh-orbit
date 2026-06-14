# branches

Single delete-branch entry: the global `b` modal (centered overlay listing every local branch). Branch creation and rename are handled by worktree workflows (the `tab`-cycle Worktree page) and PR-driven naming; the cockpit doesn't surface modals for them.

## Modal (`b`, global)

`b` opens `viewModeBranchesModal` — a centered overlay listing every local branch with a cursor + HEAD marker (`←`). Independent of focused pane.

Key matrix:

- `↑` / `↓` → move cursor (bounded; no wrap).
- `d` → arms the `viewModeRefDeleteConfirm` dialog (centered overlay) for the cursor row. HEAD rejected (`cannot delete current branch`).
- `esc` → close modal.
- `ctrl+c` `ctrl+c` → quit (first press arms, second quits).

The `d` from the modal reuses the same `pendingRefDelete` + `branchDeleteCmd` chain as the refs-pane `d`. Confirm `esc` lands in `viewModeNormal` (modal does not auto-reopen) — re-press `b` to come back. Empty local list rejects entry with a status line (`branches: no local branches`).

Modal renders via the standard `composeOverlay + renderModalBox` pattern shared with `viewModeBranchPicker`. Source of truth is `m.refs.LocalRefs()` — the refs-pane `loadRefsCmd` cycle keeps it fresh; the modal owns only its cursor.
