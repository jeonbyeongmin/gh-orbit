# worktrees

A `w`-key modal listing every entry from `git worktree list --porcelain`,
plus a one-line sticky header at the top of the refs sidebar that always
shows the current worktree's name + branch + dirty marker. The cockpit
shape this surface is built for: an AI agent occupies worktree A and is
mid-task; the reviewer pops into gh-orbit, switches to worktree B for a
quick read, and switches back — all in-process, no second terminal, no
disturbance to the agent's session.

`w` is global — every focused pane accepts it.

## Refs sidebar header

The first row of the refs pane reads:

```
Worktree: <basename(workdir)> · <branch | (detached)> · ●dirty?
```

`refreshWorktreeHeader` rebuilds it on every `refsLoadedMsg`,
`switchWorktreeMsg`, and `currentWorktreeDirtyMsg`. The dirty bit comes
from a dedicated `loadCurrentWorktreeDirtyCmd` (1× `git status` on
`m.workdir`) fired on Init and after every switch, so the header doesn't
require the modal to be open. Empty branch + no detached marker means
either pre-load or a fresh repo with no commits; the header just shows
the worktree name without a misleading state.

## `w` modal — key matrix

| Key       | When               | Action                                                              |
| --------- | ------------------ | ------------------------------------------------------------------- |
| `j` / `k` | list               | move cursor                                                         |
| `enter`   | list               | switch to cursor entry (no-op + status when cursor is already here) |
| `a`       | list               | open add modal — branch-name textinput                              |
| `d`       | list, non-active   | open remove confirm — refuses active worktree with a status message |
| `esc`     | list               | close modal                                                         |
| `enter`   | add input          | validate + run `git worktree add -b <branch> <path>`                |
| `esc`     | add input          | back to list, drop input state                                      |
| `y`       | remove (clean)     | run `git worktree remove <path>`                                    |
| `y`       | remove (dirty/lck) | cancel — surfaces a status with `[Y] to force` hint                 |
| `Y`       | remove (dirty/lck) | run `git worktree remove --force <path>`                            |
| `esc`     | remove             | back to list, drop target state                                     |

Add input only takes a branch name. The new worktree's path auto-derives
to `<dir(activeWorktreePath)>/<branch>` — a sibling directory of the
current tree. The custom-path knob lives in the separate
`worktree-lock-move-prune` backlog so the v1 modal stays single-input.

## In-process switch

`switchWorktreeMsg{path}` is the seam every "go to a different worktree"
surface dispatches through. `switchWorktree`:

1. Validates the path: must be a directory and contain `.git` (file or
   dir). Failures surface on the status bar; `m.workdir` is not touched.
2. Same-path → no-op + "already on this worktree".
3. Rewrites `m.workdir`, resets `currentRefs` to the unified `--all`
   sentinel (the new tree may not host any of the previous filter's
   refs), and **drops every cursor-persist slot** —
   `pendingRefCursorPersist` / `pendingRefCursorName` /
   `pendingRefCursorAfterDelete`. Cross-tree cursor restoration is more
   confusing than useful; a same-named branch in two trees would land
   the cursor on the wrong row.
4. Arms `pendingHEADHash = pendingHEADSentinel` so the post-reload
   `refsLoadedMsg` snaps the graph onto the new tree's HEAD commit.
5. Returns `tea.Batch(m.reloadCmd(), loadCurrentWorktreeDirtyCmd(...))`
   — and, when `viewModeLocalChanges` is active, a fresh `loadStatusCmd`
   too. `reloadCmd` doesn't touch local-changes state otherwise.

The wrapper layer is reused as-is. Every `internal/git/*` function
already accepted a `dir` argument or a `Dir` field on its options struct;
this feature only stopped passing the empty string for it and started
passing `m.workdir` instead. Adding a second worktree retargets every
load / fetch / pull / status / checkout / branch-write through that one
field.

## Dirty fan-out

Opening the modal fires:

1. `loadWorktreesCmd(dir, reqID)` — single porcelain list.
2. `worktreeDirtyFanoutCmd(reqID, paths)` — N concurrent
   `git status --porcelain` calls, one per worktree path. Each one's
   result lands as a separate `worktreeDirtyResultMsg` so rows light up
   incrementally instead of waiting for the slowest tree.

Stale-drop: the modal's `reqID` increments on every open AND every
close. A `worktreeDirtyResultMsg` whose `reqID` doesn't match the live
generation is dropped — modal-close + re-open before all results arrive
can't bleed dirty state from the previous session.

## Scope: v1 explicitly excludes

The following actions are intentionally out of v1 — each was split off
into its own backlog so the policy decisions get a dedicated interview:

- `git worktree lock` / `unlock` — lock-recommendation policy.
- `git worktree move` — path-input UX + post-move workdir reconciliation.
- `git worktree prune` — auto vs. manual trigger, prunable display.
- last commit subject / time on each row.
- Claude Code agent-session hint per row (requires Claude Code internals
  research before the detection method can be chosen).

If you find yourself reaching for one of those, the answer is "not this
PR" — see the spillover backlogs in the vault.

## Why surgical

The plan deliberately did NOT add wrapper signatures or new typed errors
beyond `ErrWorktreeDirty` / `ErrWorktreeLocked`. The cockpit shape gets
its leverage from a single new field (`Model.workdir`) and a single new
seam (`switchWorktreeMsg`) — every other surface that wants to retarget
the TUI at a different working tree can dispatch the same message
without learning about the worktree feature at all.
