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
| `p`             | refs          | checkout the cursor ref then pull (skip pull on tag / detached / no-upstream) |
| `o`             | refs          | jump graph cursor to ref tip                   |
| `a`             | refs          | show every ref's commits (unified `--all`)     |
| `n`             | refs          | new branch — base from focus (graph cursor / refs tip / HEAD); modal name input (see "Refs Write Actions") |
| `d`             | refs          | delete branch (modal `[y]/[Y]/[f]/[F]`) or drop stash (modal `[y]`); stash cursors auto-route to the drop confirm |
| `m`             | refs          | rename local branch (modal name input; HEAD branch allowed) |
| `enter`         | graph         | checkout / fast-forward / detach — chosen automatically (see "Graph Enter Behavior"). Stash row → `[p] pop / [a] apply / [esc]` modal. |
| `y`             | Commit tab    | copy full hash to clipboard                    |
| `d`             | graph / tab   | open the focused commit's full patch overlay   |
| `F`             | global        | `git fetch --all` in the background            |
| `P`             | global        | `git pull` in the background (strategy: prefs > git config > `--ff-only`) |
| `r`             | global        | reload refs + log                              |
| `q` / `ctrl+c`  | global        | quit (closes the patch overlay first)          |
| `?`             | global        | toggle expanded help panel (other shortcuts keep working) |
| `R`             | global        | reserved for a future Rebase action            |

Inside the `d` patch overlay only `j` / `k` / `pgup` / `pgdn` / `esc` / `q`
are accepted — the rest of the keymap is gated on normal mode. The
dirty-tree checkout-confirm prompt has its own gated keymap (see
"Checkout Behavior" below).

`?` toggles a multi-line help panel that replaces the bottom hint with
pane-grouped key bindings. The panel is a reference, not a modal — every
shortcut keeps working while it is open (q quits, `?` re-toggles, j/k
navigate the focused pane, etc.). The panel is suppressed inside the
`d` patch overlay and the dirty-tree checkout prompt — those modes keep
their dedicated single-line hint and own their key gating.

The bottom hint is focus-aware: refs shows `enter checkout · p checkout+pull · o jump`,
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

Graph-pane Enter is documented in its own subsection below — it subsumes the old `C` (detach) shortcut as one of its outcomes.

Dirty working tree handling:

- The wrapper does **not** pre-flight `git status` before checkout. Instead it runs the checkout and matches git's stderr ("Please commit your changes or stash them" / "would be overwritten" / "Your local changes") to wrap the failure with `ErrCheckoutNeedsCleanTree`. No race window exists between detection and the actual command.
- On `ErrCheckoutNeedsCleanTree`, the TUI enters a confirm prompt (mode `viewModeCheckoutConfirm`) where only `s` / `a` / `esc` / `ctrl+c` work; every other key is swallowed.
- `s` runs `git stash push -m "gh-orbit: before checkout <ref>"` (no `-u`, so untracked files stay in the working tree) and then re-issues the checkout. The stash is **not** popped automatically — the status bar surfaces the conventional `stash@{0}` label so the user can resolve it on their own time.
- `a` / `esc` clear `pendingCheckout` and leave the working tree alone.

### Combined checkout + pull (`p`)

`p` on the refs pane is the cursor-bound mirror of the global `P`: it checks out the cursor ref and immediately runs `git pull` on the resulting branch. Only the lower-case `p` is bound on the refs pane — the global upper-case `P` (plain pull on the current branch) is unchanged, so the case-pair reads as "global pull vs. cursor-bound pull".

Pull eligibility is decided at keypress time from the ref's `Kind` and `Upstream`:

- Tag → checkout-only (status: `pull skipped: tag has no upstream`). Tags resolve to a detached HEAD with no upstream.
- Local branch with no upstream → checkout-only (`pull skipped: local branch has no upstream`).
- Local branch with an upstream → checkout, then pull.
- Remote-tracking ref → checkout (dwim creates a local tracking branch), then pull.

Dirty working tree handling differs from `enter` / `s`. With `p`, the modal text is `[s] stash & checkout & pull` and `s` chains `stash → checkout → pull → stash pop` automatically — the user's local edits land on top of the freshly-pulled HEAD. Failure modes:

- `pull` conflict: stash pop still runs (the chain treats pop as the final step). Status bar: `pull: CONFLICT — resolve in your terminal; stash preserved at stash@{0}`.
- `pull` generic failure (transport, auth, non-fast-forward): stash is preserved and pop is **not** attempted. Status bar: `pull failed: <reason>; stash preserved at stash@{0}`. Working tree sits on the new ref's clean state; resolve the pull failure manually and `git stash pop` when ready.
- `stash pop` conflict (after a successful pull): conflict markers are written into the working tree and the stash entry is preserved. Status bar: `pop conflict — resolve markers and run \`git stash drop\` (stash@{0})`. No modal — the status bar is the only surface, mirroring the policy for `pull` conflicts.

### Graph Enter Behavior

`enter` on the graph pane is a single context-aware shortcut. The action depends on the cursor commit's chip state and HEAD's relationship to the cursor:

| cursor state                                                       | HEAD                                            | action                                                       |
| ------------------------------------------------------------------ | ----------------------------------------------- | ------------------------------------------------------------ |
| cursor commit matches a stash entry (stash@{N})                    | —                                               | open `viewModeStashActionPicker` → `[p] pop / [a] apply / [esc]`. Wins over every branch / FF / detach branch below. |
| local branch chip 1 (`B`), HEAD on `B`                             | —                                               | no-op (`already on B`)                                       |
| local branch chip 1 (`B`), HEAD elsewhere                          | —                                               | `checkout B`                                                 |
| local branch chips ≥ 2, HEAD on one of them                        | —                                               | no-op                                                        |
| local branch chips ≥ 2, HEAD elsewhere                             | —                                               | open `viewModeBranchPicker` → user picks → `checkout`        |
| no local chip, remote chip with upstream-tracking local `L` (`L` ≠ HEAD) | —                                         | `checkout L` then `git merge --ff-only <cursor>` (cross-branch) |
| no local chip, remote chip with no upstream-tracking local         | —                                               | `git checkout <stripped name>` — git's dwim creates the local tracking branch |
| no local chip (mid-commit or remote-only chip)                     | attached, tip is ancestor of cursor (≠ cursor)  | `git merge --ff-only <cursor>` (Case 1, no checkout step)    |
| no local chip                                                      | detached, **or** not an ancestor of cursor      | `git checkout --detach <cursor>`                             |

Notes:

- Fork's "Checkout & Fast-Forward" surfaces in three ways:
  - Same-branch case: HEAD is on local `main`, cursor row has only an `origin/main` chip → chipless Case 1 FF on `main`. No checkout.
  - Cross-branch case: HEAD is on `feat/foo`, cursor row has only an `origin/develop` chip whose upstream-tracking local is `develop` → `checkout develop` then `git merge --ff-only <cursor>`. The local-tracker rule excludes HEAD itself so the same-branch case stays in the FF lane.
  - New-local case: cursor row has only an `origin/develop` chip and no local tracks it (e.g., never been checked out locally) → `git checkout develop` and let git's dwim create the local tracking branch at the remote's tip.
- Multiple locals tracking the same upstream: cross-branch picks the alphabetically first. Picker UX is reserved for ambiguous local-chip rows; for cross-branch, the refs panel `p` (checkout + pull) is the explicit-choice escape hatch.
- `viewModeBranchPicker` is a modal: `j` / `k` move the cursor, `enter` confirms, `esc` cancels. Every other key is swallowed.
- The decision is computed asynchronously (`evaluateGraphActionCmd`) so the model never blocks Update on git. `actionInFlight` swallows a second Enter while the evaluator is running. A cursor move between Enter dispatch and the evaluator's reply causes the reply to be dropped — re-press Enter on the new row.
- Dirty working tree handling reuses `viewModeCheckoutConfirm` with the matching flag:
  - `withFF=true` → `[s] stash & fast-forward` (same-branch FF, no checkout step).
  - `withCheckoutFF=true` → `[s] stash & checkout & fast-forward` (cross-branch chain).
  All chains follow the no-auto-pop policy of `stashThenCheckoutCmd`.
- Status surfaces are one-line: `fast-forward: main +3`, `fast-forward: develop +2 (after checkout)`, `fast-forward failed: <reason>`, `already on main`, `branch select cancelled`.

## Refs Stash Section

A fourth refs-pane section, `Stashes`, lists `git stash list` entries (Local branches → Remote branches → Tags → Stashes). The list is populated by `git.StashList` (`git stash list --format=%gd%x00%H%x00%gs%x00%aI`), not by `for-each-ref refs/stash` — the latter returns only the top entry. `defaultRefPatterns` deliberately excludes `refs/stash` so the two paths don't dedup-collide.

Each stash entry also appears in the graph: `loadCommitsCmd` appends `m.currentStashHashes` to `git log`'s refspec so the stash commits walk into the stream as additional tips. Stash chips are rendered in a dedicated magenta-leaning slot (`colorChipStash = "165"`) and the label `stash@{N}` is injected into `Commit.RefNames` at stream time because `%D` doesn't surface `refs/stash` tokens.

Race handling: `loadRefsCmd` discovers stash entries; `refsLoadedMsg` calls `diffStashRefs` against `m.currentStashHashes` and, on diff, dispatches `reloadCmd`. Since `reloadCmd` re-issues `loadRefsCmd` itself, the follow-up `refsLoadedMsg` sees the same set → no diff → no infinite loop. First-paint cost: one extra reload after stash hashes arrive.

### Write actions

Three user-explicit stash actions complement the dirty-tree checkout chain's internal stash use:

| Key | Where | Action |
| --- | ----- | ------ |
| `enter` | graph (cursor on stash row) | open `viewModeStashActionPicker` modal `[p] pop · [a] apply · [esc]` |
| `d`     | refs (cursor on stash entry) | open `viewModeStashDropConfirm` modal `[y] drop · [esc]` |

Drop is intentionally exclusive to the refs-pane modal — destructive remove always goes through a "drop?" prompt, never the graph-Enter picker. Push (`git stash push -m <msg>`) is deferred to the changes-view backlog.

The wrappers live in `internal/git/remote.go`:

- `StashList(ctx, dir)` → `[]StashEntry`.
- `StashApply(ctx, dir, label)` — wraps CONFLICT into `ErrStashApplyConflict`.
- `StashDrop(ctx, dir, label)` — no sentinel mapping; stderr surfaces as-is.
- `StashPopAt(ctx, dir, label)` — companion to the existing no-arg `StashPop` (the checkout chain keeps using the no-arg variant; `StashPopAt` exists so the new label-bound caller doesn't perturb that callsite).

Conflict policy mirrors `git stash pop`/`apply`: the entry is preserved in both pop-conflict and apply-conflict outcomes; the status bar advertises `stash <label>: CONFLICT — resolve markers; stash preserved`. Reload still fires so the refs panel + graph reflect any partial state.

Stash slot re-indexing on pop/drop (stash@{1} → stash@{0}) is handled by `reloadCmd`'s full refs/log refresh. Drop success arms `pendingRefCursorAfterDelete{kind: RefKindStash}` so the cursor lands on the next entry in the Stash section (or the previous when the deleted slot was last).

## Refs Write Actions (`n` / `d` / `m`)

Three refs-pane keys for branch lifecycle. All open a centered modal that swallows everything outside its key matrix; `esc` cancels.

### Create (`n`)

Resolves the new branch's base from the focused pane:

- `paneGraph` with a graph cursor → graph cursor commit hash. Modal header reads `[Create branch from '<short hash>']`.
- `paneRefs` with a refs cursor → `cursorRef.ObjectName` (resolved hash, peeled for tags). Header reads `[Create branch from '<ref short name>']`.
- otherwise → empty (HEAD). Header reads `[Create branch from 'HEAD']`.

The textinput is empty on entry. `enter` runs `git check-ref-format --branch <name>` first; on rejection the modal stays open with an inline error. On a clean format, `git branch <name> [<base>]` fires; on `ErrBranchAlreadyExists` / `ErrInvalidRefName` the modal stays open with the inline error so the user can fix the name.

### Delete (`d`, refs focus only)

Graph / tab focus keep the existing patch-overlay binding for `d`. The refs-focus interpretation opens the delete modal, with key matrix derived from the cursor:

| cursor state                                            | hint keys                                          |
| ------------------------------------------------------- | -------------------------------------------------- |
| local branch + matching remote (upstream resolves)      | `[y] local`, `[Y] local+remote`, `[f] force local`, `[F] force local+remote` |
| local branch, no upstream (or stale upstream)           | `[y] delete`, `[f] force delete`                   |
| remote-tracking ref + matching local (upstream-match)   | `[y] local`, `[Y] local+remote`, `[f] force local`, `[F] force local+remote` |
| remote-tracking ref, no matching local                  | `[y] delete remote`                                |

Pre-modal rejections (status bar, no modal):

- HEAD branch cursor → `cannot delete current branch`.
- Tag cursor → `delete: branches only (tags not supported)`.

Lower-case keys (`y` / `f`) target one side; upper-case (`Y` / `F`) include the matching remote. Force is local-only (`-D`); the remote side always invokes `git push <remote> --delete <branch>` without a force option.

Execution order is local → remote, with **no rollback** on partial failure (interview decision):

- local OK + remote OK → `branchDeleteSucceededMsg` → status: `deleted '<name>' + remote '<remote>/<branch>'` (or single-side variants).
- local OK + remote FAIL → `branchDeletePartialMsg` → status: `deleted '<name>'; remote push failed: <reason>` (statusErrS).
- local FAIL (any cause) → remote step is skipped. `ErrBranchNotFullyMerged` from a safe delete routes to `branchDeleteNotMergedMsg` → status: `delete: '<name>' not fully merged — press [f] or [F] to force`. The modal closes; pressing `d` again re-opens it so the user can pick the force pair.

### Rename (`m`)

Local branches only — refs.go emits `refRenameRejectedMsg` for tags / remote-tracking refs / detached HEAD with `rename: local branch only`. The modal pre-fills the textinput with the source name, cursor at end. HEAD-on-source rename is allowed; `headWasOld` is observed before the cmd fires so the post-rename refs reload arms `pendingHEADHash = pendingHEADSentinel` and the graph cursor follows the renamed branch.

### Modal mechanics

- `viewModeRefNameInput` (create / rename) and `viewModeRefDeleteConfirm` (delete) gate the screen; the 3-pane layout stays visible above. The modal panel rows are reserved through `paneSizes` so the main area shrinks rather than overlapping.
- `enter` in the name-input modal arms `validating=true` and dispatches `checkRefFormatCmd` asynchronously. A second `enter` while validating is swallowed so a slow `git check-ref-format` can't double-fire.
- After a successful create / rename, `pendingRefCursorName` is stamped; the next `refsLoadedMsg` calls `refModel.SelectByName` so the cursor lands on the new row. Delete sets `pendingRefCursorAfterDelete` and uses `SelectAfterDeleted` (next-in-section, or previous when last).
- `refActionInFlight` gates `n` / `d` / `m` while a write cmd is running so a second key press can't queue a parallel git invocation.
