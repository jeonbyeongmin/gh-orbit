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

Schema (only field today):

```toml
[pull]
strategy = "rebase"   # "ff-only" | "merge" | "rebase"
```

## Pull strategy resolution

`P` (global pull) and the `p` checkout+pull chain resolve the strategy in this order:

1. prefs `[pull] strategy`
2. git config `pull.rebase` (`true` → rebase)
3. fallback `--ff-only`

A pull conflict surfaces `pull: CONFLICT — resolve in your terminal` in the status bar; the user resolves with their normal git workflow outside the TUI.
