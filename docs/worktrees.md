# worktrees

Multi-worktree is a first-class cockpit concept. The shape it's built
for: an AI agent occupies worktree A and is mid-task; the reviewer pops
into gh-orbit, sees every tree in the top dashboard at a glance,
presses `w` to grab the cursor in the dashboard, picks worktree B,
hits `enter` to switch — all in-process, no second terminal, no
disturbance to the agent's session.

The top dashboard is the single worktree surface. Rendered above the
graph pane on every frame, it lists every entry from
`git worktree list --porcelain`, marks the current entry with `▶`, and
paints a `●` dirty marker (`?` on timeout). The dashboard is always
visible. By default it's read-only; pressing `w` toggles focus on so
the dashboard grabs the cursor and j/k/enter/a/d/esc route to it.

## Dashboard rendering

Layout (N=4 example):

```
┌──────────────────────────────────────────────────────────┐
│ Worktrees (4)              ◆ Local Changes 3 files…      │
│ ▶ main · develop ● · sync watcher fix · 2m               │
│   feat-auth · feat/auth · add login form · 1h            │
│   feat-qa · feat/qa · run tests green · 3d               │
│   refactor · feat/refactor ● · wip                       │
│ ────────────────────────────── fetched 14m ago           │
└──────────────────────────────────────────────────────────┘
```

- **Header line** — `Worktrees (N)` left, `◆ Local Changes meta` right.
  The Local Changes meta carries `N files · +X -Y · Zm ago` when the
  working tree is dirty (numstat against HEAD + load wall clock); empty
  working tree drops the meta. On narrow widths the label wins.
- **Worktree row** — `name · branch · ● · subject · time`. `name` is the
  basename of the worktree path. The current entry (the one `m.workdir`
  lives in) prefixes with `▶` + bold + select color so the user knows
  which context the rest of the cockpit describes. `subject` + `time` are
  the worktree HEAD's last-commit summary (see **Last-commit column**) —
  graph only ever shows the *current* tree's commits, so the row carries
  the others' last activity without a switch.
- **Separator line** — horizontal rule with `fetched Xm ago` right-
  aligned. The freshness clock for fetch attempts; blank rule before
  the first fetch.

Dirty marker on each row:

- `●` — `git status` returned non-empty (dirty).
- `?` — the per-row 3s budget was exhausted; render a placeholder so
  the dashboard never silently lies about a slow / stuck worktree.
- (none) — clean, OR not yet loaded.

### Last-commit column

`subject` (truncated) + `time` (relative, e.g. `2m`, `3d` — the same
`relativeShortAt` vocabulary as the footer, no ` ago` suffix) show the
worktree HEAD's last commit. Both come from the dirty fan-out (one
`git log -1` per tree, folded into the same goroutine as the dirty
probe — see **Dirty fan-out**).

Width-adaptive degradation, since the band shares the right column with
the graph. Display order is `▶ name · branch · ● · subject · time`; when
the row is too narrow the columns drop **whole** (no leftover `…`
fragment) in priority order:

1. `subject` — dropped first, and hidden whenever fewer than **12**
   columns remain for it (a 1–2 char fragment is useless). When shown it
   takes the leftover width, capped at **30**.
2. `branch`
3. `time`
4. `●` dirty marker — dropped last.

`▶ name` always survives. A worktree with no commits yet (unborn HEAD /
bare) or a still-loading / timed-out row renders the `subject` + `time`
slots **blank** — never `?`. The `?` placeholder is reserved for the
dirty marker; a `?` in the time slot would read as a literal value.

The dashboard is read-only by default; pressing `w` toggles focus on
so j/k/enter/a/d/esc route to the dashboard's cursor. `refModel.SetWorktrees(entries, currentPath)` populates the state; per-tree fan-out fires after every `worktreesLoadedMsg` and tags each row's `worktreeDirty` / `worktreeTimedOut` / `worktreeLastCommit` state.

## Dashboard focus mode (`paneDashboard`)

The cursor surface for worktree actions. Toggled by the global `w`
keybind from `viewModeNormal`. Press `w` again or `esc` to exit;
switching / add / remove all keep focus on so a follow-up action can
fire from the same surface (only `esc` / `w` exits).

| Key       | Action                                                              |
| --------- | ------------------------------------------------------------------- |
| `j` / `k` | move cursor within the list (bounded; no wrap)                      |
| `enter`   | switch to the worktree under the cursor                             |
| `a`       | open the add-worktree input sub-modal                               |
| `d`       | open the remove-worktree confirm sub-modal (refuses main + current entry) |
| `esc` / `w` | exit focus (cursor reset, dashboard returns to read-only)         |

Visual cues while focused:

- The dashboard's outer box border switches to the focused accent color
  (same as the graph pane's focused border) so the user can tell at a
  glance which surface owns the cursor.
- The cursor row gets a background tint (`colorCursorRowBg`, xterm 237)
  layered behind whatever foreground styling the row already has. On
  the `▶` current row, the bold + accent fg survives the bg overlay so
  both signals (current + cursor) read independently.
- The bottom hint line replaces the graph hint with `dashboard: j/k
  이동 · enter switch · a add · d remove · esc 종료` while focused.

Focus on lands the cursor on the current worktree row if found, else
on row 0. Empty inventory rejects entry with a status line; focus
stays on `paneGraph`. Other normal-mode global keys (`r`, `F`, `p`,
`?`, `,`, `b`, `Z`, ...) keep working while focused — only j/k/enter/
a/d/esc are claimed by the dashboard.

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

Removing the main worktree is also rejected up-front — git tracks it
specially (the entry that owns the `.git` directory) and refuses
regardless of dirty state. Main + current is a common overlap on a
fresh checkout; the guard order surfaces the stronger constraint
(main, permanent) before the weaker one (current, switch-able) so the
user never burns a switch on a target that's permanently unremovable.

## In-process switch

`switchWorktreeMsg{path}` is the seam every "go to a different worktree"
surface dispatches through (today: `enter` while the dashboard owns the
cursor). On switch:

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
goroutines, one per worktree path. Each one's result lands as a
separate `worktreeDirtyResultMsg` so rows light up incrementally
instead of waiting for the slowest tree.

Each goroutine runs **two** probes and ships them in one msg: the dirty
`git status` and the last-commit `git log -1` (subject + committer
time). Folding the last-commit fetch into the dirty goroutine — rather
than a second independent fan-out — keeps a single `reqID`-tagged msg,
halves the goroutine count, makes the `●` marker and the subject/time
columns appear in the same frame (no jitter between them), and means
last-commit inherits every dirty refresh trigger below for free (its
data goes stale on exactly the same events). The dirty probe runs first
so it owns the budget; `WorktreeLastCommit`'s error is dropped (blank
columns) since a missing subject is non-fatal. Unlike `git status`,
`git log` never rewrites `.git/index`, so the fold adds no
watcher-flicker risk (see below).

**E3 budget**: each per-row goroutine has a 3-second
`context.WithTimeout` shared by both probes. On timeout the msg carries
`timedOut=true`; the dashboard renders `?` for that row's dirty marker
instead of trusting the (effectively unknown) dirty bit, and leaves the
last-commit columns blank. Without the budget a stuck NFS / slow
network mount could leave the dashboard visually stalled.

Stale-drop: a `worktreeDirtyResultMsg` whose `reqID` doesn't match the
live `sidebarWorktreesReqID` is dropped — a worktree switch in the
middle of a fan-out can't bleed dirty state from the previous tree.

**No self-induced events**: the fan-out runs `git --no-optional-locks
status` (see `internal/git/status.go`). A plain `git status` opportunistically
refreshes its stat cache by rewriting `.git/index`; since the external-change
watcher below watches `.git/index`, that write would fire a fresh
`worktreeWatchedChangeMsg`, re-running the fan-out — a status → index write →
event → reload → status loop that flickers the dashboard after any working-tree
mutation (merge, checkout). `--no-optional-locks` makes the probe read-only so
it never feeds the watcher.

## Refresh triggers

`loadWorktreesCmd` is dispatched from these sites — each one bumps
`sidebarWorktreesReqID` first so any in-flight fan-out from a previous
load drops on arrival:

- App startup (initial batch).
- `worktree add` / `worktree remove` success.
- dashboard-focus switch (via `reloadCmd`).
- `r` global reload key (via `reloadCmd`).
- `fetch` / `pull` / `checkout` / `ff-only` / `checkoutThenFF` /
  `branchDelete` success — all route through `reloadCmd`, which bumps
  the worktree reqID and dispatches the inventory + fan-out together.
- Local Changes `stage` / `unstage` success (explicit, in addition to
  their own status reload — needed for the dashboard `●` to flip).
- **External git op** (another shell, another worktree's agent session)
  — each known worktree's `.git/HEAD` and `.git/index` are watched via
  fsnotify; a 200ms trailing debounce coalesces burst writes (commit,
  rebase) into one refresh. The watcher emits
  `worktreeWatchedChangeMsg{path}` into Update; the handler bumps the
  reqID and dispatches `loadWorktreesCmd`. When the event path matches
  `m.workdir` (i.e., the *current* worktree changed externally), the
  handler also fires `reloadCmd` so graph + refs follow the new HEAD,
  not just the dashboard row.

  `onRawEvent` only reacts to **content** ops (Create/Write/Remove/Rename);
  Chmod-only events are dropped. This closes the other half of the
  status-induced flicker loop: even with `--no-optional-locks` (see Dirty
  fan-out), `git status` touches `.git/index`'s metadata and emits a lone
  Chmod on every reload. Without the op filter that Chmod re-fired the
  watcher → `reloadCmd` → graph "loading…" flicker. Real commits / checkouts
  / merges always carry a content op, so they still surface.

If `fsnotify.NewWatcher()` fails at startup (rare — inotify limit,
sandboxed env), the cockpit silent-degrades: status paints `external
watch unavailable — use 'r' to refresh` once, and the rest of the
refresh trigger list above keeps working. Manual `r` is always
sufficient — the watcher is an *optional* convenience layer over it.

## Scope: v1 explicitly excludes

The following actions are intentionally out of v1 — each was split off
into its own backlog so the policy decisions get a dedicated interview:

- `git worktree lock` / `unlock` — lock-recommendation policy.
- `git worktree move` — path-input UX + post-move workdir reconciliation.
- `git worktree prune` — auto vs. manual trigger, prunable display.
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
