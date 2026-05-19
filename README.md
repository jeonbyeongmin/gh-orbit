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

- You read **diffs more often than you write them.** Local Changes
  view and the patch overlay are first-class, not buried.
- You **switch branches a lot** because each agent run lands on a
  fresh branch / worktree. Refs sidebar + graph-cursor checkout are
  built around that.
- You want **machine-reproducible git operations**, not a wrapper
  with its own opinions. Every git call shells out to your `git`
  binary so `.gitconfig`, hooks, signing, and LFS keep working — the
  same git an agent would invoke from a shell.

## Status

**Early WIP.** The MVP scope is the local-side review surface (Fork
replacement, agent-diff review); PR review/merge is the next milestone.
Today the build wires up:

- Fork-style layout — refs sidebar on the left, commit graph filling
  the top of the right column, and a tab area below it
  (`Commit` · `Changes`)
- Local Changes view — a dedicated `● Local Changes` row at the top
  of the refs pane, with an inline meta `N files · +X -Y · Zm ago`
  when the working tree is dirty (numstat against HEAD); `enter`
  jumps into a working-tree diff view so you can read what an agent
  (or you) hasn't committed yet without leaving the TUI
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
- AI-vendor chip — when a commit body's `Co-Authored-By:` trailer matches
  the 4-vendor whitelist (`anthropic.com` / `openai.com` / `cursor.sh` /
  `google.com`), the row sprouts a cyan chip with the vendor name so a
  reviewer can spot AI-authored commits in the graph at a glance;
  lazy-fetched via a 5000-entry LRU cache, dim-dot placeholder while the
  fetch is in flight
- ref pane: lazy auto-scroll on j/k/g/G with overflow clipping (no fold
  or sticky-header — those were tried and removed)
- `Commit` tab: author/email, ISO 8601 dates, parent hashes, `%G?`
  sign-status (`Signed (good)`, `Unsigned`, …), full message body —
  the agent-attribution view, basically
- `Changes` tab: file-list cursor on the left (own colored `+N -M`
  rendering parsed from `git show --numstat`) plus a follower patch
  viewport on the right that reloads each time the file cursor moves
- `d` opens a full-screen patch overlay for the focused commit;
  `esc` / `q` close it without quitting the app
- vim-style key bindings (full table in [docs/architecture.md](./docs/architecture.md)):
  - `tab` — cycle pane focus (refs → graph → tab, wraps)
  - `j` / `k` / `g` / `G` — navigate within the focused pane
  - `h` / `l` — switch between the Commit and Changes tabs (only when
    the tab pane is focused)
  - `ctrl+↑` / `ctrl+↓` — resize the graph / tab split (5% per press)
  - `ctrl+d` / `ctrl+u` — scroll the Changes-tab patch viewport
  - `enter` — context-sensitive on refs pane: checkout cursor ref /
    switch to cursor worktree / open Local Changes view on the
    `● Local Changes` sticky row
  - `d` — delete branch (refs pane, branch row) / remove worktree
    (refs pane, worktree row) / open patch overlay (graph / tab focus);
    see [docs/branches.md](./docs/branches.md) and
    [docs/worktrees.md](./docs/worktrees.md)
  - `a` — add worktree (refs pane, only on a worktree row); see
    [docs/worktrees.md](./docs/worktrees.md)
  - `o` — jump graph cursor to the focused ref tip (refs pane)
  - `Z` — bulk-clean zombie branches (refs pane): every local branch
    merged into the default branch with `upstream:track [gone]` and
    not checked out anywhere is offered for delete in a single confirm
    modal; see [docs/branches.md](./docs/branches.md)
  - `y` — copy the focused commit's hash to the clipboard (Commit tab)
  - `F` — `git fetch --all` in the background
  - `p` — `git pull` in the background; strategy from
    `~/.config/gh-orbit/config.toml` (`[pull] strategy = "ff-only" |
    "merge" | "rebase"`), then git's `pull.rebase` / `pull.ff`,
    falling back to `--ff-only`
  - `r` — reload refs + log
  - `q` / `ctrl+c` — quit (closes the patch overlay first)
  - `R` is reserved for a future Rebase action
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
   into the same three-pane layout, read its diff with the Changes
   tab, approve / request-changes / merge inline.

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
