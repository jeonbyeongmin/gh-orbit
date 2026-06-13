<div align="center">

# gh-orbit

A `gh` CLI extension for reviewing local git in the terminal — commit graph, diffs, branches, and worktrees in one keyboard-driven view.

[![release](https://img.shields.io/github/v/release/jeonbyeongmin/gh-orbit?color=7c6f9f&label=release)](https://github.com/jeonbyeongmin/gh-orbit/releases)
&nbsp;[![gh extension](https://img.shields.io/badge/gh-extension-24292f?logo=github)](https://github.com/jeonbyeongmin/gh-orbit)
&nbsp;![platform](https://img.shields.io/badge/macOS%20·%20Linux%20·%20Windows-555)

English · [한국어](./README.ko.md)

<img src="./docs/assets/demo.gif" alt="gh-orbit demo" width="860">

</div>

---

gh-orbit shows a repository's commit graph, commit and working-tree diffs, branches, and worktrees in a single terminal UI. Every git operation shells out to your own `git` binary, so `.gitconfig`, hooks, commit signing, and LFS work unchanged; PR data comes from the `gh` CLI.

## Install

```bash
gh extension install jeonbyeongmin/gh-orbit
gh orbit
```

Run `gh orbit` inside any git repository.

## Underlying git / gh

Each action runs your own `git` / `gh` — gh-orbit is the interface, not a reimplementation.

| Action | Runs |
| --- | --- |
| Commit graph (on launch) | `git log --all --graph --oneline --decorate` |
| `d` patch overlay + `[` / `]` | `git show -p <commit>`, navigated file by file |
| `,` Local Changes + `space` | `git status` + `git diff` + `git add` / `git restore --staged` |
| `[` / `]` + `space` (diff pane) | per-hunk `git apply --cached` (`--reverse` to unstage) |
| `enter` (checkout / fast-forward) | `git checkout <branch>` / `git merge --ff-only <ref>` |
| `w` worktrees (switch / add / remove) | `git worktree list` / `add` / `remove` |
| `b` → `d` (delete branch) | `git branch -d <branch>` |
| `Z` (zombie cleanup) | `git branch --merged` + a `git branch -d` loop |
| `c` / `R` | `git cherry-pick <commit>` / `git rebase <onto>` |
| `v` / `x` | `git revert <commit>` / `git reset --soft\|--mixed\|--hard <commit>` |
| `n` | `git checkout -b <name> <commit>` |
| `F` / `p` / `P` | `git fetch --all` / `git pull` / `git push` |
| `o` (open PR on web) | `gh pr view --web <number>` |
| `O` → `a` / `m` / `c` / `r` (review PR) | `gh pr diff <n>` · `gh pr review --approve\|--comment\|--request-changes` · `gh pr merge --squash\|--merge\|--rebase` |
| `l` PR list modal | `gh pr list` (reused) → review a row via the `O` overlay |
| PR badges on branch chips | `gh pr list` + `gh pr checks <number>` |
| `y` | `git rev-parse <commit>` → clipboard |

## Features

### Commit graph

- Unified graph across all local branches, remotes, and tags (`--all`) on launch.
- Dot vocabulary: `●` commit · `○` merge · `◉` HEAD. Lane colors rotate an 8-hue palette.
- Each row reads `graph │ message (chips + subject) │ author │ authored time`. Time is right-anchored and always visible; the message column absorbs truncation, dropping chips then author before the subject shrinks below one cell.
- Branch/tag decorations render as chips. A matching open PR adds a `#N` badge with a CI rollup glyph (`✓` pass · `✗` fail · `○` running), refreshed at launch, on `r`, and after each fetch/pull. Repos with no GitHub remote omit the badge.
- Reloads are stale-while-revalidate: the current graph stays on screen while the new one streams in.

### Diff review

- `d` opens the focused commit's full patch (`git show -p`) as a full-screen overlay.
- `[` / `]` jump file-to-file inside the patch; the footer shows `<path> [N/M]`.
- `,` opens Local Changes — a working-tree diff (file tree + diff pane) split into Conflicts / Unstaged / Staged. `space` stages/unstages the focused file, `tab` cycles tree ↔ diff focus, `r` reloads.
- Per-hunk staging: `tab` into the diff pane, `[` / `]` move between hunks (the selected `@@` header is highlighted), and `space` stages just that hunk (`git apply --cached`) — or unstages it when viewing a staged entry. Untracked / conflict files stage whole-file from the tree.

### PR review (`O` / `l`)

- `O` on a commit whose chip carries an open-PR badge pulls that PR's diff (`gh pr diff`) into the same full-screen patch overlay the commit diff uses — `[` / `]` file navigation and scrolling work identically.
- `l` opens the PR list modal — every open PR, including ones whose head branch isn't checked out locally (which `O` can't reach). Rows read `#N <CI glyph> title · author`; `enter` opens the cursor row in the same review overlay, so `a` / `m` behave identically.
- `a` approves (`gh pr review --approve`); `m` merges, picking a strategy — `[s]` squash · `[m]` merge · `[r]` rebase (`gh pr merge`). Both ask in a centered confirm dialog composed over the diff, so it stays in view (dimmed) while you decide.
- `c` comments, `r` requests changes — both open a multi-line body editor over the dimmed diff; `ctrl+s` submits (`gh pr review --comment` / `--request-changes`), `esc` cancels. A failed submit keeps the editor open with the error inline.
- Approve keeps the overlay open (read on, or merge next); merge closes back to the graph and refreshes the PR badges. gh errors (approving your own PR, not mergeable, logged-out `gh`) surface on the hint line.

### Worktrees (`w`)

- `w` opens a full-screen dashboard — one 3-line card per worktree (branch + PR/CI + dirty/time · path · last commit) — so every tree's state reads at once. `esc` returns to the graph.
- `enter` switches the whole UI to another worktree in-process.
- `a` adds a worktree (sibling path auto-derived), `d` removes it (force-confirm for dirty/locked), `s` sorts by last-commit time.
- Each card carries an open-PR `#N` + CI badge (when the branch has one), the `↑a↓b` ahead/behind vs upstream, a `●N` dirty marker (`N` changed files), and the worktree HEAD's last-commit subject and relative time.
- `.git/HEAD` and `.git/index` are watched (fsnotify), so external commits/rebases refresh the list; `r` is always a manual fallback.

### Branches & checkout

- `enter` on the graph picks checkout / fast-forward / detach from the cursor's chips and HEAD relationship. Ambiguous rows open a branch picker; remote-chip rows chain a `git pull`.
- When checkout needs a clean tree, `s` stashes and continues, `a` / `esc` aborts.
- `n` creates a branch at the cursor and switches to it.
- `b` lists local branches; `d` deletes the cursor branch (HEAD protected).
- `Z` bulk-deletes branches that are merged into the default branch, have a `[gone]` upstream, and aren't checked out anywhere — behind one confirm with a reflog recovery hint.

### History operations

- `c` cherry-picks the cursor commit onto the current branch; `R` rebases the current branch onto the cursor commit.
- `v` reverts the cursor commit (records a new commit; safe on pushed history).
- `x` resets the current branch to the cursor commit — `[s]` soft / `[m]` mixed / `[h]` hard. A reset that would rewrite already-pushed history is refused and steered to `v`.
- All four confirm first; conflicts are left in the working tree to resolve in your terminal.

### Network & other keys

- `F` fetch (`git fetch --all`) · `p` pull (strategy-resolved) · `P` push (first push sets upstream; never forces).
- `o` opens the focused commit's PR on GitHub · `y` copies the commit hash · `r` reloads refs + log.
- `?` toggles an inline help panel (Global / Graph / Local Changes columns).

## Key bindings

| Key | Where | Action |
| --- | --- | --- |
| `j` / `k` · `g` / `G` | graph | navigate · jump to top / bottom |
| `enter` | graph | checkout / fast-forward / detach |
| `d` | graph | open the full-screen patch overlay |
| `O` | graph | open the cursor PR's diff in the review overlay |
| `l` | global | open the PR list modal (review any open PR) |
| `[` / `]` | patch | jump to previous / next file |
| `a` / `m` | PR review | approve / merge the open PR (`m` → s/m/r) |
| `c` / `r` | PR review | comment / request changes (body editor → `ctrl+s`) |
| `,` | global | Local Changes view |
| `space` | local changes | stage / unstage the focused file (or hunk, in the diff pane) |
| `[` / `]` | local changes diff | previous / next hunk |
| `w` / `b` | global | worktrees dashboard / branches modal |
| `c` / `R` / `v` / `x` | graph | cherry-pick / rebase / revert / reset |
| `n` | graph | create a branch at the cursor + switch |
| `F` / `p` / `P` | global | fetch / pull / push |
| `o` / `y` | graph | open PR on GitHub / copy hash |
| `Z` / `r` | global | zombie-branch cleanup / reload |
| `?` | global | toggle help panel |
| `ctrl+c` `ctrl+c` | global | quit (press twice) |

## Configuration

XDG-conformant paths (`internal/config` owns resolution):

- Prefs — `$XDG_CONFIG_HOME/gh-orbit/config.toml` (optional):

  ```toml
  [pull]
  strategy = "rebase"   # "ff-only" | "merge" | "rebase"
  ```

  Pull strategy resolves as: prefs `[pull] strategy` → git config `pull.rebase` → `pull.ff` → fallback `--ff-only`.
- Log — `$XDG_STATE_HOME/gh-orbit/log` (the TUI owns stdout, so runtime logging goes here).

## Develop

```bash
go run ./cmd/orbit                    # run against the cwd repo
go test ./...                         # tests
golangci-lint run                     # lint
tail -f ~/.local/state/gh-orbit/log   # follow runtime logs (TUI owns stdout)
```

Regenerate the demo GIF with [VHS](https://github.com/charmbracelet/vhs):

```bash
go build -o /tmp/orbit-demo ./cmd/orbit
vhs docs/assets/demo.tape
```

See [`docs/`](./docs/) for feature-level reference — start at [`docs/index.md`](./docs/index.md).
