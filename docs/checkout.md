# checkout

Single entry point: graph `space`. The refs-pane Enter handler was retired
together with the refs LIST in the subtract-sidebar sequence, and `space`
took the slot from `enter` (now PR review) — every checkout scenario (local
branch, remote-ahead-of-local, detached commit, multi-chip ambiguous row)
is covered by the graph `space` decision tree below. Pull is a separate
global action (`p`).

## Dirty-tree confirm flow

- The wrapper does **not** pre-flight `git status`. It runs the checkout, matches git's stderr, and wraps the failure with `ErrCheckoutNeedsCleanTree`. No race window between detection and the actual command.
- On that sentinel the TUI enters `viewModeCheckoutConfirm`. Only `s` / `a` / `esc` / `ctrl+c` work; every other key is swallowed.
- `s` (stash & continue) runs `git stash push --include-untracked`, then replays the interrupted chain (checkout / FF / checkout+FF). The stash is left in place — nothing pops it automatically. The success status appends `stashed on <branch>`, and a later checkout back onto that branch appends a `git stash pop` reminder (in-session memory keyed by branch name, not a `git stash list` query — a repeat reminder after a manual pop is the accepted cost). A stash-step failure kills the whole chain (`stash failed: …`) and leaves the tree untouched.
- `a` / `esc` clear `pendingCheckout` and leave the working tree alone.

This is deliberately narrower than the stash *surface* removed in
subtract-stash (PR #42): no stash refs section, chips, drop modal, or
auto-pop chain. Here the stash is an exit ramp for the reviewer's own
WIP when another branch needs attention now — not a managed object.

The modal is reused for the same-branch FF (`withFF`) and cross-branch FF (`withCheckoutFF`) paths — the hint text reflects which chain the decision applies to. graph `space` is the only entry, so the modal lookups never need to disambiguate refs-vs-graph callsites.

## graph `space`

Single context-aware shortcut. Action depends on the cursor commit's chip state and HEAD's relationship to the cursor:

| cursor state                                                       | HEAD                                            | action                                                                                                              |
| ------------------------------------------------------------------ | ----------------------------------------------- | ------------------------------------------------------------------------------------------------------------------- |
| local branch chip 1 (`B`), HEAD on `B`                             | —                                               | no-op (`already on B`)                                                                                              |
| local branch chip 1 (`B`), HEAD elsewhere                          | —                                               | `checkout B`                                                                                                        |
| local branch chips ≥ 2, HEAD on one of them                        | —                                               | no-op                                                                                                               |
| local branch chips ≥ 2, HEAD elsewhere                             | —                                               | open `viewModeBranchPicker` → user picks → `checkout`                                                               |
| no local chip, remote chip with upstream-tracking local `L` (≠ HEAD) | —                                             | `checkout L` then `git merge --ff-only <cursor>` (cross-branch), then **`git pull`**                                |
| no local chip, remote chip with no upstream-tracking local         | —                                               | `git checkout <stripped name>` — dwim creates the local tracking branch, then **`git pull`**                        |
| no local chip (mid-commit or remote-only chip)                     | attached, tip is ancestor of cursor (≠ cursor)  | `git merge --ff-only <cursor>` (no checkout step), then **`git pull`** iff the row carried a remote chip            |
| no local chip                                                      | detached, **or** not an ancestor of cursor      | `git checkout --detach <cursor>`                                                                                    |

### Notes

Fork's "Checkout & Fast-Forward" surfaces in three ways:

- **Same-branch**: HEAD on local `main`, cursor row has only an `origin/main` chip → chipless Case 1 FF on `main`. No checkout.
- **Cross-branch**: HEAD on `feat/foo`, cursor row has only an `origin/develop` chip whose upstream-tracking local is `develop` → `checkout develop` then `git merge --ff-only <cursor>`. The local-tracker rule excludes HEAD itself so the same-branch case stays in the FF lane.
- **New-local**: cursor row has only an `origin/develop` chip and no local tracks it → `git checkout develop` and let dwim create the local tracking branch at the remote's tip.

### Pull-after chain (remote-chip rows)

Every `space` that *started from a remote chip* (the three Fork-style rows
above) chains a background `git pull` once the checkout/FF lands —
"`space` on `origin/xx`" means "get me onto that branch synced with the
network", not just synced with the last-fetch snapshot the graph shows.
Mechanics:

- The evaluator tags the dispatch (`graphActionMsg.pullAfter`); the model
  arms `pullAfterAction` only for tagged Checkout / FF / CheckoutAndFF
  dispatches, and clears it on every failure, so an aborted chain can
  never pull later by surprise. The clean-tree detour keeps it armed
  while the confirm modal decides: `s` (stash & continue) carries it
  through the retry — the remote-chip `space` still ends synced with the
  network — and `a` / `esc` clear it.
- On success the status shows `<outcome> · pulling…` and reload + pull run
  in one batch; the pull respects the same strategy resolution as `p`
  (`[pull] strategy` → git config → `--ff-only`) and the `pullInFlight`
  gate (an already-running pull wins; the flag is consumed, not deferred).
- Plain FF on a chipless row and local-chip checkouts do **not** pull —
  those are local-only motions.

Other invariants:

- Multiple locals tracking the same upstream: cross-branch picks the alphabetically first. Picker UX is reserved for ambiguous local-chip rows; there is no explicit-choice escape hatch on the refs pane.
- `viewModeBranchPicker`: `j` / `k` move cursor, `enter` confirms, `esc` cancels. Every other key swallowed.
- Decision computed asynchronously via `evaluateGraphActionCmd` — model never blocks `Update` on git. `actionInFlight` swallows a second `space` while the evaluator is running. A cursor move between `space` dispatch and reply causes the reply to be dropped — re-press `space` on the new row.
- Status surfaces are one-line: `fast-forward: main +3`, `fast-forward: develop +2 (after checkout)`, `fast-forward failed: <reason>`, `already on main`, `branch select cancelled`.

The old `C` (detach) shortcut is subsumed — graph `space` produces it as the detached-HEAD outcome.

## Rebase (`R`)

`R` rebases the **current branch onto the cursor commit** — the everyday
"my feature branch is behind develop, replay it" move, driven from the
same graph cursor as `space`.

- **Confirm-first**: `R` arms a centered confirm dialog
  (`rebase <head> onto <label>? [y] rebase · [esc] cancel`) — same
  surface as the branch-delete confirm; the graph stays visible
  (dimmed) underneath. `<label>` is the first local chip on the row,
  else the first remote chip, else the short hash.
- **Rejections up front**: detached HEAD (`checkout a branch first`),
  cursor on HEAD itself (no-op), or any in-flight graph action.
- **Conflicts delegate to the terminal**: a conflict stop reports
  `rebase: CONFLICT — resolve in your terminal`, reloads so the graph
  shows the mid-rebase state, and never auto-aborts — identical contract
  to pull conflicts. Success reloads with a HEAD jump
  (`rebase: done (<head> onto <label>)`); a dirty-tree refusal surfaces
  git's own message as `rebase failed: …`.
- Wrapper: `git.Rebase` (plain `git rebase <onto>`), conflict detection
  via the same merge-like stderr/stdout scan as `git.Pull`
  (`ErrRebaseConflict`).

## Cherry-pick (`c`)

`c` applies the **cursor commit onto the current branch** — the same
confirm-first dialog and conflict contract as `R`
(`cherry-pick <hash> onto <head>? [y]/[esc]`; conflicts report
`resolve in your terminal` and leave the mid-pick state in place).
Rejections mirror rebase: detached HEAD, cursor on HEAD, in-flight
actions. Wrapper: `git.CherryPick` (`ErrCherryPickConflict`).

## Revert (`v`) / reset (`x`) — the scrap path

`v` and `x` are the two ways to throw away an agent's commits without
leaving the cockpit. `v` **reverts the cursor commit** — `git revert
--no-edit <hash>` records a new commit that undoes it, preserving
history (safe on already-pushed commits). Same single-`y` confirm and
conflict contract as `c` (`revert <hash> on <head>? [y]/[esc]`;
`ErrRevertConflict` → resolve in your terminal). Detached HEAD is
rejected; reverting HEAD itself is allowed (a normal undo). Wrapper:
`git.Revert`.

`x` **resets the current branch to the cursor commit**, dropping the
commits after it. Because reset rewrites the branch pointer (not a new
commit) it is gated harder than the confirm-first actions: pressing `x`
first runs an async pre-check (`git rev-list`, off the Update goroutine)
that confirms the cursor is *behind* HEAD — a cursor ahead of HEAD has
nothing to discard — and that the dropped range isn't already on the
branch's upstream. A pushed-history reset would need a force-push
(forbidden here), so it is refused and steered to `v`. Only then does
the confirm arm, offering all three modes: `[s] soft` (keep the dropped
changes staged), `[m] mixed` (keep the working tree, unstage), `[h]
hard` (discard the working tree too — flagged in the error color). The
prompt names how many commits the reset drops. Wrapper: `git.Reset`.

## New branch at cursor (`n`)

`n` opens a name-input modal (same shape as the worktree add input)
and runs `git checkout -b <name> <cursor>` — create at the cursor
commit and switch in one step. Empty names and git failures (name
collision, dirty tree) surface as an inline error and keep the modal
open for correction; success closes the modal, reloads, and HEAD-jumps
the graph cursor onto the new branch tip.

## Push (`P`)

`P` completes the network triad (`F` fetch · `p` pull · `P` push):
plain `git push` for the current branch, with a one-shot
`--set-upstream origin <branch>` retry when the branch has no upstream
yet. Never forces; detached HEAD is rejected up front.
