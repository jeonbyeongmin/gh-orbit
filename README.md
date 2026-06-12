# gh-orbit

A `gh` CLI extension that gives you a terminal **review cockpit for
AI-coding-agent work** — local diff, commit graph, refs, and (soon)
PR review in one TUI.

## Why?

AI coding agents (Claude Code, Cursor agents, Codex, etc.) produce
commits, branches, and PRs faster than the usual review chain keeps up
with. The bottleneck stops being "write the code" and starts being
"figure out what the agent just did, on which branch, against which
base, and whether to keep it." Today that loop is split across:

- `git status` / `git diff` in one terminal,
- `gh dash` or the GitHub web UI for the agent's PR,
- `lazygit` or Fork for the local commit graph,
- a fourth window for `git log` on the agent's worktree.

[`gh dash`](https://github.com/dlvhdr/gh-dash) covers remote PRs but
ignores local git state. [`lazygit`](https://github.com/jesseduffield/lazygit)
covers local git but its commit-graph view is secondary — Fork's
strength is the opposite, and Fork has no terminal build. `gh-orbit`
collapses the loop into one TUI optimized for the "an agent just did
30 minutes of work — what changed, is it good, ship or scrap" review
pattern.

Concretely, the design assumes:

- You read **diffs more often than you write them.** The full-screen
  patch overlay (`d`) is the primary review surface, with `[` / `]`
  to jump between files; the Local Changes view covers the working
  tree on the same overlay-first pattern.
- You **switch branches a lot** because each agent run lands on a
  fresh branch / worktree. Graph-cursor checkout (`enter`) + worktree
  modal (`w`) + branches modal (`b`) are built around that.
- You want **machine-reproducible git operations**, not a wrapper
  with its own opinions. Every git call shells out to your `git`
  binary so `.gitconfig`, hooks, signing, and LFS keep working — the
  same git an agent would invoke from a shell.

The aesthetic is closer to `tig` than to Fork — a dense commit-graph
cockpit with metadata on top, modal patch viewer for the actual diff
work — not a three-pane file-browser. `gh dash` lives in the same
neighborhood for remote PRs, which is the next milestone.

## Status

**Early WIP.** The MVP scope is the local-side review surface
(agent-diff review on top of a tig-style cockpit); PR review/merge is
the next milestone.
Today the build wires up:

- Single-pane layout — the commit graph fills the whole terminal;
  worktrees (`w`), branches (`b`), and the per-commit diff (`d`,
  full-screen patch overlay) all live in overlays
- Local Changes view — `,` jumps into a working-tree diff view
  (file tree + diff, stage/unstage) so you can read what hasn't
  been committed yet without leaving the TUI
- the graph runs against the unified `--all` revision spec by default
  so every local/remote/tag is one walk; `enter` from the refs pane
  jumps the graph cursor to a ref tip without changing the base
- commit row reads left-to-right as `graph | message (chips + subject) |
  author | hash | authored`; the hash and time anchor to the right edge,
  the message column absorbs truncation, and chips/author drop (in that
  order) before the subject shrinks below one cell
- ref decoration (`%D`) is parsed into typed branch/tag entries and
  rendered as chips attached to the front of the subject in the message
  column
- ref pane: lazy auto-scroll on j/k/g/G with overflow clipping (no fold
  or sticky-header — those were tried and removed)
- `d` opens a full-screen patch overlay for the focused commit
  (entire `git show -p` body); inside, `[` / `]` jump between files
  and the bottom hint shows `<path> [N/M]` so you always know which
  file the cursor is in; `esc` closes it without quitting the app
- vim-style key bindings (full table in [docs/architecture.md](./docs/architecture.md)):
  - `j` / `k` / `g` / `G` — navigate the commit graph
  - `enter` — context-sensitive on the graph: checkout / FF / detach
  - `d` — open the focused commit's patch overlay; inside, `[` / `]`
    jump prev / next file
  - `,` — open the Local Changes view (working-tree diff)
  - `w` / `b` — open the worktrees / branches modal; see
    [docs/worktrees.md](./docs/worktrees.md) and
    [docs/branches.md](./docs/branches.md)
  - `Z` — bulk-clean zombie branches (every local branch merged into
    the default branch with `upstream:track [gone]` and not checked
    out anywhere is offered for delete in a single confirm modal);
    see [docs/branches.md](./docs/branches.md)
  - `y` — copy the focused commit's hash to the clipboard
  - `F` — `git fetch --all` in the background
  - `p` — `git pull` in the background; strategy from
    `~/.config/gh-orbit/config.toml` (`[pull] strategy = "ff-only" |
    "merge" | "rebase"`), then git's `pull.rebase` / `pull.ff`,
    falling back to `--ff-only`
  - `r` — reload refs + log
  - `P` — push the current branch (first push auto-sets upstream)
  - `c` — cherry-pick the focused commit onto the current branch
  - `n` — create a branch at the focused commit and switch to it
  - `o` — open the focused commit on GitHub
  - `ctrl+c` `ctrl+c` — quit (press twice; works anywhere, including
    inside the patch overlay)
  - `R` — rebase the current branch onto the focused commit
    (confirm dialog; conflicts are left for your terminal)
- a status line next to the help row surfaces fetch progress, errors,
  and hash-copy confirmation
- `internal/git` exposes typed wrappers around `git log`, `git show
  --numstat`, `git show -p` (full and per-file), `git show --no-patch`
  metadata bundle, refs decoration, and `git fetch`, all shelling out
  to the user's `git` binary

## Roadmap

Ordered by current intent, not commitment:

1. **Per-hunk staging** — extend the existing Local Changes stage /
   unstage with per-hunk operations so reviewing an agent's working
   tree doesn't require dropping to a second shell.
2. **PR review pane** — the original Fork+`gh dash` half: pull a PR
   into the same layout, read its diff in the patch overlay, approve
   / request-changes / merge inline.

Neither is wired up yet — they're listed so the project's trajectory
is legible from the README.

## Install

```bash
gh extension install jeonbyeongmin/gh-orbit
gh orbit
```

## Develop

```bash
go run ./cmd/orbit                    # run against the cwd repo
go test ./...                         # tests
golangci-lint run                     # lint
tail -f ~/.local/state/gh-orbit/log   # follow runtime logs (TUI owns stdout)
```

See [CLAUDE.md](./CLAUDE.md) for the behavioral contract this repo
uses with its own AI coding agents (this project is built that way),
and [`docs/`](./docs/) for feature-level reference.
