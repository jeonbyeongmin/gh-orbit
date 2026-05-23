# architecture

Stacked TUI. Top dashboard (worktrees + Local Changes meta + fetch freshness) sits above the commit graph; the graph fills the rest of the terminal. The per-commit diff is read in the full-screen `d` patch overlay, with `[` / `]` jumping between files inside it — the previous bottom Commit/Changes tab pane was retired with the subtract-bottom-pane change. Post-PR-B2 the sidebar is retired — graph Enter is the single checkout surface, branches modal (`b`) is the single delete-branch surface, the top dashboard's `w` focus mode is the single worktree-workflow surface.

The shape is closer to `tig` than to Fork: a dense commit cockpit on top, modal patch viewer for the actual diff work. `gh dash` covers the same neighborhood for remote PRs, which gh-orbit will absorb in a follow-up.

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

The top dashboard is data-driven: header + N worktree rows + separator + 2 border rows. paneSizes caps the dashboard at `mainH - 3` so the graph retains ≥3 outer rows. The dashboard is read-only by default — pressing `w` toggles its focus mode so j/k/enter/a/d/esc route to the dashboard's cursor (see [worktrees.md](worktrees.md)). Local Changes is entered globally via `,`. Fetch is throttled by terminal focus events (60s) so an alt-tab burst can't saturate `git fetch`.

The full-screen `d` patch overlay is where commit diffs live (entire `git show -p` body), opened on top of the base layout and closed with `esc` / `q`. Inside the overlay, `[` / `]` jump to the previous / next `diff --git` header so a 20-file patch reads as 20 ordered chapters instead of one long scroll. The bottom hint surfaces `<path> [N/M]` so the reviewer always knows which file the cursor is in.

## Pane composition

One root `tea.Model`. Each pane is a sub-model with the standard `Init/Update/View` trio composed in.

## Key bindings

| Key            | Pane   | Action                                                                              |
| -------------- | ------ | ----------------------------------------------------------------------------------- |
| `j` / `k`      | graph  | navigate the commit list                                                            |
| `g` / `G`      | graph  | jump to top / bottom                                                                |
| `b`            | global | open branches modal (delete-branch entry) — see [branches.md](branches.md)          |
| `w`            | global | toggle dashboard focus (switch / add / remove entry) — see [worktrees.md](worktrees.md) |
| `enter`        | graph  | context-aware: checkout / FF / detach — see [checkout.md](checkout.md)              |
| `y`            | graph  | copy the focused commit's full hash to clipboard                                    |
| `d`            | graph  | open the focused commit's full patch overlay                                        |
| `F`            | global | `git fetch --all` in background                                                     |
| `p`            | global | `git pull` in background (strategy in [config.md](config.md))                       |
| `r`            | global | reload refs + log                                                                   |
| `,`            | global | enter Local Changes mode                                                            |
| `Z`            | global | zombie-branch cleanup — see [branches.md](branches.md)                              |
| `q` / `ctrl+c` | global | quit (closes patch overlay first)                                                   |
| `?`            | global | toggle expanded help panel                                                          |
| `R`            | global | reserved for future Rebase                                                          |

Patch overlay (`d`) accepts only `j` / `k` / `pgup` / `pgdn` / `[` / `]` / `esc` / `q`. `[` jumps to the previous file header, `]` to the next; both are no-ops past the first / last file (no wrap — surprise jumps make the cockpit harder to read, not easier). The dirty-tree checkout-confirm prompt has its own gated keymap (see [checkout.md](checkout.md)). The worktree add-input and remove-confirm sub-modals gate their own keymaps — see [worktrees.md](worktrees.md).

`?` toggles a multi-line help panel that replaces the bottom hint with pane-grouped bindings. Reference, not modal — every shortcut keeps working. Suppressed inside the patch overlay and dirty-tree confirm; those modes keep their own single-line hint.

Bottom hint, single line:

- `enter checkout/ff/detach · d patch · y copy · w worktrees · b branches · Z zombies · ? help · q quit`

## Bubble Tea rules

- Never block in `Update`. Every git invocation returns via `tea.Cmd` → `tea.Msg`. A 50k-commit repo with synchronous `git log` would freeze the UI — stream and paginate.
- Stdout is the TUI while `tea.Program` runs. `fmt.Println` corrupts the screen. Use the file logger from `internal/config` (see [config.md](config.md)).
- Vim-style movement: `hjkl`. `:` reserved for a future command line (`:checkout <branch>`, `:merge <branch>`). Bindings via `bubbles/key` so the help panel stays in sync.

## Commit row

Each commit renders left-to-right: `[graph][message (chips + subject)][author][hash][authored time]`. Hash and authored time are right-anchored and always visible. The message column absorbs truncation; chips and the author column drop (in that order) before the subject is allowed below one cell.
