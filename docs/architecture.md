# architecture

Single-pane TUI. Four full-screen sibling pages — Graph, Worktree, Local Changes, Pull Requests — cycled with `tab` / `shift+tab` (the only page-nav keys; there is no esc/q exit, and a top breadcrumb names the current page). Graph is home: the commit graph fills the whole terminal, and transient surfaces (confirms, the branches list, the `→` patch overlay) live in overlays on top of it. The per-commit diff is read in the full-screen `→` patch overlay, with `[` / `]` jumping between hunks inside it — the previous bottom Commit/Changes tab pane was retired with the subtract-bottom-pane change, and the top worktree dashboard was retired with the worktrees-modal change. Graph Enter is the single open-PR-on-the-web surface, the branches modal (`b`) is the single delete-branch surface, the Worktree page is the single worktree-workflow surface, the Pull Requests page is the single open-PR list. Reviewing a PR happens on GitHub (`enter` opens it in the browser); the cockpit keeps only the land action — `m` opens a merge confirm (`gh pr merge`).

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

The Worktree page is one step away in the cycle (`tab` from Graph) — ↑/↓/space/enter/a/d/s route to its cursor while it owns the screen (see [worktrees.md](worktrees.md)); `tab` / `shift+tab` move on to the next / previous page rather than closing it. Local Changes is two steps forward; the Pull Requests page is last, so `shift+tab` from Graph wraps straight onto it (see [pull-requests.md](pull-requests.md)). Fetch is throttled by terminal focus events (60s) so an alt-tab burst can't saturate `git fetch`.

The full-screen `→` patch overlay is where commit diffs live (entire `git show -p` body), opened on top of the base layout and closed with `←`. Inside the overlay, `{` / `}` jump to the previous / next `diff --git` header so a 20-file patch reads as 20 ordered chapters instead of one long scroll. The bottom hint surfaces `<path> [N/M]` so the reviewer always knows which file the cursor is in.

## Pane composition

One root `tea.Model`. Each pane is a sub-model with the standard `Init/Update/View` trio composed in.

## Key bindings

| Key            | Pane   | Action                                                                              |
| -------------- | ------ | ----------------------------------------------------------------------------------- |
| `tab` / `⇧tab` | global | cycle page: Graph → Worktree → Local Changes → Pull Requests (and back) — no esc/q exit |
| `↑` / `↓`      | graph  | navigate the commit list                                                            |
| `g` / `G`      | graph  | jump to top / bottom                                                                |
| `b`            | global | open branches modal (delete-branch entry) — see [branches.md](branches.md)          |
| `space`        | graph  | context-aware: checkout / FF / detach — see [checkout.md](checkout.md)              |
| `y`            | graph  | copy the focused commit's full hash to clipboard                                    |
| `→`            | graph  | open the focused commit's full patch overlay                                        |
| `F`            | global | `git fetch --all` in background                                                     |
| `p`            | global | `git pull` in background (strategy in [config.md](config.md))                       |
| `P`            | global | `git push` in background (first push auto-sets upstream; never forces)             |
| `r`            | global | reload refs + log                                                                   |
| `^C ^C`        | global | quit (press twice; closes patch overlay first)                                      |
| `?`            | global | toggle inline help reference panel (column layout)                                  |
| `R`            | graph  | rebase current branch onto cursor (confirm-first; conflicts → terminal) — see [checkout.md](checkout.md) |
| `c`            | graph  | cherry-pick cursor commit onto current branch (confirm-first; conflicts → terminal) |
| `v`            | graph  | revert cursor commit (confirm-first; history-preserving; conflicts → terminal)      |
| `x`            | graph  | reset current branch to cursor (soft/mixed/hard; pushed-history → revert)           |
| `n`            | graph  | create branch at cursor + switch (name input modal)                                |
| `enter`        | graph  | open the cursor row's open PR on GitHub in the browser — see [pull-requests.md](pull-requests.md) |
| `m`            | graph  | merge the cursor row's open PR (confirm dialog) — see [pull-requests.md](pull-requests.md) |

Patch overlay (`→`) accepts only `↑` / `↓` / `pgup` / `pgdn` (scroll), `[` / `]` (prev / next hunk), `{` / `}` (prev / next file), `←` (close), plus the cross-page `?` / `tab` / `⇧tab` / `^C ^C`. `{` jumps to the previous file header, `}` to the next; both are no-ops past the first / last file (no wrap — surprise jumps make the cockpit harder to read, not easier). The dirty-tree checkout-confirm prompt has its own gated keymap (see [checkout.md](checkout.md)). The worktree add-input and remove-confirm sub-modals gate their own keymaps — see [worktrees.md](worktrees.md). The merge confirm (`m`) gates to `s` / `m` / `r` (strategy) / `esc` — see [pull-requests.md](pull-requests.md).

`?` toggles an inline help reference panel that grows out of the footer of **whichever page is showing** (it's a `helpOpen` flag orthogonal to the page mode, not a `viewMode` — `showsHelp()` gates it to the bare page modes). The panel lays the keys into side-by-side columns, scoped to the current page (`helpCategoriesFor`): the graph page shows `Global` / `Graph` / `Sync`, the worktree page `Global` / `Worktree`, the local-changes page `Global` / `Tree` / `Diff`, the pull-requests page `Global` / `Pull Requests`. Only `Global` (`?` / `^C ^C` / `tab` cycle) is cross-page; everything else is reachable only from its own page, so the panel never advertises a key that does nothing where you are. Reference, not modal — every shortcut keeps working while it is open, and a second `?` collapses it. The panel reserves `helpReservedRows()` rows (the tallest column's height for that page, clamped to ≤ half the screen and never starving the page below 3 rows), so the page shrinks by that much while it's open. Narrow terminals that can't fit the columns fall back to the stacked one-row-per-category layout.

Bottom hint, single line — just a pressable `? help` token plus the status message; the full reference grows out of the footer only while `?` is open:

- `? help`

## Bubble Tea rules

- Never block in `Update`. Every git invocation returns via `tea.Cmd` → `tea.Msg`. A 50k-commit repo with synchronous `git log` would freeze the UI — stream and paginate.
- Reloads are stale-while-revalidate: `reloadCmd` keeps the current graph on screen and the new stream's first batch swaps it in place (`graphModel.pendingSwap`) — no blank "loading…" flash on `r` / watcher / post-checkout refreshes. Only the worktree switch hard-resets to the placeholder, because the old tree's graph would mislead. Loading placeholders and busy statuses animate via a single gated spinner tick (`spinnerTickMsg`) that stops re-arming the moment nothing is loading.
- Stdout is the TUI while `tea.Program` runs. `fmt.Println` corrupts the screen. Use the file logger from `internal/config` (see [config.md](config.md)).
- Cursor movement is arrow-only (`↑` / `↓`) — the vim `hjkl` bindings were retired. `:` reserved for a future command line (`:checkout <branch>`, `:merge <branch>`). Bindings via `bubbles/key` so the help panel stays in sync.

## Commit row

Each commit renders left-to-right: `[graph][message (chips + subject)][author][authored time]`. Authored time is right-anchored and always visible. The hash is not rendered — `y` copies the focused commit's full hash (the status line echoes the short form). The message column absorbs truncation; chips and the author column drop (in that order) before the subject is allowed below one cell.

Branch chips carry an open-PR badge when `gh pr list` finds a PR whose head branch matches the chip (remote chips match after their `origin/` prefix is stripped): `#N` plus a 1-cell CI rollup glyph — `✓` passing, `✗` failing, `○` still running, nothing when the PR has no checks. The badge renders as a two-tone tail segment: dark bg (236) distinct from the chip's own color, fg carrying the verdict (green 114 / red 203 / orange 214 / neutral 250). Selected and dim rows flatten the whole chip — badge included — to the row override color. The list refreshes at startup, on `r`, and after every successful fetch/pull; failures (no GitHub remote, logged-out `gh`) go to the runtime log, never the status line — the badge is passive enrichment.
