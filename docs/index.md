# docs/

Pointers, not prose. One-line hook per file — open the file for detail.

- [architecture.md](architecture.md) — TUI layout, pane composition, key bindings, Bubble Tea rules, commit-row anatomy.
- [git-wrappers.md](git-wrappers.md) — `internal/git` conventions: typed wrappers, streaming, error wrap, porcelain parsing.
- [config.md](config.md) — XDG paths for log + prefs, `[pull] strategy` resolution chain.
- [checkout.md](checkout.md) — refs `enter` / `p`, graph `enter`, dirty-tree confirm flow, fast-forward variants.
- [stash.md](stash.md) — Stashes refs section, graph injection, `enter`/`d` write actions, conflict policy.
- [branches.md](branches.md) — `n` / `d` / `m` modals for create / delete / rename, key matrix, modal mechanics.

When a behavior crosses files (e.g. dirty-tree stash chain shows up in both checkout and stash), the canonical doc is **checkout.md** for control flow, **stash.md** for the wrapper surface.
