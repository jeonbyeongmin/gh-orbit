# checkout

Single entry point: graph `enter`. The refs-pane Enter handler was retired
together with the refs LIST in the subtract-sidebar sequence — every
checkout scenario (local branch, remote-ahead-of-local, detached commit,
multi-chip ambiguous row) is covered by the graph Enter decision tree
below. Pull is a separate global action (`p`).

## Dirty-tree confirm flow

- The wrapper does **not** pre-flight `git status`. It runs the checkout, matches git's stderr, and wraps the failure with `ErrCheckoutNeedsCleanTree`. No race window between detection and the actual command.
- On that sentinel the TUI enters `viewModeCheckoutConfirm`. Only `a` / `esc` / `ctrl+c` work; every other key is swallowed.
- `a` / `esc` clear `pendingCheckout` and leave the working tree alone.

The modal is reused for the same-branch FF (`withFF`) and cross-branch FF (`withCheckoutFF`) paths — the hint text reflects which chain the abort applies to. graph Enter is the only entry, so the modal lookups never need to disambiguate refs-vs-graph callsites.

## graph `enter`

Single context-aware shortcut. Action depends on the cursor commit's chip state and HEAD's relationship to the cursor:

| cursor state                                                       | HEAD                                            | action                                                                                                              |
| ------------------------------------------------------------------ | ----------------------------------------------- | ------------------------------------------------------------------------------------------------------------------- |
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

- Multiple locals tracking the same upstream: cross-branch picks the alphabetically first. Picker UX is reserved for ambiguous local-chip rows; there is no explicit-choice escape hatch on the refs pane.
- `viewModeBranchPicker`: `j` / `k` move cursor, `enter` confirms, `esc` cancels. Every other key swallowed.
- Decision computed asynchronously via `evaluateGraphActionCmd` — model never blocks `Update` on git. `actionInFlight` swallows a second Enter while the evaluator is running. A cursor move between Enter dispatch and reply causes the reply to be dropped — re-press Enter on the new row.
- Status surfaces are one-line: `fast-forward: main +3`, `fast-forward: develop +2 (after checkout)`, `fast-forward failed: <reason>`, `already on main`, `branch select cancelled`.

The old `C` (detach) shortcut is subsumed — graph `enter` produces it as the detached-HEAD outcome.
