# git-wrappers

`internal/git` is the only place that builds `*exec.Cmd`. Everything else calls typed wrappers.

## Rules

- **Typed wrappers only.** TUI calls `Log`, `Stat`, `Patch`, `PatchForFile`, `CommitDetail`, `Refs`, `Fetch`, etc. Stubbable in tests.
- **Stream large output.** Prefer `StdoutPipe` + scanner over `CombinedOutput` for anything that can be large (`git log`, `git diff`).
- **Wrap stderr into the returned error.** The TUI must be able to surface a real message, not `exit status 128`.
- **Parse machine output.** Use `--porcelain` / `-z` / `--format=...` whenever available. Don't scrape human-readable text. NUL separators in `--format=%H%x00%P%x00...` keep newline-bearing fields like commit bodies safe to split.
- **Decoration tokens (`%D`)** are parsed into typed `Ref` slices on each commit so the graph row can render branch/tag chips on the front of the subject.

## Why not go-git

We want `.gitconfig`, hooks, commit signing, and LFS to keep working with zero extra code. Shelling out inherits all of that for free.

## Sentinel errors

Stderr matching turns common git failures into typed sentinels so the TUI can branch on them:

- `ErrCheckoutNeedsCleanTree` — "Please commit your changes or stash them" / "would be overwritten" / "Your local changes". Drives the dirty-tree confirm flow ([checkout.md](checkout.md)).
- `ErrBranchAlreadyExists`, `ErrInvalidRefName`, `ErrBranchNotFullyMerged` — branch lifecycle ([branches.md](branches.md)).
- `ErrStashApplyConflict` — stash apply CONFLICT ([stash.md](stash.md)).

Add new sentinels when a TUI flow needs to react to a specific git failure mode; otherwise pass stderr through unchanged.
