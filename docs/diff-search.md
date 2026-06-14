# Diff search (`/`)

Incremental search inside the patch overlay. `/` opens a query line; matches
highlight in the viewport and `n` / `N` jump between them. Scoped to the file
currently open in the overlay; clears on `esc`.

- Query state lives on `diffModel` so it survives hunk/file navigation.
- Highlight reuses the `@@` accent style rather than a new lipgloss color.
- No regex yet — literal substring match, case-insensitive.
