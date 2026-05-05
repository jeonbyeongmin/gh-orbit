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

- Fork-style layout — refs sidebar on the left, commit graph filling the top
  of the right column, and a tab area below it (`Commit` · `Changes`)
- the graph runs against the unified `--all` revision spec by default so
  every local/remote/tag is one walk; `enter` from the refs pane jumps the
  graph cursor to a ref tip without changing the base
- commit row reads left-to-right as `graph | message (chips + subject) |
  author | hash | authored`; the hash and time anchor to the right edge,
  the message column absorbs truncation, and chips/author drop (in that
  order) before the subject shrinks below one cell
- ref decoration (`%D`) is parsed into typed branch/tag entries and
  rendered as chips attached to the front of the subject in the message
  column
- ref pane: lazy auto-scroll on j/k/g/G with overflow clipping (no fold or
  sticky-header — those were tried and removed)
- `Commit` tab: author/email, ISO 8601 dates, parent hashes, `%G?` sign-status
  (`Signed (good)`, `Unsigned`, …), full message body
- `Changes` tab: file-list cursor on the left (own colored `+N -M` rendering
  parsed from `git show --numstat`) plus a follower patch viewport on the
  right that reloads each time the file cursor moves
- `d` opens a full-screen patch overlay for the focused commit; `esc` / `q`
  close it without quitting the app
- vim-style key bindings (full table in [CLAUDE.md](./CLAUDE.md#key-bindings)):
  - `tab` — cycle pane focus (refs → graph → tab, wraps)
  - `j` / `k` / `g` / `G` — navigate within the focused pane
  - `h` / `l` — switch between the Commit and Changes tabs (only when the tab pane is focused)
  - `ctrl+↑` / `ctrl+↓` — resize the graph / tab split (5% per press)
  - `ctrl+d` / `ctrl+u` — scroll the Changes-tab patch viewport
  - `enter` — jump graph cursor to the focused ref tip (refs pane)
  - `a` — show every ref's commits (refs pane)
  - `y` — copy the focused commit's hash to the clipboard (Commit tab)
  - `d` — open the patch overlay (`esc` / `q` to close)
  - `F` — `git fetch --all` in the background
  - `r` — reload refs + log
  - `q` / `ctrl+c` — quit (closes the patch overlay first)
  - `R` is reserved for a future Rebase action
- a status line next to the help row surfaces fetch progress, errors, and
  hash-copy confirmation
- `internal/git` exposes typed wrappers around `git log`, `git show
  --numstat`, `git show -p` (full and per-file), `git show --no-patch`
  metadata bundle, refs decoration, and `git fetch`, all shelling out to the
  user's `git` binary so `.gitconfig`, hooks, signing, and LFS keep working

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
