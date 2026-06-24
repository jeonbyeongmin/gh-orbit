# worktrees

Multi-worktree is a first-class cockpit concept. The shape it's built
for: work is in flight on worktree A; the user presses `tab` to reach the
full-screen Worktree page, picks worktree B, hits `space` to
switch — all in-process, no second terminal, no disturbance to whatever
is running on the other tree.

The Worktree page replaces the graph — the same graph-swapping seam
Local Changes uses (`isWorktreesSurface()` routes `main` in `View()`),
not a centered overlay. It renders one **3-line card** per worktree from
`git worktree list --porcelain`, so every tree's branch, PR/CI state, and
last activity read at once. It is a sibling page in the `tab` / `shift+tab`
cycle (Graph → Worktree → Local Changes); there is no esc/q exit — `tab`
moves on to Local Changes, `shift+tab` back to the graph.

## Card layout

Layout (N=2 example, full terminal width; the card renderer lives in
`worktreeview.go`):

```
[Worktrees · 2]

▌▶ develop                                       ↑1  ●3  1h
▌    ~/project/gh-orbit
▌    Merge pull request #101 from feat/pr-review

   feat/claude-code-agent-hint              #42✓  ↑2↓1  2w
     ~/project/gh-orbit/.claude/worktrees/feat+sort-by-last-commit
     docs(worktrees): re-anchor the keep-priority list
```

There is no in-box key hint row anymore: the page keys live in the
shared `?` panel (the `Worktree` category) and the bottom line carries
`? help` + status, same as the graph and local-changes pages. The
earlier in-box hint duplicated the `?` panel once the panel went
page-aware, so it was retired.

The 2-col gutter carries two independent signals: the cursor bar `▌`
(the card under `↑`/`↓`, running down all three lines) and the current
marker `▶` (the worktree `m.workdir` lives in). Because the branch no
longer shares a row with the path, the directory name, and the subject,
**nothing truncates under width pressure** — the failure mode the old
single-row layout forced.

- **Line 1 — branch + status.** The branch leads (git guarantees one
  branch per attached worktree, so it's the stable identifier; detached
  trees read `(detached)`). The status cluster right-anchors, left to right:
  the open-PR badge `#N` + CI glyph (`✓` pass · `✗` fail · `○` running, from
  the same `gh pr list` rollup the graph chips use — `Model.prs`, keyed
  directly by `wt.Branch`); the upstream delta `↑a↓b` (ahead/behind, omitted
  when the branch has no upstream or is in sync); the `●N` dirty marker
  (`N` = changed-file count); and the relative last-commit time. Only the
  branch truncates when the line is tight; the status never does.
- **Line 2 — path (dim).** The worktree path with `$HOME` collapsed to
  `~`, left-truncated (`…tail`) so the directory basename — the part that
  tells trees apart — survives.
- **Line 3 — subject.** The worktree HEAD's last-commit subject (`—` when
  the tree has no commit yet). The graph only shows the *current* tree's
  commits, so the card carries the others' last activity without a switch.

Dirty marker (`●N` / `?`), upstream delta, and the last-commit subject +
time all come from the per-tree fan-out (see **Dirty fan-out**): `●N`
(`N` changed files) when `git status` is non-empty, `?` when the per-row 3s
budget was exhausted (so the card never silently lies about a slow tree),
absent when clean or not yet loaded. The fan-out's `git status` already
parses the changed files, so the count is free — no extra probe.

Cards stack with a blank separator and window to the available height;
when the list overflows, `↑ N more` / `↓ N more` markers cap the visible
run and the window follows the cursor. `refModel.SetWorktrees` populates
the state; the fan-out fires after every `worktreesLoadedMsg` and tags
each row's `worktreeDirty` / `worktreeTimedOut` / `worktreeLastCommit`.

## Worktrees dashboard (`viewModeWorktreesModal`)

The cursor surface for worktree actions. Reached as a page in the
`tab` / `shift+tab` cycle (`tab` from the graph); `tab` advances to Local
Changes and `shift+tab` returns to the graph — there is no esc/q exit.
(The `Modal` in `viewModeWorktreesModal` is a historical artifact from
when it was a centered overlay — it's a full-screen page now.)

| Key         | Action                                                            |
| ----------- | ----------------------------------------------------------------- |
| `↑` / `↓`   | move cursor within the list (bounded; no wrap)                    |
| `space`     | switch to the worktree under the cursor (returns to the graph)    |
| `enter`     | open the cursor worktree's open PR on the web (no-op + status if none) |
| `a`         | open the add-worktree input sub-modal                             |
| `d`         | open the remove-worktree confirm sub-modal (refuses main + current entry) |
| `s`         | toggle last-commit sort (main pinned, rest newest-first)          |
| `tab` / `⇧tab` | cycle to the next / previous page                              |

Visual cues:

- The cursor card carries a left bar `▌` (accent color) down all three
  of its lines — the only per-card highlight. A background tint across a
  multi-line card would fight the lipgloss wrap-reset on its styled
  segments, so the gutter bar does the job instead.
- The current worktree (the one `m.workdir` lives in) prefixes its branch
  line with `▶`. Cursor (`▌`) and current (`▶`) are independent signals:
  the card you're pointing at need not be the tree you're in.

### Last-commit sort (`s`)

While focused, `s` toggles the row order between git-natural (main
first — the default) and last-commit time **descending**. The main
worktree stays pinned at the top as an anchor; the rest sort
newest-commit-first, and rows whose last-commit time hasn't loaded yet
(or timed out — zero-value `when`) sink to the bottom. The cursor rides
the same worktree across the reorder, so the highlight doesn't jump.

The sort is a **snapshot** of the cache at keypress: rows don't re-jump
as the dirty fan-out trickles in. A later `r` reload (or re-toggle)
picks up fresh times. It's **session-local** — the preference persists
across page switches (the cycle no longer zeroes `worktreesModalState`)
but is not written to config, so a fresh session starts in natural order
again. While the sort is on, a `↓time` tag rides next to the
`[Worktrees]` header label.

Entering lands the cursor on the current worktree row if found, else
on row 0. An empty inventory still lands on the page — it renders a
`(no worktrees loaded yet)` body rather than refusing entry, so the
cycle never gets stuck. While the page owns the screen it owns every key
(the global shortcuts route to its cursor); only the `tab` / `shift+tab`
cycle, `?` (toggle the inline help panel), and the `q q` / `^C` quit pass through.

### Open a card's PR on the web (`enter`)

`enter` opens the cursor worktree's open PR on GitHub in the browser —
the same `gh pr view --web` jump graph `enter` and the Pull Requests page
use (`worktreesModalOpenPRWeb` looks the PR up by `wt.Branch` in
`Model.prs`, then `openPRWeb`). It's fire-and-forget: the page stays put,
a transient `opening #N…` status, then `opened #N in browser`. A card
whose branch has no open PR reports on the status line instead. Reviewing
and landing both happen off the dashboard now — read the diff on the web,
or land it with `m` from the graph / Pull Requests page (see
[pull-requests.md](pull-requests.md)).

## Add input sub-modal (`viewModeWorktreeAddInput`)

| Key     | Action                                                            |
| ------- | ----------------------------------------------------------------- |
| `enter` | run `git worktree add -b <branch> <path>` (path auto-derived)     |
| `esc`   | back to the worktrees modal, drop input state                     |

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
| `esc`   | back to the worktrees modal, drop target state                    |

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
surface dispatches through (today: `space` in the worktrees modal). On
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

Each goroutine runs **three** probes and ships them in one msg: the dirty
`git status` (its parsed entries also give the `●N` changed-file count for
free), the last-commit `git log -1` (subject + committer time), and the
ahead/behind `git rev-list --left-right --count @{u}...HEAD`. Folding them
into one goroutine — rather than three independent fan-outs — keeps a
single `reqID`-tagged msg, cuts the goroutine count, makes the `●` marker,
the subject/time columns, and the `↑↓` counts appear in the same frame (no
jitter between them), and means all three inherit every dirty refresh
trigger below for free (their data goes stale on exactly the same events).
The dirty probe runs first so it owns the budget; `WorktreeLastCommit` and
`WorktreeAheadBehind` errors are dropped (blank columns) since a missing
subject or upstream is non-fatal. Unlike `git status`, neither `git log`
nor `git rev-list` rewrites `.git/index`, so the fold adds no
watcher-flicker risk (see below).

**E3 budget**: each per-row goroutine has a 3-second
`context.WithTimeout` shared by the three probes. On timeout the msg carries
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
