# Blame gutter (`B`)

Toggle a per-line author + relative-date gutter in the patch overlay, sourced
from `git blame --porcelain` for the open file. Cached per (commit, file);
cleared when the overlay closes.

- Gutter width derives from the longest author initial set, capped at 12 cols.
- Renders left of the diff body; the `[N/M]` footer is unaffected.
