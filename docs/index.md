# docs/

Feature-level reference for `gh-orbit` — the terminal review
cockpit (see top-level [README.md](../README.md)).

Pointers, not prose. One-line hook per file — open the file for
detail.

- [architecture.md](architecture.md) — TUI layout, pane composition,
  key bindings, Bubble Tea rules, commit-row anatomy. The reviewer's
  cockpit surface.
- [git-wrappers.md](git-wrappers.md) — `internal/git` conventions:
  typed wrappers, streaming, error wrap, porcelain parsing. The
  everything-shells-out-to-one-`git` boundary.
- [config.md](config.md) — XDG paths for log + prefs,
  `[pull] strategy` resolution chain.
- [checkout.md](checkout.md) — refs `enter` / `p`, graph `space`,
  dirty-tree confirm flow, fast-forward variants. The "land on the
  branch you need fast" surface.
- [branches.md](branches.md) — `n` / `d` / `m` modals for create /
  delete / rename, key matrix, modal mechanics. The "pile of
  leftover branches" cleanup surface.
- [worktrees.md](worktrees.md) — the `tab`-cycle Worktree page for
  list / switch / add / remove, in-process switch + dirty fan-out.
  The "work lives in worktree A, you inspect worktree B" surface.
- [pull-requests.md](pull-requests.md) — `enter` opens a PR on the web,
  `m` merges it (confirm dialog), and the Pull Requests tab lists every
  open PR. The "jump to a PR and land it from the terminal" surface.

When a behavior crosses files, the canonical doc is **checkout.md**
for control flow and **git-wrappers.md** for the wrapper surface.

## Reading order

If you were just dropped into this repo to make a change:

1. Skim [architecture.md](architecture.md) for layout vocabulary
   (refs / graph / tab / patch overlay).
2. Skim [git-wrappers.md](git-wrappers.md) before touching anything
   that runs git.
3. Read the doc that matches the *surface* your task is on (checkout
   / branches / config).
4. Do not silently override an invariant documented here. If a doc
   is wrong, fix the doc as part of the same change.
