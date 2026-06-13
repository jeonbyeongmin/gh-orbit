# pr-review

`O` pulls the cursor row's open PR into the same full-screen patch
overlay the commit diff (`d`) uses, then approve / merge run inline
without leaving the cockpit. Entry is cursor-row, not a list: the row's
`#N` chip badge is the affordance, resolved by the same
`prForCursorRow` matching the badge renderer uses. Rows without a
PR-bearing chip report `no open PR on this commit` and stay put.

The overlay is `viewModeDiffWindow` reused verbatim — same viewport,
same `[` / `]` file navigation. `reviewPRNumber != 0` is the only thing
that distinguishes a PR overlay from a commit patch: it swaps the
bottom hint to `renderPRReviewHint` and arms `a` / `m`.

## Why the confirms aren't modals

The diff overlay is full-screen, so a centered `composeOverlay` confirm
box would paint over the *graph* base (`View()` early-returns for
`viewModeDiffWindow`, never composing the diff under a modal). Approve
and merge instead live as a sub-state of the overlay (`prAction`) and
only swap the bottom hint line — the `viewModeCheckoutConfirm` idiom.
The diff stays on screen while the reviewer confirms, which is exactly
what reading-then-merging wants.

## Keymap (inside the PR overlay)

| Key | State | Action |
| --- | --- | --- |
| `a` | browse | arm approve confirm |
| `m` | browse | arm merge confirm |
| `[` / `]` · `j` / `k` · pgup/pgdn | browse | file nav + scroll (shared with commit overlay) |
| `y` | approve armed | `gh pr review --approve` |
| `s` / `m` / `r` | merge armed | `gh pr merge --squash` / `--merge` / `--rebase` |
| `esc` | armed | cancel back to browse (overlay stays open) |
| `esc` / `q` | browse | close overlay → graph |
| `ctrl+c` | in-flight | only key honored while a gh call runs |

`O` opens; lowercase `o` still opens the PR on the web
(`gh pr view --web`). `--delete-branch` is deliberately never passed to
`gh pr merge`.

## Outcome routing

- **approve done** — overlay stays open (read on, or chain a merge);
  `approved #N` notice on the hint line, dismissed by the next key.
  Refreshes the PR list.
- **merge done** — overlay closes to the graph; `merged #N (<strategy>)`
  on the normal status line. Refreshes the PR list so the `#N` badge
  tracks the closed PR. No auto-fetch — the local graph reflects the
  merge only after the next `F` / `p`.
- **failure** (approving your own PR, not mergeable, logged-out `gh`) —
  overlay stays open; `gh`'s own error (first line) on the hint line in
  the error color.

## Invariants

- `prReviewInFlight` gates the overlay to `ctrl+c` only while an approve
  / merge gh call runs — a second press can't fork a parallel call
  (mirrors `refActionInFlight` for the delete confirm).
- The busy status (`approving #N…` / `merging #N…`) drives the spinner
  via `statusIsBusy`, which `spinnerVisible` checks before the mode
  switch — so the overlay animates even though `diffModel` itself isn't
  loading. `clearBusy` drops it on every outcome so the spinner can't
  spin forever (the diff `View()` never renders `m.status`).
- The PR diff reuses `diffPatchLoadedMsg` / `diffPatchFailedMsg`; the
  synthetic `prDiffID(n)` (`pr/<n>`) stands in for the commit hash the
  `diffModel` stale-drop guard (`accepts`) keys on. Zero `diffModel`
  changes.
- gh wrappers (`prDiffExec` / `prReviewExec` / `prMergeExec`) are
  package-level seams, swapped in tests — same pattern as
  `prListExec` / `browsePRExec`.

See [`internal/tui/prreview.go`](../internal/tui/prreview.go).
