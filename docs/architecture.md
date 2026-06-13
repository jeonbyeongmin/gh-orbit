# architecture

Single-pane TUI. The commit graph fills the whole terminal; everything else lives in overlays. The per-commit diff is read in the full-screen `d` patch overlay, with `[` / `]` jumping between files inside it — the previous bottom Commit/Changes tab pane was retired with the subtract-bottom-pane change, and the top worktree dashboard was retired with the worktrees-modal change. Graph Enter is the single checkout surface, the branches modal (`b`) is the single delete-branch surface, the worktrees modal (`w`) is the single worktree-workflow surface.

The shape is closer to `tig` than to Fork: a dense commit cockpit on top, modal patch viewer for the actual diff work. `gh dash` covers the same neighborhood for remote PRs, which gh-orbit will absorb in a follow-up.

Graph dot vocabulary: `●` regular commit · `○` merge commit (2+ parents — plumbing renders lighter than work) · `◉` the HEAD row. Lane colors rotate through an 8-hue palette ordered so rotation neighbors stay far apart.

```
┌──────────────────────────────────────────────────────────┐
│ Worktrees (4)                  ◆ Local Changes 3 files…  │
│ ▶ main · develop ●                                       │
│   feat-auth · feat/auth                                  │
│   feat-qa · feat/qa                                      │
│   refactor · feat/refactor ●                             │
│ ────────────────────────────── fetched 14m ago           │
├──────────────────────────────────────────────────────────┤
│                                                          │
│   commit graph (fills the remaining terminal height)     │
│   ...                                                    │
│                                                          │
└──────────────────────────────────────────────────────────┘
```

Worktrees live behind the `w` modal — j/k/enter/a/d/s/esc route to its cursor while open (see [worktrees.md](worktrees.md)). Local Changes is entered globally via `,`. Fetch is throttled by terminal focus events (60s) so an alt-tab burst can't saturate `git fetch`.

The full-screen `d` patch overlay is where commit diffs live (entire `git show -p` body), opened on top of the base layout and closed with `esc`. Inside the overlay, `[` / `]` jump to the previous / next `diff --git` header so a 20-file patch reads as 20 ordered chapters instead of one long scroll. The bottom hint surfaces `<path> [N/M]` so the reviewer always knows which file the cursor is in.

## Pane composition

One root `tea.Model`. Each pane is a sub-model with the standard `Init/Update/View` trio composed in.

## Key bindings

| Key            | Pane   | Action                                                                              |
| -------------- | ------ | ----------------------------------------------------------------------------------- |
| `j` / `k`      | graph  | navigate the commit list                                                            |
| `g` / `G`      | graph  | jump to top / bottom                                                                |
| `b`            | global | open branches modal (delete-branch entry) — see [branches.md](branches.md)          |
| `w`            | global | worktrees modal (switch / add / remove / sort) — see [worktrees.md](worktrees.md) |
| `enter`        | graph  | context-aware: checkout / FF / detach — see [checkout.md](checkout.md)              |
| `y`            | graph  | copy the focused commit's full hash to clipboard                                    |
| `d`            | graph  | open the focused commit's full patch overlay                                        |
| `F`            | global | `git fetch --all` in background                                                     |
| `p`            | global | `git pull` in background (strategy in [config.md](config.md))                       |
| `P`            | global | `git push` in background (first push auto-sets upstream; never forces)             |
| `r`            | global | reload refs + log                                                                   |
| `,`            | global | enter Local Changes mode                                                            |
| `Z`            | global | zombie-branch cleanup — see [branches.md](branches.md)                              |
| `^C ^C`        | global | quit (press twice; closes patch overlay first)                                      |
| `?`            | global | toggle inline help reference panel (column layout)                                  |
| `R`            | graph  | rebase current branch onto cursor (confirm-first; conflicts → terminal) — see [checkout.md](checkout.md) |
| `c`            | graph  | cherry-pick cursor commit onto current branch (confirm-first; conflicts → terminal) |
| `v`            | graph  | revert cursor commit (confirm-first; history-preserving; conflicts → terminal)      |
| `x`            | graph  | reset current branch to cursor (soft/mixed/hard; pushed-history → revert)           |
| `n`            | graph  | create branch at cursor + switch (name input modal)                                |
| `o`            | graph  | open the cursor row's open PR on GitHub (`gh pr view --web`; badge rows only)       |
| `O`            | graph  | pull the cursor row's open PR diff into the patch overlay to review — see [pr-review.md](pr-review.md) |

Patch overlay (`d`) accepts only `j` / `k` / `pgup` / `pgdn` / `[` / `]` / `esc`. `[` jumps to the previous file header, `]` to the next; both are no-ops past the first / last file (no wrap — surprise jumps make the cockpit harder to read, not easier). When the overlay was opened with `O` to review a PR (`reviewPRNumber != 0`), `a` (approve) / `m` (merge) open a centered confirm dialog composed over the dimmed diff — see [pr-review.md](pr-review.md). The dirty-tree checkout-confirm prompt has its own gated keymap (see [checkout.md](checkout.md)). The worktree add-input and remove-confirm sub-modals gate their own keymaps — see [worktrees.md](worktrees.md).

`?` toggles an inline help reference panel that grows out of the footer, laying every binding into side-by-side columns (`Global` / `Graph` / `Local Changes`). Reference, not modal — every shortcut keeps working while it is open, and a second `?` collapses it. The panel reserves `helpReservedRows()` rows (the tallest column's height, clamped to ≤ half the screen and never starving the graph below 3 rows), so the graph shrinks by that much while it's open. Narrow terminals that can't fit three columns fall back to the stacked one-row-per-category layout.

Bottom hint, single line — just a pressable `? help` token plus the status message; the full reference grows out of the footer only while `?` is open:

- `? help`

## Bubble Tea rules

- Never block in `Update`. Every git invocation returns via `tea.Cmd` → `tea.Msg`. A 50k-commit repo with synchronous `git log` would freeze the UI — stream and paginate.
- Reloads are stale-while-revalidate: `reloadCmd` keeps the current graph on screen and the new stream's first batch swaps it in place (`graphModel.pendingSwap`) — no blank "loading…" flash on `r` / watcher / post-checkout refreshes. Only the worktree switch hard-resets to the placeholder, because the old tree's graph would mislead. Loading placeholders and busy statuses animate via a single gated spinner tick (`spinnerTickMsg`) that stops re-arming the moment nothing is loading.
- Stdout is the TUI while `tea.Program` runs. `fmt.Println` corrupts the screen. Use the file logger from `internal/config` (see [config.md](config.md)).
- Vim-style movement: `hjkl`. `:` reserved for a future command line (`:checkout <branch>`, `:merge <branch>`). Bindings via `bubbles/key` so the help panel stays in sync.

## Commit row

Each commit renders left-to-right: `[graph][message (chips + subject)][author][authored time]`. Authored time is right-anchored and always visible. The hash is not rendered — `y` copies the focused commit's full hash (the status line echoes the short form). The message column absorbs truncation; chips and the author column drop (in that order) before the subject is allowed below one cell.

Branch chips carry an open-PR badge when `gh pr list` finds a PR whose head branch matches the chip (remote chips match after their `origin/` prefix is stripped): `#N` plus a 1-cell CI rollup glyph — `✓` passing, `✗` failing, `○` still running, nothing when the PR has no checks. The badge renders as a two-tone tail segment: dark bg (236) distinct from the chip's own color, fg carrying the verdict (green 114 / red 203 / orange 214 / neutral 250). Selected and dim rows flatten the whole chip — badge included — to the row override color. The list refreshes at startup, on `r`, and after every successful fetch/pull; failures (no GitHub remote, logged-out `gh`) go to the runtime log, never the status line — the badge is passive enrichment.
