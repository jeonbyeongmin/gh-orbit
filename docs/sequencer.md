# sequencer

In-progress cherry-pick / rebase / merge / revert resolution on the
**Local Changes** page. The "stay in the terminal" answer to a conflict
stop: the cockpit detects the parked operation and offers continue /
abort instead of forcing a shell round-trip.

## Detection

- `git.DetectSequencer(dir)` resolves the per-worktree git dir with
  `git rev-parse --absolute-git-dir` (so linked worktrees + the gitfile
  case work), then stats the marker files git itself writes — there is
  no porcelain command for this.
- Precedence mirrors git's own status logic: `rebase-merge`/`rebase-apply`
  → `MERGE_HEAD` → `CHERRY_PICK_HEAD` → `REVERT_HEAD`. Rebase wins because
  a stopped rebase pick is a rebase, not the cherry-pick/merge it uses
  under the hood.
- Run as a best-effort part of every `loadStatusCmd` (the 1s poll), so the
  state clears the moment the op ends. A detection failure leaves it
  `SequencerNone` rather than failing the whole status reload.

## Surface

- A one-row banner at the top of the tree pane names the op + the keys
  (`local_changes.go` `sequencerBanner`). While conflicts remain it says
  "resolve conflicts, then …"; once they're staged it drops to just the
  keys. The banner reserves one tree row (`bannerRows` / `visibleTreeRows`
  thread it through `TreeView` + `followCursor`), and persists even on a
  clean tree (all resolved + staged) so `C` stays reachable.
- `C` → `git.SequencerContinue`. Gated on `ConflictCount() == 0`: an
  unstaged resolution still reads as an index conflict, so zero conflicts
  means every one is resolved *and* staged — exactly what `--continue`
  needs. `GIT_EDITOR=true` suppresses the commit-message editor (it would
  collide with the altscreen). A re-conflict on the next step wraps
  `ErrSequencerConflict` and keeps the banner up.
- `ctrl+x` → inline abort confirm (`lcAbortOpen`, a model flag inside
  `viewModeLocalChanges` like `lcDiscardOpen` — the page stays the
  backdrop). `y` fires `git.SequencerAbort`; destructive, so it is
  confirm-gated.
- Both continue and abort take `lcActionInFlight` (shared with stash /
  discard so two index writers can't race) and reload the graph + status
  on completion.

## Entry points

A conflict from the graph-side `R` / `c` / `v` (or `p` pull) now reports
`… CONFLICT — resolve in Local Changes (C / abort)` and reloads. See
[checkout.md](checkout.md) for those flows and
[git-wrappers.md](git-wrappers.md) for the conflict sentinels.
