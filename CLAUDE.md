# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project: gh-orbit

A `gh` CLI extension that pulls the parts of Fork (local git history) and the GitHub web UI (PR review/merge) that the author actually uses into a single terminal TUI. The niche between existing tools:

- `gh dash` shows remote PRs/issues but ignores local git state.
- `lazygit` covers local git but its commit-graph view is secondary — Fork's strength is the opposite.

Distribution target: `gh extension install jeonbyeongmin/gh-orbit`, invoked as `gh orbit`. **MVP scope is the local-git half (Fork replacement).** GitHub PR review/merge is a follow-up milestone, which is why `internal/gh/` exists but starts mostly empty.

## Stack

- **Language:** Go 1.24+
- **TUI:** [Bubble Tea](https://github.com/charmbracelet/bubbletea) (Elm-style runtime) + [Lipgloss](https://github.com/charmbracelet/lipgloss) (styling) + [Bubbles](https://github.com/charmbracelet/bubbles) (widgets)
- **Git access:** the user's `git` binary via `os/exec`. Deliberately **not** `go-git` — we want `.gitconfig`, hooks, commit signing, and LFS to keep working with zero extra code.
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

The MVP screen is a Fork-style 3-pane layout:

```
┌────────┬──────────────────────┬─────────────────┐
│ refs   │   commit graph       │   diff          │
│ branches/tags/PRs │  selected ─┘  of selected commit │
└────────┴──────────────────────┴─────────────────┘
```

Bubble Tea conventions for this codebase:

- One root `tea.Model` per screen; pane sub-models compose into it via the standard `Init/Update/View` trio.
- **Never block in `Update`.** Every git invocation returns asynchronously through `tea.Cmd` → `tea.Msg`. A 50k-commit repo running `git log` synchronously would freeze the UI; stream and paginate.
- **stdout is the TUI** while `tea.Program` is running — any `fmt.Println` will corrupt the screen. Use the file logger (below) instead.
- Vim-style keys: `hjkl` for movement, `:` opens a command line (e.g. `:checkout <branch>`, `:merge <branch>`). Define bindings with `bubbles/key` so help screens stay in sync.

## Git Wrapper Conventions (`internal/git`)

- The TUI never constructs `*exec.Cmd` directly — it goes through typed wrappers (`Log`, `Diff`, `Branches`, ...). Makes stubbing in tests possible.
- Prefer `StdoutPipe` + scanner over `CombinedOutput` for anything that can be large (`git log`, `git diff`).
- When a git command fails, wrap stderr into the returned error. The TUI should be able to surface a real message instead of "exit status 128".
- Parse with `--porcelain` / `-z` / `--format=...` whenever available — don't scrape human-readable output.

## Logging

Logs go to `$XDG_STATE_HOME/gh-orbit/log` (defaults to `~/.local/state/gh-orbit/log`). `internal/config` resolves the path and opens the file; the TUI's logger writes there. During development:

```bash
tail -f ~/.local/state/gh-orbit/log
```

If `$XDG_STATE_HOME` is unset, fall back to `~/.local/state/gh-orbit/log` per the XDG Base Directory spec — not `~/.gh-orbit/`.
