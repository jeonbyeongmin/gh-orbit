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
[pull]
strategy = "rebase"   # "ff-only" | "merge" | "rebase"

[diff]
theme = "github-dark"   # "github-dark" | "catppuccin-mocha" | "catppuccin-latte" | "github-light"
```

`[diff] theme` picks the diff color theme — two dark (`github-dark`, the
default; `catppuccin-mocha`) and two light (`catppuccin-latte`, `github-light`).
Unset / unrecognized falls back to `github-dark`. The Settings dialog (`,`)
cycles it with ←/→, applies it live, and writes the choice back here via
`SavePrefs` — which rewrites the whole file from the known keys, so hand-written
comments and unknown keys are dropped on save.

## Pull strategy resolution

`P` (global pull) and the `p` checkout+pull chain resolve the strategy in this order:

1. prefs `[pull] strategy`
2. git config `pull.rebase` (`true` → rebase)
3. fallback `--ff-only`

A pull conflict surfaces `pull: CONFLICT — resolve in Local Changes (C / abort)` in the status bar; the user stages resolutions on the Local Changes page and continues/aborts there ([sequencer.md](sequencer.md)), or resolves in their terminal.
