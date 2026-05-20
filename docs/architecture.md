# architecture

Stacked TUI. Top dashboard (worktrees + Local Changes meta + fetch freshness), commit graph in the middle, detail tab below. Post-PR-B2 the sidebar is retired — graph Enter is the single checkout surface, branches modal (`b`) is the single delete-branch surface, worktrees modal (`w`) is the single worktree-workflow surface.

```
┌──────────────────────────────────────────────────────────┐
│ Worktrees (4)                  ◆ Local Changes 3 files…  │
│ ▶ main · develop ●                                       │
│   feat-auth · feat/auth                                  │
│   feat-qa · feat/qa                                      │
│   refactor · feat/refactor ●                             │
│ ────────────────────────────── fetched 14m ago           │
├──────────────────────────────────────────────────────────┤
│   commit graph (60% of remaining height)                 │
│   ...                                                    │
├──────────────────────────────────────────────────────────┤
│ [Commit] · Changes  (40%)                                │
│ author / date / parents / sign · full message — or —     │
│ file-list ↔ diff                                         │
└──────────────────────────────────────────────────────────┘
```

The top dashboard is data-driven: header + N worktree rows + separator + 2 border rows. paneSizes caps the dashboard at `mainH - 6` so the graph and tab boxes each retain ≥3 outer rows. The dashboard is read-only — cursor for worktree switching lives in the `w` modal (see [worktrees.md](worktrees.md)). Local Changes is entered globally via `,`. Fetch is throttled by terminal focus events (60s) so an alt-tab burst can't saturate `git fetch`.

- Graph/tab split is user-resizable: `ctrl+↑` / `ctrl+↓`, 5% per press, clamped to [20, 80].
- Tab is `Commit` (full metadata: author/email, ISO 8601 dates, parent hashes, `%G?` sign-status, full body) or `Changes` (file-list cursor left, follower patch viewport right).
- `File Tree` tab is reserved for a follow-up.

## Pane composition

One root `tea.Model`. Each pane is a sub-model with the standard `Init/Update/View` trio composed in.

## Key bindings

| Key                 | Pane          | Action                                                                                                                |
| ------------------- | ------------- | --------------------------------------------------------------------------------------------------------------------- |
| `tab`               | global        | cycle focus graph → tab (wraps)                                                                                       |
| `j` / `k`           | focused       | navigate within pane (Changes: file-list cursor)                                                                      |
| `g` / `G`           | focused       | jump to top / bottom (Changes: file-list)                                                                             |
| `ctrl+d` / `ctrl+u` | Changes       | scroll the patch follower viewport                                                                                    |
| `h` / `l` / `←` / `→` | tab pane    | switch between Commit and Changes (toggle, wraps)                                                                     |
| `ctrl+↑` / `ctrl+↓` | global        | resize graph/tab split                                                                                                |
| `b`                 | global        | open branches modal (delete-branch entry) — see [branches.md](branches.md)                                            |
| `w`                 | global        | open worktrees modal (switch / add / remove entry) — see [worktrees.md](worktrees.md)                                 |
| `enter`             | graph         | context-aware: checkout / FF / detach — see [checkout.md](checkout.md)                                                |
| `y`                 | Commit tab    | copy full hash to clipboard                                                                                           |
| `d`                 | graph / tab   | open the focused commit's full patch overlay                                                                          |
| `F`                 | global        | `git fetch --all` in background                                                                                       |
| `p`                 | global        | `git pull` in background (strategy in [config.md](config.md))                                                         |
| `r`                 | global        | reload refs + log                                                                                                     |
| `q` / `ctrl+c`      | global        | quit (closes patch overlay first)                                                                                     |
| `?`                 | global        | toggle expanded help panel                                                                                            |
| `R`                 | global        | reserved for future Rebase                                                                                            |

Patch overlay (`d`) accepts only `j` / `k` / `pgup` / `pgdn` / `esc` / `q`. The dirty-tree checkout-confirm prompt has its own gated keymap (see [checkout.md](checkout.md)). The worktree add-input and remove-confirm sub-modals gate their own keymaps — see [worktrees.md](worktrees.md).

`?` toggles a multi-line help panel that replaces the bottom hint with pane-grouped bindings. Reference, not modal — every shortcut keeps working. Suppressed inside the patch overlay and dirty-tree confirm; those modes keep their own single-line hint.

Bottom hint is focus-aware:

- graph → `enter checkout/ff/detach · d patch · w worktrees · b branches · Z zombies`
- tab → `h/l switch · y copy · w worktrees · b branches`
- every focus appends `? help · q quit`.

## Bubble Tea rules

- Never block in `Update`. Every git invocation returns via `tea.Cmd` → `tea.Msg`. A 50k-commit repo with synchronous `git log` would freeze the UI — stream and paginate.
- Stdout is the TUI while `tea.Program` runs. `fmt.Println` corrupts the screen. Use the file logger from `internal/config` (see [config.md](config.md)).
- Vim-style movement: `hjkl`. `:` reserved for a future command line (`:checkout <branch>`, `:merge <branch>`). Bindings via `bubbles/key` so the help panel stays in sync.

## Cursor → detail loaders

Cursor moves on the graph fan out to two debounced loaders:

- 200ms `git show --numstat` → Changes-tab file list.
- Immediate `git show --no-patch` → Commit-tab metadata.

Both share a single `diffReqID` for stale-drop. A fast `j` mash never paints a previous commit's data.

## Commit row

Each commit renders left-to-right: `[graph][message (chips + subject)][author][hash][authored time]`. Hash and authored time are right-anchored and always visible. The message column absorbs truncation; chips and the author column drop (in that order) before the subject is allowed below one cell.
