# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project: gh-orbit

A `gh` CLI extension that pulls the parts of Fork (local git history) and the GitHub web UI (PR review/merge) that the author actually uses into a single terminal TUI. The niche between existing tools:

- `gh dash` shows remote PRs/issues but ignores local git state.
- `lazygit` covers local git but its commit-graph view is secondary — Fork's strength is the opposite.

Distribution target: `gh extension install jeonbyeongmin/gh-orbit`, invoked as `gh orbit`. **MVP scope is the local-git half (Fork replacement).** GitHub PR review/merge is a follow-up milestone, which is why `internal/gh/` exists but starts mostly empty.

## Stack

- **Language:** Go 1.26+
- **TUI:** [Bubble Tea](https://github.com/charmbracelet/bubbletea) (Elm-style runtime) + [Lipgloss](https://github.com/charmbracelet/lipgloss) (styling) + [Bubbles](https://github.com/charmbracelet/bubbles) (widgets)
- **Git access:** the user's `git` binary via `os/exec`. Deliberately **not** `go-git` — we want `.gitconfig`, hooks, commit signing, and LFS to keep working with zero extra code.
- **Clipboard:** [`atotto/clipboard`](https://github.com/atotto/clipboard) for the `y` hash-copy action — shells out to `pbcopy`/`xclip`/`xsel`/Win32 so we inherit the user's environment.
- **GitHub access (later):** shell out to `gh` so we inherit `gh auth` instead of running our own OAuth.

## Repository Layout

```
cmd/orbit/         entry point; the binary must be named `gh-orbit` for `gh extension`
internal/tui/      Bubble Tea models, views, key bindings, panes
internal/git/      git CLI wrappers (log, diff, branch, status, ...)
internal/gh/       gh / GitHub API wrappers — reserved for PR features
internal/config/   XDG paths, debug logging setup, user prefs
```

`internal/` over `pkg/`: there is no public Go API to support — keep everything closed until something external needs it.

## Common Commands

```bash
go run ./cmd/orbit                          # run from source against the cwd repo
go build -o gh-orbit ./cmd/orbit            # binary name must be `gh-orbit` for `gh` to pick it up
go test ./...                               # all tests
go test -run TestParseLog ./internal/git    # single test
golangci-lint run                           # lint (govet, errcheck, staticcheck baseline)
gofmt -w .                                  # format

# Install locally as a gh extension and try end-to-end
gh extension remove orbit 2>/dev/null; gh extension install .
gh orbit
```

`gh extension install .` only works from a repo whose binary is named `gh-<name>`, so don't rename the output of `go build`.

## TUI Architecture

The MVP screen is a Fork-style layout — refs sidebar on the left, the commit
graph claiming the top of the right column, and a tab area below it that
switches between the focused commit's metadata and its file-level changes:

```
┌────────┬──────────────────────────────────────┐
│ refs   │   commit graph (full width, 60%)     │
│        │                                      │
│ local  │                                      │
│ remote ├──────────────────────────────────────┤
│ tags   │ [Commit] · Changes  (40%)            │
│        │ author / date / parents / sign       │
│        │ full message — or — file-list ↔ diff │
└────────┴──────────────────────────────────────┘
```

The graph/tab vertical split is user-resizable (`ctrl+↑` / `ctrl+↓`, 5% per
press, clamped to [20, 80]). The bottom tab is `Commit` (full metadata —
author/email, ISO 8601 dates, parent hashes, `%G?` sign-status, full body)
or `Changes` (file-list cursor on the left, follower patch viewport on the
right). `File Tree` is reserved for a follow-up backlog.

### Key bindings

| Key             | Pane          | Action                                         |
| --------------- | ------------- | ---------------------------------------------- |
| `tab`           | global        | cycle pane focus refs → graph → tab (wraps)    |
| `j` / `k`       | focused pane  | navigate within pane (Changes: file-list cursor) |
| `g` / `G`       | focused pane  | jump to top / bottom (Changes: file-list)      |
| `ctrl+d` / `ctrl+u` | Changes tab | scroll the patch follower viewport             |
| `h` / `l` / `←` / `→` | tab pane | switch between Commit and Changes (toggle, wraps) |
| `ctrl+↑` / `ctrl+↓` | global    | resize graph/tab split (5% per press)          |
| `enter`         | refs          | checkout the cursor ref (see "Checkout Behavior") |
| `o`             | refs          | jump graph cursor to ref tip                   |
| `a`             | refs          | show every ref's commits (unified `--all`)     |
| `C`             | graph         | checkout cursor commit as detached HEAD        |
| `y`             | Commit tab    | copy full hash to clipboard                    |
| `d`             | global        | open the focused commit's full patch overlay   |
| `F`             | global        | `git fetch --all` in the background            |
| `P`             | global        | `git pull` in the background (strategy: prefs > git config > `--ff-only`) |
| `r`             | global        | reload refs + log                              |
| `q` / `ctrl+c`  | global        | quit (closes the patch overlay first)          |
| `?`             | global        | toggle expanded help panel (esc/q ignored while open) |
| `R`             | global        | reserved for a future Rebase action            |

Inside the `d` patch overlay only `j` / `k` / `pgup` / `pgdn` / `esc` / `q`
are accepted — the rest of the keymap is gated on normal mode. The
dirty-tree checkout-confirm prompt has its own gated keymap (see
"Checkout Behavior" below).

`?` toggles a multi-line help panel that replaces the bottom hint with
pane-grouped key bindings. While open, every key except `?` and
`ctrl+c` is swallowed (q and esc are ignored). The panel is also
suppressed inside the `d` patch overlay and the dirty-tree checkout
prompt — those modes keep their dedicated single-line hint.

The bottom hint is focus-aware: refs shows `enter checkout · o jump`,
graph shows `enter/d patch · C detach`, tab shows `h/l switch · y copy`.
Every focus appends `? help · q quit`.

Bubble Tea conventions for this codebase:

- One root `tea.Model` per screen; pane sub-models compose into it via the standard `Init/Update/View` trio.
- **Never block in `Update`.** Every git invocation returns asynchronously through `tea.Cmd` → `tea.Msg`. A 50k-commit repo running `git log` synchronously would freeze the UI; stream and paginate.
- **stdout is the TUI** while `tea.Program` is running — any `fmt.Println` will corrupt the screen. Use the file logger (below) instead.
- Vim-style keys: `hjkl` for movement, `:` opens a command line (e.g. `:checkout <branch>`, `:merge <branch>`). Define bindings with `bubbles/key` so help screens stay in sync.
- Cursor moves on the graph fan out to two debounced loaders: a 200ms `git show --numstat` for the Changes-tab file list and an immediate `git show --no-patch` for the Commit-tab metadata. Both share a single `diffReqID` for stale-drop, so a fast `j` mash never paints a previous commit's data.

## Git Wrapper Conventions (`internal/git`)

- The TUI never constructs `*exec.Cmd` directly — it goes through typed wrappers (`Log`, `Stat`, `Patch`, `PatchForFile`, `CommitDetail`, `Refs`, `Fetch`, ...). Makes stubbing in tests possible.
- Prefer `StdoutPipe` + scanner over `CombinedOutput` for anything that can be large (`git log`, `git diff`).
- When a git command fails, wrap stderr into the returned error. The TUI should be able to surface a real message instead of "exit status 128".
- Parse with `--porcelain` / `-z` / `--format=...` whenever available — don't scrape human-readable output. NUL separators in `--format=%H%x00%P%x00...` keep newline-bearing fields like commit bodies safe to split.
- Decoration tokens (`%D`) are parsed into typed `Ref` slices on each commit so the graph row can render branch/tag chips attached to the front of the subject in the message column.

## Commit Row Layout

Each commit renders left-to-right as: `[graph][message (chips + subject)][author][hash][authored time]`. Hash and authored time are right-anchored and always visible; the message column absorbs truncation, with chips and the author column dropping (in that order) before the subject is allowed to fall below one cell.

## Logging

Logs go to `$XDG_STATE_HOME/gh-orbit/log` (defaults to `~/.local/state/gh-orbit/log`). `internal/config` resolves the path and opens the file; the TUI's logger writes there. During development:

```bash
tail -f ~/.local/state/gh-orbit/log
```

If `$XDG_STATE_HOME` is unset, fall back to `~/.local/state/gh-orbit/log` per the XDG Base Directory spec — not `~/.gh-orbit/`.

## User Preferences

User prefs live in `$XDG_CONFIG_HOME/gh-orbit/config.toml` (defaults to `~/.config/gh-orbit/config.toml`). The file is optional — a missing or empty file means "use defaults". `internal/config.LoadPrefs` parses TOML; unknown keys are ignored.

Schema (only field today):

```toml
[pull]
strategy = "rebase"   # "ff-only" | "merge" | "rebase"
```

`P` resolves the strategy in this order: prefs `[pull] strategy` → git config `pull.rebase` (`true` → rebase) → git config `pull.ff` (`only` → ff-only) → final fallback `--ff-only`. A pull conflict surfaces "pull: CONFLICT — resolve in your terminal" in the status bar; resolve with the user's normal git workflow outside the TUI.

## Checkout Behavior

`enter` on the refs pane translates the cursor ref into a local-name argument before invoking `git checkout`:

- Local branch — pass `ShortName` (`main`, `feat/foo`).
- Tag — pass `ShortName`. Result is a detached HEAD on the tag's commit, which is what the user picked.
- Remote-tracking ref — strip the `<remote>/` prefix and pass the inner branch name (`origin/feat` → `feat`). Git's dwim rule then creates a local tracking branch when no same-name local exists; the wrapper does **not** invoke `--track` explicitly.

`C` on the graph pane invokes `git checkout --detach <hash>` against the cursor commit and lands on a detached HEAD.

Dirty working tree handling:

- The wrapper does **not** pre-flight `git status` before checkout. Instead it runs the checkout and matches git's stderr ("Please commit your changes or stash them" / "would be overwritten" / "Your local changes") to wrap the failure with `ErrCheckoutNeedsCleanTree`. No race window exists between detection and the actual command.
- On `ErrCheckoutNeedsCleanTree`, the TUI enters a confirm prompt (mode `viewModeCheckoutConfirm`) where only `s` / `a` / `esc` / `ctrl+c` work; every other key is swallowed.
- `s` runs `git stash push -m "gh-orbit: before checkout <ref>"` (no `-u`, so untracked files stay in the working tree) and then re-issues the checkout. The stash is **not** popped automatically — the status bar surfaces the conventional `stash@{0}` label so the user can resolve it on their own time.
- `a` / `esc` clear `pendingCheckout` and leave the working tree alone.
