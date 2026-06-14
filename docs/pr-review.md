# pr-review

`enter` pulls the cursor row's open PR into the same full-screen patch
overlay the commit diff (`d`) uses, then approve / merge run inline
without leaving the cockpit. The row's `#N` chip badge is the
affordance, resolved by the same `prForCursorRow` the badge renderer
uses; rows without a PR-bearing chip report `no open PR on this commit`
and stay put.

`l` opens the PR list modal — every open PR `gh pr list` returned,
including ones whose head branch isn't checked out (which the cursor
path can't reach). `enter` on a row opens that PR in the very same
overlay via the shared `beginPRReviewFor`, so approve / merge behave
identically regardless of which entry point you used.

## PR list modal (`l`)

`viewModePRsModal` is a centered overlay — same vocabulary as the
branches modal: `j` / `k` navigate, `enter` reviews,
`l` / `q` / `esc` close. Rows read `#N <glyph> title · author`, where
`<glyph>` is the shared CI rollup (`prCheckGlyph`); the title then the
author absorb truncation so `#N` and the glyph always survive. The data
is `m.prList`, the gh-ordered slice `prListCmd` already loads for the
chip badges — the modal only owns its own cursor. `enter` routes
through `beginPRReviewFor`, the cursor-agnostic core split out of
`beginPRReview`.

The overlay is `viewModeDiffWindow` reused verbatim — same viewport,
same `[` / `]` file navigation. `reviewPRNumber != 0` is the only thing
that distinguishes a PR overlay from a commit patch: it swaps the
bottom hint to `renderPRReviewHint` and arms `a` / `m`.

## Confirm dialogs over the diff

Approve and merge open a centered confirm dialog — the same
`renderModalBox` vocabulary the rebase / revert / reset confirms use.
Because the diff overlay is full-screen and `View()` early-returns for
`viewModeDiffWindow`, the dialog is composed (`composeOverlay`) over the
**diff view** as its base, not the graph: the diff dims behind the box
so the reviewer keeps it in view while deciding. `prAction`
(`approve` / `merge`) holds which confirm is armed; pressing the action
key dispatches and closes the box, and the busy state (`approving #N…`)
shows on the hint line behind it.

## Keymap (inside the PR overlay)

| Key | State | Action |
| --- | --- | --- |
| `a` | browse | arm approve confirm |
| `m` | browse | arm merge confirm |
| `c` / `r` | browse | open the comment / request-changes body editor |
| `[` / `]` · `j` / `k` · pgup/pgdn | browse | file nav + scroll (shared with commit overlay) |
| `y` | approve armed | `gh pr review --approve` |
| `s` / `m` / `r` | merge armed | `gh pr merge --squash` / `--merge` / `--rebase` |
| `ctrl+s` | body editor | submit `gh pr review --comment` / `--request-changes` |
| `esc` | armed / editor | cancel back to browse (overlay stays open) |
| `esc` / `q` | browse | close overlay → graph |
| `ctrl+c` | in-flight | only key honored while a gh call runs |

`enter` opens the cursor PR; `l` opens the list. `--delete-branch` is
deliberately never passed to `gh pr merge`.

## Body editor (`c` / `r`)

`c` / `r` arm `prActionComment` / `prActionRequestChanges` — a
`bubbles/textarea` composed over the dimmed diff by the same
`renderModalBox` path as the approve / merge confirms (it just renders
an input instead of a y/n prompt). While armed, every key but `ctrl+s`
(submit) and `esc` (cancel) is forwarded to the textarea, so `a` / `m` /
`c` / `r` type literally rather than re-arming. `gh pr review
--comment` / `--request-changes` both require a body, so an empty
submit is rejected inline (`body required`) and a gh failure keeps the
editor open with the error inline — only a clean submit closes it. The
kind is derived from `prAction` (`reviewBodyKind`), not a parallel
field.

## Outcome routing

- **approve done** — overlay stays open (read on, or chain a merge);
  `approved #N` notice on the hint line, dismissed by the next key.
  Refreshes the PR list.
- **merge done** — overlay closes to the graph; `merged #N (<strategy>)`
  on the normal status line. Refreshes the PR list so the `#N` badge
  tracks the closed PR. No auto-fetch — the local graph reflects the
  merge only after the next `F` / `p`.
- **comment / request-changes done** — editor closes, overlay stays
  open; `commented on #N` / `requested changes on #N` notice on the hint
  line. Refreshes the PR list (the CI badge is unaffected, but the
  review state on GitHub now reflects it).
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
