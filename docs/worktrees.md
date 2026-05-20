# worktrees

Multi-worktree is a first-class cockpit concept. The shape it's built
for: an AI agent occupies worktree A and is mid-task; the reviewer pops
into gh-orbit, sees every tree in the top dashboard at a glance,
switches to worktree B for a quick read via the `w` modal, and switches
back — all in-process, no second terminal, no disturbance to the
agent's session.

Two surfaces share the worktree state, in different roles:

- **Top dashboard** — read-only band rendered above the graph pane on
  every frame. Lists every entry from `git worktree list --porcelain`,
  marks the current entry with `▶`, paints a `●` dirty marker (`?` on
  timeout). The dashboard is always visible; it never grabs the cursor.
- **`w` modal** — centered overlay listing every worktree with a
  cursor. The single entry for the worktree workflow (switch / add /
  remove). Opens via the global `w` keybind.

## Dashboard rendering

Layout (N=4 example):

```
┌──────────────────────────────────────────────────────────┐
│ Worktrees (4)              ◆ Local Changes 3 files…      │
│ ▶ main · develop ●                                       │
│   feat-auth · feat/auth                                  │
│   feat-qa · feat/qa                                      │
│   refactor · feat/refactor ●                             │
│ ────────────────────────────── fetched 14m ago           │
└──────────────────────────────────────────────────────────┘
```

- **Header line** — `Worktrees (N)` left, `◆ Local Changes meta` right.
  The Local Changes meta carries `N files · +X -Y · Zm ago` when the
  working tree is dirty (numstat against HEAD + load wall clock); empty
  working tree drops the meta. On narrow widths the label wins.
- **Worktree row** — `name · branch · dirty`. `name` is the basename of
  the worktree path. The current entry (the one `m.workdir` lives in)
  prefixes with `▶` + bold + select color so the user knows which
  context the rest of the cockpit describes.
- **Separator line** — horizontal rule with `fetched Xm ago` right-
  aligned. The freshness clock for fetch attempts; blank rule before
  the first fetch.

Dirty marker on each row:

- `●` — `git status` returned non-empty (dirty).
- `?` — the per-row 3s budget was exhausted; render a placeholder so
  the dashboard never silently lies about a slow / stuck worktree.
- (none) — clean, OR not yet loaded.

The dashboard is read-only — no cursor, no key handling. `refModel.SetWorktrees(entries, currentPath)` populates the state; per-tree fan-out fires after every `worktreesLoadedMsg` and tags each row's `worktreeDirty` / `worktreeTimedOut` state.

## `w` modal (`viewModeWorktreesModal`)

The cursor surface for worktree actions. Opens via the global `w`
keybind from `viewModeNormal`. Mirrors the branches modal (`b`)
pattern.

| Key       | Action                                                              |
| --------- | ------------------------------------------------------------------- |
| `j` / `k` | move cursor within the list (bounded; no wrap)                      |
| `enter`   | switch to the worktree under the cursor                             |
| `a`       | open the add-worktree input sub-modal                               |
| `d`       | open the remove-worktree confirm sub-modal (refuses current entry)  |
| `esc` / `q` | close modal                                                       |

Modal opens with the cursor parked on the current worktree. Empty
inventory rejects entry with a status line; on entry success the modal
closes and the action sub-modal takes over.

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
surface dispatches through (today: `enter` inside the `w` modal). On
switch:

1. Validate path (directory containing `.git`); fail surfaces on status.
2. Same-path → no-op + "already on this worktree".
3. Rewrite `m.workdir`, reset `currentRefs` to `--all`.
4. Arm `pendingHEADHash = pendingHEADSentinel` so the post-reload refs
   stream snaps the graph onto the new tree's HEAD commit.
5. Bump `sidebarWorktreesReqID` (drops any in-flight fan-out from the
   previous tree — the field name predates PR B2 and now stands for
   "worktree-loader reqID", not a sidebar component).
6. Dispatch `tea.Batch(reloadCmd(), loadWorktreesCmd(...))` and — when
   `viewModeLocalChanges` is active — a fresh `loadStatusCmd` too.

`m.refs.SetWorktrees(...)` is nudged synchronously with the new path so
the `▶` marker on the dashboard flips immediately while the
authoritative list (with its dirty fan-out) is in flight.

### Switch confirmation status line

On every successful switch the status line paints
`→ switched: <prev-basename> → <new-basename>` and the handler arms a
`tea.Tick(3s)` auto-clear. The fix for scenario 3 (switch confirmation
ambiguity) from the design doc — without the toast a fast switch can
look like a no-op because the surrounding TUI (dashboard `▶`, graph,
tab) animates faster than the eye registers.

Anti-stale mechanism: `m.statusTickSeq` is incremented before the tick
is dispatched, and the closure captures the current value. On receipt
the handler clears only when `m.statusTickSeq == msg.seq` AND the
status still starts with `→ switched:`. Two protections:

- **Seq gate** — a follow-up switch bumps the counter, invalidating the
  earlier tick so it can't wipe the fresh "→ switched" line.
- **Prefix gate** — even with a matching seq, a non-switched status
  (e.g. `fetching…`) survives. Belt-and-suspenders: any future status
  source that forgets to bump the seq still won't get wiped here.

Validation failures (`worktree switch: ...`) and the same-path no-op
(`already on this worktree`) don't fire the tick — those status lines
are user-facing rejections that should persist until the next action.

## Dirty fan-out

`worktreeDirtyFanoutCmd(reqID, paths)` dispatches N concurrent
`git status --porcelain` calls, one per worktree path. Each one's
result lands as a separate `worktreeDirtyResultMsg` so rows light up
incrementally instead of waiting for the slowest tree.

**E3 budget**: each per-row goroutine has a 3-second
`context.WithTimeout`. On timeout the msg carries `timedOut=true`; the
dashboard renders `?` for that row instead of trusting the
(effectively unknown) dirty bit. Without the budget a stuck NFS / slow
network mount could leave the dashboard visually stalled.

Stale-drop: a `worktreeDirtyResultMsg` whose `reqID` doesn't match the
live `sidebarWorktreesReqID` is dropped — a worktree switch in the
middle of a fan-out can't bleed dirty state from the previous tree.

## Scope: v1 explicitly excludes

The following actions are intentionally out of v1 — each was split off
into its own backlog so the policy decisions get a dedicated interview:

- `git worktree lock` / `unlock` — lock-recommendation policy.
- `git worktree move` — path-input UX + post-move workdir reconciliation.
- `git worktree prune` — auto vs. manual trigger, prunable display.
- last commit subject / time on each row (per-wt HEAD fan-out).
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
