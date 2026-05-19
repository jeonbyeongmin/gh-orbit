# worktrees

The refs sidebar's top section is a sticky inventory of every entry from
`git worktree list --porcelain`. The cockpit shape this surface is built
for: an AI agent occupies worktree A and is mid-task; the reviewer pops
into gh-orbit, sees both trees in the sidebar at a glance, switches to
worktree B for a quick read, and switches back — all in-process, no
second terminal, no disturbance to the agent's session.

Inventory invariant: the worktree section is always visible. Even when
the user is deep in the refs list (scrolled past all branches), the
sticky frame keeps the inventory rendered.

## Sidebar section

The pane is laid out top-to-bottom as:

```
Worktrees        ← section header
▶ main · main · ●   ← current worktree, ▶ prefix + bold + select color
  feat · feat · ?   ← non-current worktree (dirty fan-out timed out)
  other · other     ← non-current worktree (clean)

● Local Changes  3 files · +47 -12 · 2m ago   ← sticky working-tree row

Local branches
  ...
Remote branches  ← Q5 filter: hides origin/X when a local X exists
  ...
Tags
  ...
```

The `● Local Changes` row carries an inline meta `N files · +X -Y · Zm ago`
when the working tree is dirty (numstat against HEAD, plus the wall-clock
timestamp of the last successful load). Empty working tree falls back to
the bare label. The row reuses cursor-accent weight (highlight color when
unselected, bold-highlight when selected) so it reads as a cockpit signal;
the meta itself renders dim. Width-bound: meta truncates with `…` before
the label is dropped — the label is the row's primary identity.

The dirty marker on each worktree row:

- `●` — `git status` returned non-empty (dirty).
- `?` — the per-row 3s budget was exhausted; render a placeholder so
  the sidebar never silently lies about a slow / stuck worktree.
- (none) — clean, OR not yet loaded.

`refModel.SetWorktrees(entries, currentPath)` populates the section;
per-tree fan-out fires after every `worktreesLoadedMsg` and tags each
row's `worktreeDirty` / `worktreeTimedOut` state.

## Key matrix on a worktree row

| Key       | Action                                                                   |
| --------- | ------------------------------------------------------------------------ |
| `j` / `k` | move cursor within / across the worktree section and into Local Changes  |
| `g` / `G` | jump to top of inventory / bottom of refs (cursor traversal extends)     |
| `enter`   | switch to the worktree under the cursor                                  |
| `a`       | open the add-worktree input sub-modal                                    |
| `d`       | open the remove-worktree confirm sub-modal (refuses the current entry)   |

Cursor traversal order (top to bottom): worktree rows → `● Local Changes`
sticky → ref rows in the three sections. `onWorktree int` (-1 when not
on a worktree row) carries the cursor index inside the worktree section;
the existing `onLocalChanges bool` plus `cursor int` (n-th ref) handle
the other two regions.

## Add input sub-modal (`viewModeWorktreeAddInput`)

| Key     | Action                                                            |
| ------- | ----------------------------------------------------------------- |
| `enter` | run `git worktree add -b <branch> <path>` (path auto-derived)     |
| `esc`   | close, drop input state                                           |

Branch name is the only input; the new worktree's path auto-derives to
`<dir(activeWorktreePath)>/<branch>` — a sibling directory of the
current tree. The custom-path knob is deferred until a real workflow
demands it.

Invalid branch names surface git's stderr verbatim via
`worktreeAddFailedMsg` — no pre-flight validation hop.

## Remove confirm sub-modal (`viewModeWorktreeRemoveConfirm`)

| Key     | Action                                                            |
| ------- | ----------------------------------------------------------------- |
| `y`     | clean entry: run `git worktree remove <path>`                     |
| `y`     | dirty/locked entry: cancel and surface "[Y] to force" hint        |
| `Y`     | dirty/locked entry: run `git worktree remove --force <path>`      |
| `esc`   | close, drop target state                                          |

Removing the current worktree is rejected before the confirm opens —
the user must switch first. Git would refuse anyway, but the friendly
status surface saves a round trip.

## In-process switch

`switchWorktreeMsg{path}` is the seam every "go to a different worktree"
surface dispatches through (today: `enter` on a sidebar row). On switch:

1. Validate path (directory containing `.git`); fail surfaces on status.
2. Same-path → no-op + "already on this worktree".
3. Rewrite `m.workdir`, reset `currentRefs` to `--all`, drop every
   cursor-persist slot (cross-tree cursor restoration would be
   confusing — same-named branch in two trees lands cursor wrong).
4. Arm `pendingHEADHash = pendingHEADSentinel` so the post-reload refs
   stream snaps the graph onto the new tree's HEAD commit.
5. Bump `sidebarWorktreesReqID` (drops any in-flight fan-out from the
   previous tree).
6. Dispatch `tea.Batch(reloadCmd(), loadWorktreesCmd(...))` and — when
   `viewModeLocalChanges` is active — a fresh `loadStatusCmd` too.

`m.refs.SetWorktrees(...)` is nudged synchronously with the new path so
the `▶` marker flips immediately while the authoritative list (with
its dirty fan-out) is in flight.

## Dirty fan-out

`worktreeDirtyFanoutCmd(reqID, paths)` dispatches N concurrent
`git status --porcelain` calls, one per worktree path. Each one's
result lands as a separate `worktreeDirtyResultMsg` so rows light up
incrementally instead of waiting for the slowest tree.

**E3 budget** (eng-review iron rule): each per-row goroutine has a
3-second `context.WithTimeout`. On timeout the msg carries
`timedOut=true`; the sidebar renders `?` for that row instead of
trusting the (effectively unknown) dirty bit. Without the budget a
stuck NFS / slow network mount could leave the sidebar visually
stalled.

Stale-drop: a `worktreeDirtyResultMsg` whose `reqID` doesn't match the
live `sidebarWorktreesReqID` is dropped — a worktree switch in the
middle of a fan-out can't bleed dirty state from the previous tree.

## Why no full-screen list modal anymore

The original `w` modal was a centered overlay listing every worktree —
useful when the list was the only surface that ever saw it, but
redundant once the sidebar is the list. Removing the modal:

- Reduces mode count (one less `viewMode` to gate keys for).
- Aligns with invariant 2 of the cockpit narrative: multi-worktree
  state is permanent context, not a thing to summon.
- Saves a keystroke per inspection — no `w` to open, no `esc` to
  close. The visible inventory IS the answer.

The two action sub-modals stay because branch-name input and remove
confirmation are genuinely modal interactions (single-purpose,
focus-stealing); rendering them as overlays keeps the cursor
ungrabbed in the sidebar.

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
its leverage from a single field (`Model.workdir`) and a single seam
(`switchWorktreeMsg`) — every other surface that wants to retarget the
TUI at a different working tree can dispatch the same message.
