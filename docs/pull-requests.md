# pull-requests

Reviewing a PR happens on GitHub now. The cockpit keeps two PR actions
worth a keystroke from the terminal — **jump to the PR** (`enter`, opens
it in the browser) and **land it** (`m`, a merge confirm) — plus a
**Pull Requests page** listing every open PR. The cockpit is a `gh`
extension, so the `gh` CLI is guaranteed present; non-GitHub remotes / a
logged-out `gh` surface `gh`'s own error on the status line.

See [`internal/tui/prreview.go`](../internal/tui/prreview.go) (the web /
merge actions) and [`internal/tui/prs_page.go`](../internal/tui/prs_page.go)
(the page).

## Open on the web (`enter`)

`enter` on a PR-badged graph row runs `gh pr view <n> --web` for the
cursor row's open PR — the `#N` chip badge is the affordance, resolved by
the same `prForCursorRow` the badge renderer uses. Rows without a
PR-bearing chip report `no open PR on this commit` and stay put. The same
web jump is wired to `enter` on the **Worktree page** (the cursor card's
branch PR) and the **Pull Requests page** (the cursor row), so the action
is identical wherever a PR is in view. It's fire-and-forget: a transient
`opening #N…` busy status, then `opened #N in browser` (or `gh`'s error).

## Pull Requests page (`viewModePRsPage`)

The 4th tab, last in the `tab` / `shift+tab` cycle (so `shift+tab` from
the graph wraps onto it). A full-screen page — same vocabulary as the
Worktree dashboard, not a centered overlay — listing every open PR
`gh pr list` returned, **including ones whose head branch isn't checked
out** (which the graph cursor can't reach). The data is `m.prList`, the
gh-ordered slice `prListCmd` already loads for the chip badges; the page
only owns its own cursor (`prsPage.cursor`).

| Key            | Action                                                       |
| -------------- | ------------------------------------------------------------ |
| `↑` / `↓`      | move the cursor                                              |
| `enter`        | open the cursor PR on the web (`gh pr view --web`)           |
| `m`            | merge the cursor PR (arms the merge confirm)                 |
| `r`            | refresh the open-PR list (`gh pr list`)                      |
| `tab` / `⇧tab` | cycle to the next / previous page                            |
| `?`            | toggle the inline help (Global + Pull Requests)             |

Each PR is a **3-line card** (`buildPRCard`), the same 2-col gutter
(cursor bar `▌` + `▶` marker) + 3-line body as the worktree dashboard:

```
▌▶ #124 Local Changes stash/discard/poll        ✓    3h
▌    @alice   feat/local-changes → develop
▌    ● approved   +312 -47   4 files
```

- **Line 1** — `#N title`, then a right-anchored status cluster: the CI
  rollup glyph (`prCheckGlyph`, colored by verdict), a red `⚠` when
  `mergeable == CONFLICTING`, and the relative `updatedAt` age. The title
  absorbs truncation; the cluster never does.
- **Line 2** — the author up front (bright) with the `head → base` branch
  dimmed beside it. The author is the load-bearing bit here and never
  truncates (it used to ride the old single-row tail, where it vanished
  first); the branch absorbs the squeeze.
- **Line 3** — the review-decision dot (`● approved` green · `changes`
  red · `pending` grey, from `reviewDecision`) and the diff size
  (`+adds -dels   N files`). Both are optional — a PR with no review
  decision and no diff data leaves the line blank.

The view always fills exactly the box height (header + scroll-windowed
cards, padded) so the frame never jumps, and clipped lists flag the
overflow with `↑ N more` / `↓ N more`. Unlike the old `l` modal there is
**no empty guard** — a tab you cycle to always shows, rendering
`(no open PRs)` when the list is empty. A refresh landing a shorter list
while the page is open clamps the cursor so it can't vanish past the new
end.

While the page is open and the terminal is focused, a **30s poll**
(`prsPollMsg`, gated like the Local Changes poll: armed on entry, dies on
exit) re-runs `gh pr list` so CI / review / merge state landed by others
surfaces without a manual `r`. The interval is deliberately coarse —
`gh pr list` is a remote GraphQL call, not the local read the Local
Changes 1s poll runs, and `focusFetchThrottle` already pegs remote refresh
at ~60s. The poll skips its round-trip while the window is blurred
(`windowFocused`, toggled by `tea.Focus`/`BlurMsg`) so an idle cockpit
left on the page makes no network calls; `dispatchPRList`'s `prsInFlight`
gate drops a tick that lands mid-load.

## Merge confirm (`m`)

`m` on a PR (graph cursor or PR-page cursor) arms `viewModeMergeConfirm` —
a standalone centered confirm, the same `renderModalBox` vocabulary the
reset confirm uses. `mergeReturnMode` records the launching page so the
dialog composes over — and closes back to — the graph or the PR page
(`isPRsSurface` keeps the breadcrumb steady while it's open). The prompt
offers the three strategies:

```
merge PR #N?
[s] squash · [m] merge · [r] rebase · [esc] cancel
```

`s` / `m` / `r` dispatch `gh pr merge --squash|--merge|--rebase`;
`--delete-branch` is deliberately never passed (deleting the branch is a
surprise the reviewer didn't ask for). While the merge runs the dialog
swaps to a `merging…` spinner line and `mergeInFlight` gates it to
`ctrl+c` only, so a second strategy key can't fork a parallel `gh` call.
The busy status drives the spinner via `statusIsBusy`.

## Outcome routing

- **merge done** — the dialog closes to its launching page;
  `merged #N (<strategy>)` on the status line. The merge landed on the
  remote, so it kicks a `git fetch` (unless one's already in flight); the
  `fetchSucceededMsg` path then reloads the graph (the merge commit
  appears) and re-pulls the PR list (the `#N` badge drops) — no manual
  `F` / `r` needed.
- **merge failed** (not mergeable, checks failing, logged-out `gh`) — the
  dialog closes; `merge failed: <gh error>` (first line) on the status
  line in the error color.
- **web opened / failed** — a one-line status; no mode change.

## Invariants

- `gh` wrappers (`prViewWebExec` / `prMergeExec`) are package-level seams,
  swapped in tests — same pattern as `prListExec`.
- `prForCursorRow` resolves the cursor row's chips against the open-PR map
  (`MergeLocalRemotePairs` → `prForChip`), so `enter` / `m` work exactly
  where a `#N` badge is visible.
- `--delete-branch` is never passed to `gh pr merge`.
