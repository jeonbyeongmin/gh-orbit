# gh-orbit docs

Feature-level reference for contributors.
Top-level [README.md](../README.md) explains *why* the tool exists,
and everything mechanical lives here.

Reading order for a new contributor:

1. [architecture.md](architecture.md) — what the screen looks like
   and how panes compose.
2. [git-wrappers.md](git-wrappers.md) — how the TUI talks to git.
3. [config.md](config.md) — where logs and prefs go.
4. [checkout.md](checkout.md) — the largest behavioral surface
   (graph `space`, `p`, dirty-tree, fast-forward).
5. [branches.md](branches.md) — `b` / `d` delete-branch modal.
6. [worktrees.md](worktrees.md) — the `tab`-cycle Worktree page for
   list / switch / add / remove.
7. [pull-requests.md](pull-requests.md) — the Pull Requests tab:
   `enter` opens a PR on the web, `m` merges it.

[index.md](index.md) is the same map in one-line form, optimized for
skimming the repo before a change.
