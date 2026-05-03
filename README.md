# gh-orbit

A `gh` CLI extension that brings the parts of [Fork](https://git-fork.com)
(local git history) and the GitHub web UI (PR review/merge) you actually use
into a single terminal TUI.

## Why?

[`gh dash`](https://github.com/dlvhdr/gh-dash) covers remote PRs but ignores
local git state. [`lazygit`](https://github.com/jesseduffield/lazygit) covers
local git but its commit-graph view is secondary — Fork's strength is the
opposite. `gh-orbit` aims at the niche between the two: a Fork-style
commit-graph-first workflow with PR review sitting next to it.

## Status

**Early WIP.** The MVP scope is the local-git half (Fork replacement);
PR review/merge is a follow-up milestone. Right now the build wires up:

- 3-pane Fork-style layout (refs / commit graph / diff) — placeholder content
- vim-style key bindings:
  - `h` / `l` — move pane focus
  - `j` / `k` — navigate within pane
  - `enter` — select ref (refs pane)
  - `a` — show every ref's commits (refs pane)
  - `F` — `git fetch --all` in the background
  - `r` — reload refs + log
  - `q` / `ctrl+c` — quit
  - `R` is reserved for a future Rebase action.
- a status line next to the help row surfaces fetch progress and errors
- `internal/git.Log()` / `git.Fetch()` shelling out to the user's `git` binary

## Install

Until this is published, install from a local clone:

```bash
git clone https://github.com/jeonbyeongmin/gh-orbit
cd gh-orbit
go build -o gh-orbit ./cmd/orbit
gh extension install .
gh orbit
```

## Develop

```bash
go run ./cmd/orbit                    # run against the cwd repo
go test ./...                         # tests
golangci-lint run                     # lint
tail -f ~/.local/state/gh-orbit/log   # follow runtime logs (TUI owns stdout)
```

See [CLAUDE.md](./CLAUDE.md) for project-internal conventions — TUI rules,
git wrapper patterns, layout.
