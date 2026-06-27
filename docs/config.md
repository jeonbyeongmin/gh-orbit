# config

XDG-conformant paths. `internal/config` owns resolution.

## Logging

- Path: `$XDG_STATE_HOME/gh-orbit/log`, falling back to `~/.local/state/gh-orbit/log` per XDG. **Not** `~/.gh-orbit/`.
- `internal/config` resolves the path and opens the file; the TUI's logger writes there.
- During development:

  ```bash
  tail -f ~/.local/state/gh-orbit/log
  ```

## User prefs

- Path: `$XDG_CONFIG_HOME/gh-orbit/config.toml`, fallback `~/.config/gh-orbit/config.toml`.
- File is optional — missing or empty means "use defaults".
- `internal/config.LoadPrefs` parses TOML; unknown keys are ignored.

Schema:

```toml
theme = "orbit-dark"   # any key from the table below

[pull]
strategy = "rebase"   # "ff-only" | "merge" | "rebase"
```

Top-level `theme` picks the app-wide color theme — it paints the diff, the commit
graph (lane palette + meta columns), ref chips / PR badges, the status line,
modal borders, and the page tabs. Unset / unrecognized falls back to
`orbit-dark`. The Settings dialog (`,`) cycles it with ←/→, applies it live, and
writes the choice back here via `SavePrefs` — which rewrites the whole file from
the known keys, so hand-written comments and unknown keys are dropped on save.

| dark | light |
| --- | --- |
| `orbit-dark` (default, signature) | `orbit-light` (signature) |
| `github-dark` | `github-light` |
| `nord` | `catppuccin-latte` |
| `gruvbox-dark` | `gruvbox-light` |
| `one-dark` | `rose-pine-dawn` |
| `tokyo-night` | |
| `rose-pine` | |
| `catppuccin-mocha` | |

`orbit-dark` / `orbit-light` carry the project's signature slate-purple accent
(`#7c6f9f`); `github-dark` matches the cockpit's pre-theming look; the rest are
muted, low-neon palettes.

> **Legacy `[diff] theme`**: earlier versions stored the theme under `[diff]
> theme`. It's still read when top-level `theme` is unset, and the first theme
> switch migrates it to the top-level key.

## Pull strategy resolution

`P` (global pull) and the `p` checkout+pull chain resolve the strategy in this order:

1. prefs `[pull] strategy`
2. git config `pull.rebase` (`true` → rebase)
3. fallback `--ff-only`

A pull conflict surfaces `pull: CONFLICT — resolve in Local Changes (C / abort)` in the status bar; the user stages resolutions on the Local Changes page and continues/aborts there ([sequencer.md](sequencer.md)), or resolves in their terminal.
