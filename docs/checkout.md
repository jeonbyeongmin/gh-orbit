# checkout

Three entry points: refs `enter`, refs `p` (checkout + pull), graph `enter`. All share the dirty-tree confirm flow.

## refs `enter`

Translates the cursor ref into a local-name argument before invoking `git checkout`:

- Local branch → pass `ShortName` (`main`, `feat/foo`).
- Tag → pass `ShortName`. Result is a detached HEAD on the tag's commit, which is what the user picked.
- Remote-tracking ref → strip `<remote>/` prefix, pass the inner branch name (`origin/feat` → `feat`). Git's dwim creates a local tracking branch when no same-name local exists. The wrapper does **not** invoke `--track` explicitly.

## Dirty-tree confirm flow

- The wrapper does **not** pre-flight `git status`. It runs the checkout, matches git's stderr, and wraps the failure with `ErrCheckoutNeedsCleanTree`. No race window between detection and the actual command.
- On that sentinel the TUI enters `viewModeCheckoutConfirm`. Only `s` / `a` / `esc` / `ctrl+c` work; every other key is swallowed.
- `s` runs `git stash push -m "gh-orbit: before checkout <ref>"` (no `-u`, untracked files stay in the working tree) and re-issues the checkout. The stash is **not** popped automatically — the status bar surfaces the `stash@{0}` label so the user can resolve it on their own time.
- `a` / `esc` clear `pendingCheckout` and leave the working tree alone.

Variants for the other entry points reuse the same modal with flags:

| flag                 | hint text                                | use                                                                |
| -------------------- | ---------------------------------------- | ------------------------------------------------------------------ |
| (default)            | `[s] stash & checkout`                   | refs `enter`                                                       |
| `withPull=true`      | `[s] stash & checkout & pull`            | refs `p`                                                           |
| `withFF=true`        | `[s] stash & fast-forward`               | graph `enter` same-branch FF (no checkout step)                    |
| `withCheckoutFF=true`| `[s] stash & checkout & fast-forward`    | graph `enter` cross-branch FF                                      |

All chains follow the no-auto-pop policy of `stashThenCheckoutCmd`, with one exception: `withPull` does pop (next section).

## refs `p` — checkout + pull

Cursor-bound mirror of global `P`. Only lower-case `p` is bound on the refs pane — the global upper-case `P` (plain pull on the current branch) is unchanged. Case-pair reads as "global pull vs. cursor-bound pull".

Pull eligibility decided at keypress time from the ref's `Kind` and `Upstream`:

| ref state                                    | action                                                          |
| -------------------------------------------- | --------------------------------------------------------------- |
| Tag                                          | checkout-only · status `pull skipped: tag has no upstream`      |
| Local branch, no upstream                    | checkout-only · status `pull skipped: local branch has no upstream` |
| Local branch with upstream                   | checkout, then pull                                             |
| Remote-tracking ref                          | checkout (dwim creates local tracker), then pull                |

Dirty-tree with `p`: modal hint is `[s] stash & checkout & pull` and `s` chains `stash → checkout → pull → stash pop`. The user's edits land on top of the freshly-pulled HEAD. Failure modes:

- **Pull conflict** → stash pop still runs (chain treats pop as the final step). Status: `pull: CONFLICT — resolve in your terminal; stash preserved at stash@{0}`.
- **Pull generic failure** (transport, auth, non-fast-forward) → stash preserved, pop **not** attempted. Status: `pull failed: <reason>; stash preserved at stash@{0}`. Working tree sits on the new ref's clean state.
- **Stash pop conflict** (after a successful pull) → conflict markers written, stash entry preserved. Status: `pop conflict — resolve markers and run \`git stash drop\` (stash@{0})`. No modal — status bar is the only surface.

## graph `enter`

Single context-aware shortcut. Action depends on the cursor commit's chip state and HEAD's relationship to the cursor:

| cursor state                                                       | HEAD                                            | action                                                                                                              |
| ------------------------------------------------------------------ | ----------------------------------------------- | ------------------------------------------------------------------------------------------------------------------- |
| cursor commit matches a stash entry (stash@{N})                    | —                                               | open `viewModeStashActionPicker` → `[p] pop / [a] apply / [esc]`. Wins over every branch / FF / detach branch below |
| local branch chip 1 (`B`), HEAD on `B`                             | —                                               | no-op (`already on B`)                                                                                              |
| local branch chip 1 (`B`), HEAD elsewhere                          | —                                               | `checkout B`                                                                                                        |
| local branch chips ≥ 2, HEAD on one of them                        | —                                               | no-op                                                                                                               |
| local branch chips ≥ 2, HEAD elsewhere                             | —                                               | open `viewModeBranchPicker` → user picks → `checkout`                                                               |
| no local chip, remote chip with upstream-tracking local `L` (≠ HEAD) | —                                             | `checkout L` then `git merge --ff-only <cursor>` (cross-branch)                                                     |
| no local chip, remote chip with no upstream-tracking local         | —                                               | `git checkout <stripped name>` — dwim creates the local tracking branch                                             |
| no local chip (mid-commit or remote-only chip)                     | attached, tip is ancestor of cursor (≠ cursor)  | `git merge --ff-only <cursor>` (no checkout step)                                                                   |
| no local chip                                                      | detached, **or** not an ancestor of cursor      | `git checkout --detach <cursor>`                                                                                    |

### Notes

Fork's "Checkout & Fast-Forward" surfaces in three ways:

- **Same-branch**: HEAD on local `main`, cursor row has only an `origin/main` chip → chipless Case 1 FF on `main`. No checkout.
- **Cross-branch**: HEAD on `feat/foo`, cursor row has only an `origin/develop` chip whose upstream-tracking local is `develop` → `checkout develop` then `git merge --ff-only <cursor>`. The local-tracker rule excludes HEAD itself so the same-branch case stays in the FF lane.
- **New-local**: cursor row has only an `origin/develop` chip and no local tracks it → `git checkout develop` and let dwim create the local tracking branch at the remote's tip.

Other invariants:

- Multiple locals tracking the same upstream: cross-branch picks the alphabetically first. Picker UX is reserved for ambiguous local-chip rows. For cross-branch, the refs panel `p` is the explicit-choice escape hatch.
- `viewModeBranchPicker`: `j` / `k` move cursor, `enter` confirms, `esc` cancels. Every other key swallowed.
- Decision computed asynchronously via `evaluateGraphActionCmd` — model never blocks `Update` on git. `actionInFlight` swallows a second Enter while the evaluator is running. A cursor move between Enter dispatch and reply causes the reply to be dropped — re-press Enter on the new row.
- Status surfaces are one-line: `fast-forward: main +3`, `fast-forward: develop +2 (after checkout)`, `fast-forward failed: <reason>`, `already on main`, `branch select cancelled`.

The old `C` (detach) shortcut is subsumed — graph `enter` produces it as the detached-HEAD outcome.
