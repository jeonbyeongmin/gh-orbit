# worktrees

Multi-worktree is a first-class cockpit concept. The shape it's built
for: work is in flight on worktree A; the user pops into gh-orbit,
presses `w` to open the worktrees modal, picks worktree B, hits
`enter` to switch — all in-process, no second terminal, no disturbance
to whatever is running on the other tree.

The `w` modal is the single worktree surface — the same centered
overlay pattern as the branches modal (`b`). It lists every entry from
`git worktree list --porcelain`, marks the current entry with `▶`, and
paints a `●` dirty marker (`?` on timeout). The graph keeps the whole
screen; worktrees appear only while the modal is open.

## Modal rendering

Layout (N=4 example):

```
        ┌──────────────────────────────────────────────────┐
        │ [Worktrees]                                      │
        │ ▶ main · develop ● · sync watcher fix · 2m       │
        │   feat-auth · feat/auth · add login form · 1h    │
        │   feat-qa · feat/qa · run tests green · 3d       │
        │   refactor · feat/refactor ● · wip               │
        │ fetched 14m ago                                  │
        │ [j/k] navigate · [enter] switch · [a] add · …    │
        └──────────────────────────────────────────────────┘
```

- **Header line** — `[Worktrees]`, plus a `↓time` tag while the
  last-commit sort is on.
- **Worktree row** — `name · branch · ● · subject · time`. `name` is
  the basename of the worktree path, capped at **24** cells (a long
  branch-shaped name would otherwise swallow the row) and padded to the
  widest name in the current set so the columns after it line up across
  rows. The alignment yields to information density on a terminal too
  narrow to spare the padding. The
  current entry (the one `m.workdir` lives in) prefixes with `▶` + bold +
  select color so the user knows which context the rest of the cockpit
  describes. `subject` +
  `time` are the worktree HEAD's last-commit summary (see **Last-commit
  column**) — graph only ever shows the *current* tree's commits, so the
  row carries the others' last activity without a switch.
- **Freshness line** — `fetched Xm ago` under the rows; absent before
  the first fetch attempt.
- **Hint line** — the modal key reference, mirroring the branches
  modal's hint.

Dirty marker on each row:

- `●` — `git status` returned non-empty (dirty).
- `?` — the per-row 3s budget was exhausted; render a placeholder so
  the modal never silently lies about a slow / stuck worktree.
- (none) — clean, OR not yet loaded.

### Last-commit column

`subject` (truncated) + `time` (relative, e.g. `2m`, `3d` — the same
`relativeShortAt` vocabulary as the footer, no ` ago` suffix) show the
worktree HEAD's last commit. Both come from the dirty fan-out (one
`git log -1` per tree, folded into the same goroutine as the dirty
probe — see **Dirty fan-out**).

Width-adaptive degradation: the row width tracks the terminal inside a
40–76 cell band, and rows past the 16-row window scroll behind
`↑/↓ N more` markers (shared renderScrollWindow math with the branch
modals). Display order is `▶ name · branch · ● · subject · time`; columns are
allocated in **keep-priority** order — each takes space only if it (plus
its separator) still fits, but a column that doesn't fit is skipped while
smaller lower-priority columns still claim the leftover, so a too-long
`subject` never leaves the row half-empty. Keep-priority, highest first:

1. `name` — always survives (capped + padded as above).
2. `branch`
3. `subject` — hidden whenever fewer than **12** columns remain for it (a
   1–2 char fragment is useless); when shown it takes its own width up to a
   **30**-column cap.
4. `●` dirty marker
5. `time`

So `subject` and `branch` outlive the small `●` / `time` columns under
width pressure (the inversion the redesign fixed: a long name used to push
branch + subject out first). A worktree with no commits yet (unborn HEAD /
bare) or a still-loading / timed-out row renders the `subject` + `time`
slots **blank** — never `?`. The `?` placeholder is reserved for the
dirty marker; a `?` in the time slot would read as a literal value.

`refModel.SetWorktrees(entries, currentPath)` populates the state;
per-tree fan-out fires after every `worktreesLoadedMsg` and tags each
row's `worktreeDirty` / `worktreeTimedOut` / `worktreeLastCommit`
state.

## Worktrees modal (`viewModeWorktreesModal`)

The cursor surface for worktree actions. Opened by the global `w`
keybind from `viewModeNormal`; `w` again or `esc` closes it.

| Key       | Action                                                              |
| --------- | ------------------------------------------------------------------- |
| `j` / `k` | move cursor within the list (bounded; no wrap)                      |
| `enter`   | switch to the worktree under the cursor (closes the modal)          |
| `a`       | open the add-worktree input sub-modal                               |
| `d`       | open the remove-worktree confirm sub-modal (refuses main + current entry) |
| `s`       | toggle last-commit sort (main pinned, rest newest-first)            |
| `esc` / `w` | close (cursor + sort reset)                                       |

Visual cues:

- The cursor row gets a background tint (`colorCursorRowBg`, xterm 237)
  layered behind whatever foreground styling the row already has. On
  the `▶` current row, the bold + accent fg survives the bg overlay so
  both signals (current + cursor) read independently.
- The backdrop dims (composeOverlay), same as every centered modal.

### Last-commit sort (`s`)

While focused, `s` toggles the row order between git-natural (main
first — the default) and last-commit time **descending**. The main
worktree stays pinned at the top as an anchor; the rest sort
newest-commit-first, and rows whose last-commit time hasn't loaded yet
(or timed out — zero-value `when`) sink to the bottom. The cursor rides
the same worktree across the reorder, so the highlight doesn't jump.

The sort is a **snapshot** of the cache at keypress: rows don't re-jump
as the dirty fan-out trickles in. A later `r` reload (or re-toggle)
picks up fresh times. It's **session-local** — closing the modal
(`esc` / `w`) zeroes `worktreesModalState`, so the next open starts in
natural order again (no config persistence). While the sort is on, a
`↓time` tag rides next to the `[Worktrees]` header label.

Opening lands the cursor on the current worktree row if found, else
on row 0. Empty inventory rejects entry with a status line and stays
in `viewModeNormal`. While the modal is open it owns every key (the
standard modal contract) — global shortcuts resume on close.

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
surface dispatches through (today: `enter` in the worktrees modal). On
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
the `▶` marker in the modal flips immediately while the
authoritative list (with its dirty fan-out) is in flight.

### Switch confirmation status line

On every successful switch the status line paints
`→ switched: <prev-basename> → <new-basename>` and the handler arms a
`tea.Tick(3s)` auto-clear. The fix for scenario 3 (switch confirmation
ambiguity) from the design doc — without the toast a fast switch can
look like a no-op because the surrounding TUI (modal `▶`, graph,
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
`timedOut=true`; the modal renders `?` for that row's dirty marker
instead of trusting the (effectively unknown) dirty bit, and leaves the
last-commit columns blank. Without the budget a stuck NFS / slow
network mount could leave the modal visually stalled.

Stale-drop: a `worktreeDirtyResultMsg` whose `reqID` doesn't match the
live `sidebarWorktreesReqID` is dropped — a worktree switch in the
middle of a fan-out can't bleed dirty state from the previous tree.

**No self-induced events**: the fan-out runs `git --no-optional-locks
status` (see `internal/git/status.go`). A plain `git status` opportunistically
refreshes its stat cache by rewriting `.git/index`; since the external-change
watcher below watches `.git/index`, that write would fire a fresh
`worktreeWatchedChangeMsg`, re-running the fan-out — a status → index write →
event → reload → status loop that flickers the inventory after any working-tree
mutation (merge, checkout). `--no-optional-locks` makes the probe read-only so
it never feeds the watcher.

## Refresh triggers

`loadWorktreesCmd` is dispatched from these sites — each one bumps
`sidebarWorktreesReqID` first so any in-flight fan-out from a previous
load drops on arrival:

- App startup (initial batch).
- `worktree add` / `worktree remove` success.
- worktrees-modal switch (via `reloadCmd`).
- `r` global reload key (via `reloadCmd`).
- `fetch` / `pull` / `checkout` / `ff-only` / `checkoutThenFF` /
  `branchDelete` success — all route through `reloadCmd`, which bumps
  the worktree reqID and dispatches the inventory + fan-out together.
- Local Changes `stage` / `unstage` success (explicit, in addition to
  their own status reload — needed for the modal `●` to flip).
- **External git op** (another shell, another worktree)
  — each known worktree's `.git/HEAD` and `.git/index` are watched via
  fsnotify; a 200ms trailing debounce coalesces burst writes (commit,
  rebase) into one refresh. The watcher emits
  `worktreeWatchedChangeMsg{path}` into Update; the handler bumps the
  reqID and dispatches `loadWorktreesCmd`. When the event path matches
  `m.workdir` (i.e., the *current* worktree changed externally), the
  handler also fires `reloadCmd` so graph + refs follow the new HEAD,
  not just the modal row.

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

If you find yourself reaching for one of those, the answer is "not this
PR" — see the spillover backlogs in the vault.

## Why surgical

The plan deliberately did NOT add wrapper signatures or new typed errors
beyond `ErrWorktreeDirty` / `ErrWorktreeLocked`. The cockpit shape gets
its leverage from a single field (`Model.workdir`) and a single seam
(`switchWorktreeMsg`) — every other surface that wants to retarget the
TUI at a different working tree can dispatch the same message.
