# docs/

Feature-level reference for `gh-orbit` — the review cockpit for
AI-coding-agent work (see top-level [README.md](../README.md)).

Pointers, not prose. One-line hook per file — open the file for
detail.

- [architecture.md](architecture.md) — TUI layout, pane composition,
  key bindings, Bubble Tea rules, commit-row anatomy. The reviewer's
  cockpit surface.
- [git-wrappers.md](git-wrappers.md) — `internal/git` conventions:
  typed wrappers, streaming, error wrap, porcelain parsing. The
  agent-and-human-share-one-`git` boundary.
- [config.md](config.md) — XDG paths for log + prefs,
  `[pull] strategy` resolution chain.
- [checkout.md](checkout.md) — refs `enter` / `p`, graph `enter`,
  dirty-tree confirm flow, fast-forward variants. The "land on the
  agent's branch fast" surface.
- [stash.md](stash.md) — Stashes refs section, graph injection,
  `enter`/`d` write actions, conflict policy.
- [branches.md](branches.md) — `n` / `d` / `m` modals for create /
  delete / rename, key matrix, modal mechanics. The "agent left a
  pile of branches behind" cleanup surface.
- [worktrees.md](worktrees.md) — `w` modal for list / switch / add /
  remove, refs-pane sticky header, in-process switch + dirty fan-out.
  The "agent occupies worktree A, reviewer inspects worktree B" surface.

When a behavior crosses files (e.g. dirty-tree stash chain shows up
in both checkout and stash), the canonical doc is **checkout.md**
for control flow, **stash.md** for the wrapper surface.

## Reading order for an AI agent

If you (the agent) were just dropped into this repo to make a change:

1. Skim [architecture.md](architecture.md) for layout vocabulary
   (refs / graph / tab / patch overlay).
2. Skim [git-wrappers.md](git-wrappers.md) before touching anything
   that runs git.
3. Read the doc that matches the *surface* your task is on (checkout
   / stash / branches / config).
4. Do not silently override an invariant documented here. If a doc
   is wrong, fix the doc as part of the same change.
