# stash

`Stashes` is a fourth refs-pane section. Listing order: Local branches → Remote branches → Tags → Stashes.

## Discovery

- Populated by `git.StashList` (`git stash list --format=%gd%x00%H%x00%gs%x00%aI`), **not** `for-each-ref refs/stash` — the latter returns only the top entry.
- `defaultRefPatterns` deliberately excludes `refs/stash` so the two discovery paths don't dedup-collide.

## Graph injection

Each stash entry also appears in the graph:

- `loadCommitsCmd` appends `m.currentStashHashes` to `git log`'s refspec so stash commits walk into the stream as additional tips.
- Stash chips render in a dedicated magenta-leaning slot (`colorChipStash = "165"`).
- The label `stash@{N}` is injected into `Commit.RefNames` at stream time because `%D` doesn't surface `refs/stash` tokens.

## Race handling

`loadRefsCmd` discovers stash entries. `refsLoadedMsg` calls `diffStashRefs` against `m.currentStashHashes` and, on diff, dispatches `reloadCmd`. Since `reloadCmd` re-issues `loadRefsCmd` itself, the follow-up `refsLoadedMsg` sees the same set → no diff → no infinite loop. First-paint cost: one extra reload after stash hashes arrive.

## Write actions

| Key     | Where                         | Action                                                                  |
| ------- | ----------------------------- | ----------------------------------------------------------------------- |
| `enter` | graph (cursor on stash row)   | open `viewModeStashActionPicker` modal `[p] pop · [a] apply · [esc]`    |
| `d`     | refs (cursor on stash entry)  | open `viewModeStashDropConfirm` modal `[y] drop · [esc]`                |

Drop is intentionally exclusive to the refs-pane modal — destructive remove always goes through a "drop?" prompt, never the graph-Enter picker. Push (`git stash push -m <msg>`) is deferred to the changes-view backlog.

## Wrappers (`internal/git/remote.go`)

- `StashList(ctx, dir)` → `[]StashEntry`.
- `StashApply(ctx, dir, label)` — wraps CONFLICT into `ErrStashApplyConflict`.
- `StashDrop(ctx, dir, label)` — no sentinel mapping; stderr surfaces as-is.
- `StashPopAt(ctx, dir, label)` — companion to the no-arg `StashPop`. The dirty-tree checkout chain keeps using `StashPop`; `StashPopAt` exists so the new label-bound caller doesn't perturb that callsite.

## Conflict policy

Mirrors `git stash pop` / `apply`: the entry is preserved in both pop-conflict and apply-conflict outcomes. Status bar: `stash <label>: CONFLICT — resolve markers; stash preserved`. Reload still fires so refs panel + graph reflect any partial state.

## Re-indexing

Stash slot re-indexing on pop/drop (`stash@{1}` → `stash@{0}`) is handled by `reloadCmd`'s full refs/log refresh. Drop success arms `pendingRefCursorAfterDelete{kind: RefKindStash}` so the cursor lands on the next entry in the Stash section (or the previous when the deleted slot was last).
