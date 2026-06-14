<div align="center">

# gh-orbit

A `gh` CLI extension for reviewing local git in the terminal — read the commit graph and diffs, then review and land each worktree's PR from one keyboard-driven dashboard.

[![release](https://img.shields.io/github/v/release/jeonbyeongmin/gh-orbit?color=7c6f9f&label=release)](https://github.com/jeonbyeongmin/gh-orbit/releases)
&nbsp;[![gh extension](https://img.shields.io/badge/gh-extension-24292f?logo=github)](https://github.com/jeonbyeongmin/gh-orbit)
&nbsp;![platform](https://img.shields.io/badge/macOS%20·%20Linux%20·%20Windows-555)

English · [한국어](./README.ko.md)

<img src="./docs/assets/demo.gif" alt="gh-orbit demo" width="860">

</div>

---

gh-orbit shows a repository's commit graph, commit and working-tree diffs, branches, and worktrees in a single terminal UI. The worktree dashboard surfaces every branch's open PR and CI state at once, so when work is spread across worktrees — several coding agents, or just your own parallel branches — you review and land each one's PR without leaving the terminal. Every git operation shells out to your own `git` binary, so `.gitconfig`, hooks, commit signing, and LFS work unchanged; PR data comes from the `gh` CLI.

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
| `→` patch overlay + `[` / `]` | `git show -p <commit>`, navigated file by file |
| Local Changes (`tab` cycle) + `space` | `git status` + `git diff` + `git add` / `git restore --staged` |
| `[` / `]` + `space` (diff pane) | per-hunk `git apply --cached` (`--reverse` to unstage) |
| `space` (checkout / fast-forward) | `git checkout <branch>` / `git merge --ff-only <ref>` |
| `tab` → Worktrees (switch / add / remove) | `git worktree list` / `add` / `remove` |
| `b` → `d` (delete branch) | `git branch -d <branch>` |
| `c` / `R` | `git cherry-pick <commit>` / `git rebase <onto>` |
| `v` / `x` | `git revert <commit>` / `git reset --soft\|--mixed\|--hard <commit>` |
| `n` | `git checkout -b <name> <commit>` |
| `F` / `p` / `P` | `git fetch --all` / `git pull` / `git push` |
| `enter` (open PR on the web) | `gh pr view <n> --web` |
| `m` (merge PR) | `gh pr merge --squash\|--merge\|--rebase` |
| Pull Requests tab | `gh pr list` (reused) → `enter` opens a row on the web, `m` merges it |
| PR badges on branch chips | `gh pr list` + `gh pr checks <number>` |
| `y` | `git rev-parse <commit>` → clipboard |

## Features

### Commit graph

<img src="./docs/assets/commit-graph.gif" alt="commit graph" width="800">

- Unified graph across all local branches, remotes, and tags (`--all`) on launch.
- Dot vocabulary: `●` commit · `○` merge · `◉` HEAD. Lane colors rotate an 8-hue palette.
- Each row reads `graph │ message (chips + subject) │ author │ authored time`. Time is right-anchored and always visible; the message column absorbs truncation, dropping chips then author before the subject shrinks below one cell.
- Branch/tag decorations render as chips. A matching open PR adds a `#N` badge with a CI rollup glyph (`✓` pass · `✗` fail · `○` running), refreshed at launch, on `r`, and after each fetch/pull. Repos with no GitHub remote omit the badge.
- Reloads are stale-while-revalidate: the current graph stays on screen while the new one streams in.

### Diff review

<img src="./docs/assets/diff-review.gif" alt="diff review" width="800">

- `→` opens the focused commit's full patch (`git show -p`) as a full-screen overlay.
- `[` / `]` jump file-to-file inside the patch; the footer shows `<path> [N/M]`.
- The **Local Changes** page (in the `tab` / `shift+tab` cycle) is a working-tree diff (file tree + diff pane) split into Conflicts / Unstaged / Staged. `→` enters the diff pane, `←` returns to the tree, `space` stages/unstages the focused file, `r` reloads.
- Per-hunk staging: `tab` into the diff pane, `[` / `]` move between hunks (the selected `@@` header is highlighted), and `space` stages just that hunk (`git apply --cached`) — or unstages it when viewing a staged entry. Untracked / conflict files stage whole-file from the tree.

### Pull requests (`enter` / `m` / the Pull Requests tab)

<img src="./docs/assets/pull-requests.gif" alt="pull requests" width="800">

Reviewing happens on GitHub; the cockpit jumps you there and lands the PR.

- `enter` on a commit whose chip carries an open-PR badge opens that PR on GitHub in your browser (`gh pr view --web`). The same web jump is wired to `enter` on the Worktree and Pull Requests pages.
- `m` merges the cursor row's open PR via a centered confirm dialog — `[s]` squash · `[m]` merge · `[r]` rebase (`gh pr merge`). Merge closes the dialog and refreshes the PR badges; gh errors (not mergeable, logged-out `gh`) surface on the status line.
- The **Pull Requests tab** (last in the `tab` / `shift+tab` cycle) lists every open PR — including ones whose head branch isn't checked out locally (which the graph cursor can't reach). Rows read `#N <CI glyph> title · author`; `enter` opens the cursor row on the web, `m` merges it, `r` refreshes the list.

### Worktrees (`tab`)

<img src="./docs/assets/worktrees.gif" alt="worktrees dashboard" width="800">

- `tab` cycles to a full-screen dashboard — one 3-line card per worktree (branch + PR/CI + dirty/time · path · last commit) — so every tree's state reads at once. `tab` / `shift+tab` move on to the next / previous page.
- `space` switches the whole UI to another worktree in-process; `enter` opens that worktree's open PR on GitHub in the browser — review each agent's PR on the web, land it with `m` from the graph / Pull Requests tab.
- `a` adds a worktree (sibling path auto-derived), `d` removes it (force-confirm for dirty/locked), `s` sorts by last-commit time.
- Each card carries an open-PR `#N` + CI badge (when the branch has one), the `↑a↓b` ahead/behind vs upstream, a `●N` dirty marker (`N` changed files), and the worktree HEAD's last-commit subject and relative time.
- `.git/HEAD` and `.git/index` are watched (fsnotify), so external commits/rebases refresh the list; `r` is always a manual fallback.

### Branches & checkout

<img src="./docs/assets/branches.gif" alt="branches and checkout" width="800">

- `space` on the graph picks checkout / fast-forward / detach from the cursor's chips and HEAD relationship. Ambiguous rows open a branch picker.
- When checkout needs a clean tree, `s` stashes and continues, `a` / `esc` aborts.
- `n` creates a branch at the cursor and switches to it.
- `b` lists local branches; `d` deletes the cursor branch (HEAD protected).

### History operations

- `c` cherry-picks the cursor commit onto the current branch; `R` rebases the current branch onto the cursor commit.
- `v` reverts the cursor commit (records a new commit; safe on pushed history).
- `x` resets the current branch to the cursor commit — `[s]` soft / `[m]` mixed / `[h]` hard. A reset that would rewrite already-pushed history is refused and steered to `v`.
- All four confirm first; conflicts are left in the working tree to resolve in your terminal.

### Network & other keys

- `F` fetch (`git fetch --all`) · `p` pull (strategy-resolved) · `P` push (first push sets upstream; never forces).
- `y` copies the commit hash · `r` reloads refs + log.
- `?` toggles an inline help panel scoped to the current page (Global + that page's key columns).

## Key bindings

| Key | Where | Action |
| --- | --- | --- |
| `↑` / `↓` · `g` / `G` | graph | navigate · jump to top / bottom |
| `space` | graph | checkout / fast-forward / detach |
| `→` | graph | open the full-screen patch overlay |
| `enter` | graph | open the cursor row's open PR on the web |
| `m` | graph | merge the cursor row's open PR (`s`/`m`/`r` strategy) |
| `[` / `]` | patch | jump to previous / next file |
| `enter` / `m` | pull requests | open on the web / merge the cursor PR |
| `tab` / `⇧tab` | global | cycle pages (graph · worktree · local changes · pull requests) |
| `space` | local changes | stage / unstage the focused file (or hunk, in the diff pane) |
| `[` / `]` | local changes diff | previous / next hunk |
| `b` | global | branches modal |
| `c` / `R` / `v` / `x` | graph | cherry-pick / rebase / revert / reset |
| `n` | graph | create a branch at the cursor + switch |
| `F` / `p` / `P` | global | fetch / pull / push |
| `y` | graph | copy hash |
| `r` | global | reload |
| `?` | global | toggle help panel |
| `ctrl+c` `ctrl+c` | global | quit (press twice) |

## Configuration

XDG-conformant paths (`internal/config` owns resolution):

- Prefs — `$XDG_CONFIG_HOME/gh-orbit/config.toml` (optional):

  ```toml
  [pull]
  strategy = "rebase"   # "ff-only" | "merge" | "rebase"
  ```

  Pull strategy resolves as: prefs `[pull] strategy` → git config `pull.rebase` → fallback `--ff-only`.
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
