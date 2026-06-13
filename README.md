<div align="center">

# gh-orbit

**A terminal review cockpit — your local commit graph, diffs, branches, and worktrees in one keyboard-driven TUI.**

[![release](https://img.shields.io/github/v/release/jeonbyeongmin/gh-orbit?color=7c6f9f&label=release)](https://github.com/jeonbyeongmin/gh-orbit/releases)
&nbsp;[![gh extension](https://img.shields.io/badge/gh-extension-24292f?logo=github)](https://github.com/jeonbyeongmin/gh-orbit)
&nbsp;![platform](https://img.shields.io/badge/macOS%20·%20Linux%20·%20Windows-555)

English · [한국어](./README.ko.md)

<img src="./docs/assets/demo.gif" alt="gh-orbit demo" width="860">

</div>

---

## Why gh-orbit?

A branch just landed 30 minutes of work. **What changed, against which base, and do you keep it?** Today that question is split across four windows — `git diff` in one terminal, the GitHub web UI for the PR, `lazygit` or Fork for the graph, a fourth shell for `git log` on another worktree.

`gh-orbit` collapses that loop into a single TUI built for the *review* pattern, not the *write* pattern.

- **🛰️ One cockpit, the whole loop.** Commit graph, full-screen diff, branches, worktrees, and the working tree — no window juggling, no context loss.
- **📖 Diff-first, not file-browser-first.** You read diffs more often than you write them. The full-screen patch overlay is the primary surface; `[` / `]` turn a 20-file patch into 20 ordered chapters.
- **🌳 Worktree-native.** Each unit of work lands on a fresh branch/worktree. Switch between them **in-process** — no second terminal, no disturbing whatever is running on the other tree.
- **🔧 Your real git underneath.** Every operation shells out to your own `git` binary, so `.gitconfig`, hooks, commit signing, and LFS keep working exactly as they do in a shell. No reimplemented git, no surprises.
- **🔔 PR-aware at a glance.** Branch chips carry an open-PR badge — `#N` plus a 1-cell CI rollup glyph (`✓` pass / `✗` fail / `○` running) — fed by a background `gh pr list`.
- **⌨️ Keyboard-driven & fast.** Vim-style movement, modal overlays, never blocks on git. Built on [Bubble Tea](https://github.com/charmbracelet/bubbletea).

The aesthetic is closer to `tig` than to Fork: a dense commit cockpit on top, a modal patch viewer for the actual diff work.

## What it replaces

Every action is your real `git` / `gh` underneath — `gh-orbit` is the cockpit, not a reimplementation. One keystroke for the commands you'd otherwise run across four windows:

| In `gh-orbit` | Equivalent `git` / `gh` |
| --- | --- |
| Commit graph (unified, on launch) | `git log --all --graph --oneline --decorate` |
| `d` patch overlay + `[` / `]` | `git show -p <commit>`, scrolled file by file |
| `,` Local Changes + `space` | `git status` + `git diff` + `git add` / `git restore --staged` |
| `enter` (checkout / fast-forward) | `git checkout <branch>` / `git merge --ff-only <ref>` |
| `w` worktrees (switch / add / remove) | `git worktree list` / `add` / `remove` |
| `b` → `d` (delete branch) | `git branch -d <branch>` |
| `Z` zombie cleanup | `git branch --merged` + a manual `git branch -d` loop |
| `c` / `R` | `git cherry-pick <commit>` / `git rebase <onto>` |
| `v` / `x` | `git revert <commit>` / `git reset --soft\|--mixed\|--hard <commit>` |
| `n` | `git checkout -b <name> <commit>` |
| `F` / `p` / `P` | `git fetch --all` / `git pull` / `git push` |
| `o` (open PR) | `gh pr view --web <number>` |
| PR badges on branch chips | `gh pr list` + `gh pr checks <number>` |
| `y` | `git rev-parse <commit>` → clipboard |

## Install

```bash
gh extension install jeonbyeongmin/gh-orbit
gh orbit
```

Run `gh orbit` from inside any git repository.

## Features

### 🛰️ Commit graph cockpit

- **Unified graph** — walks the `--all` revision spec by default, so every local branch, remote, and tag is one graph.
- **Lane-colored graph** with a clear dot vocabulary: `●` regular commit · `○` merge commit · `◉` the HEAD row. Lane hues rotate through an 8-color palette ordered so neighbors stay distinct.
- **Legible commit rows** — each row reads `graph │ message (chips + subject) │ author │ authored time`. Time is right-anchored and always visible; the message column absorbs truncation, with chips and author dropping (in that order) before the subject ever shrinks below one cell.
- **Ref chips** — branch/tag decorations render as typed chips on the front of the subject.
- **PR badges** — branch chips show `#N` + a CI rollup glyph (`✓`/`✗`/`○`) for matching open PRs, refreshed at startup, on `r`, and after every fetch/pull. Repos with no GitHub remote degrade silently.
- **No-flash reloads** — stale-while-revalidate: the current graph stays on screen while the new one streams in. Loading states animate via a single gated spinner.

### 📖 Diff review

- **`d` — full-screen patch overlay** for the focused commit (the entire `git show -p` body).
- **`[` / `]` — jump file-to-file** inside the patch; the bottom hint shows `<path> [N/M]` so you always know where the cursor is.
- **`,` — Local Changes view** — a working-tree diff (file tree + diff pane) split into Conflicts / Unstaged / Staged sections, so you can review what isn't committed yet without leaving the TUI.
  - `space` stage / unstage the focused file · `tab` cycle tree ↔ diff focus · `r` reload · `j`/`k`/`g`/`G` navigate.

### 🌳 Worktrees (`w`)

- **In-process switch** — pick a worktree, hit `enter`, and the whole cockpit retargets the new tree. No second terminal.
- **Add / remove / sort** — `a` adds a worktree (sibling path auto-derived), `d` removes (with force-confirm for dirty/locked), `s` sorts by last-commit time.
- **Live status** — each row shows a `●` dirty marker, the worktree HEAD's last-commit subject + relative time.
- **External-change watcher** — `.git/HEAD` and `.git/index` are watched via fsnotify, so a commit or rebase in another shell refreshes the inventory automatically (manual `r` always works as a fallback).

### 🌿 Branches & checkout

- **`enter` — context-aware checkout** on the graph: checkout a local branch, fast-forward, or detach, decided from the cursor's chip state and HEAD relationship. Ambiguous rows open a branch picker; remote-chip rows chain a `git pull` so "enter on `origin/x`" means "synced with the network."
- **Dirty-tree confirm** — when checkout needs a clean tree, choose `s` (stash & continue) or `a`/`esc` (abort). The stash is left in place; returning to the branch reminds you to pop it.
- **`n` — new branch at cursor** + switch in one step.
- **`b` — branches modal** — list every local branch, `d` to delete (HEAD protected).
- **`Z` — zombie cleanup** — bulk-delete local branches that are merged into the default branch, have a `[gone]` upstream, and aren't checked out anywhere — all behind a single confirm with a reflog recovery hint.

### ✂️ History ops & the scrap path

- **`c` — cherry-pick** the cursor commit onto the current branch.
- **`R` — rebase** the current branch onto the cursor commit.
- **`v` — revert** the cursor commit (history-preserving; safe on already-pushed commits).
- **`x` — reset** the current branch to the cursor commit (`[s]` soft / `[m]` mixed / `[h]` hard). A pushed-history reset would need a force-push, so it's refused and steered to `v`.
- All four are **confirm-first**, and conflicts are left in place for you to resolve in your terminal.

### 🌐 Network & misc

- **`F` fetch** (`git fetch --all`, background) · **`p` pull** (strategy-resolved) · **`P` push** (first push auto-sets upstream; never forces).
- **`o`** — open the focused commit's PR on GitHub · **`y`** — copy the commit hash · **`r`** — reload refs + log.
- **`?`** — toggle an inline help reference panel (Global / Graph / Local Changes columns).

## Key bindings

| Key | Where | Action |
| --- | --- | --- |
| `j` / `k` · `g` / `G` | graph | navigate · jump to top / bottom |
| `enter` | graph | context-aware checkout / fast-forward / detach |
| `d` | graph | open the full-screen patch overlay |
| `[` / `]` | patch | jump to previous / next file |
| `,` | global | Local Changes view (working tree) |
| `space` | local changes | stage / unstage the focused file |
| `w` / `b` | global | worktrees / branches modal |
| `c` / `R` / `v` / `x` | graph | cherry-pick / rebase / revert / reset |
| `n` | graph | create a branch at the cursor + switch |
| `F` / `p` / `P` | global | fetch / pull / push |
| `o` / `y` | graph | open PR on GitHub / copy hash |
| `Z` / `r` | global | zombie-branch cleanup / reload |
| `?` | global | toggle help reference panel |
| `ctrl+c` `ctrl+c` | global | quit (press twice) |

## Configuration

XDG-conformant paths (`internal/config` owns resolution):

- **Prefs** — `$XDG_CONFIG_HOME/gh-orbit/config.toml` (optional):

  ```toml
  [pull]
  strategy = "rebase"   # "ff-only" | "merge" | "rebase"
  ```

  Pull strategy resolves as: prefs `[pull] strategy` → git config `pull.rebase` → `pull.ff` → fallback `--ff-only`.
- **Log** — `$XDG_STATE_HOME/gh-orbit/log` (the TUI owns stdout, so all runtime logging goes here).

## Roadmap

Ordered by current intent, not commitment:

1. **Per-hunk staging** — extend Local Changes stage/unstage with per-hunk operations.
2. **PR review pane** — pull a PR into the same layout, read its diff in the patch overlay, and approve / request-changes / merge inline.

## Develop

```bash
go run ./cmd/orbit                    # run against the cwd repo
go test ./...                         # tests
golangci-lint run                     # lint
tail -f ~/.local/state/gh-orbit/log   # follow runtime logs (TUI owns stdout)
```

The demo GIF is regenerated with [VHS](https://github.com/charmbracelet/vhs):

```bash
go build -o /tmp/orbit-demo ./cmd/orbit
vhs docs/assets/demo.tape
```

See [`docs/`](./docs/) for feature-level reference — start at [`docs/index.md`](./docs/index.md).
