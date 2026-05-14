# cc-manager-kitty

Claude Code session manager for the Kitty terminal. Manages multiple Claude Code sessions as Kitty tabs with color-coded accounts and an interactive dashboard.

## Prerequisites

- Go 1.22+ (to build)
- Kitty with remote control enabled
- [`alerter`](https://github.com/vjeantet/alerter) for macOS notifications (optional)

Your `kitty.conf` must include:

```plaintext
allow_remote_control yes
listen_on unix:/tmp/kitty-{kitty_pid}
```

## Install

```bash
git clone <repo-url> ~/path/to/cc-manager-kitty
cd ~/path/to/cc-manager-kitty
just install
```

This builds the `ccm` binary, copies it to `~/.local/bin/ccm`, and symlinks `cc-manager.conf` to `~/.config/kitty/`. Running again is safe (idempotent). To remove everything: `just uninstall`.

Then add to your `kitty.conf`:

```plaintext
include cc-manager.conf
```

Reload with `ctrl+shift+f5`. This gives you `cmd+1`–`cmd+0` for tab switching, `cmd+l` to toggle last two tabs, and `ctrl+shift+d` to open the dashboard.

## Setup

Create a config file:

```bash
ccm config init
```

This writes an example `~/.config/cc-manager/config.toml`. Edit it to define your accounts and sessions:

```toml
[defaults]
sort = "alpha"

[defaults.notifications]
permission = true
stop = true

[[accounts]]
name = "work"
config_dir = "~/.config/claude-work"
color = "blue"

[[accounts]]
name = "personal"
config_dir = "~/.config/claude"
color = "green"

[[sessions]]
title = "W:api"
directory = "~/projects/api"
account = "work"

[[sessions]]
title = "P:dotfiles"
directory = "~"
account = "personal"
```

**Colors**: green, blue, red, orange, purple, teal, pink, yellow, gray, cyan

After editing, validate with `ccm config validate`.

## Usage

### Dashboard

```bash
ccm
```

Launches the interactive Bubble Tea dashboard. If a dashboard is already open, focuses it instead. Use `ctrl+shift+d` from inside Kitty to open it as an overlay.

Footer hints adapt to the selected row's state. Press `?` for the full keymap.

### Session launch

New sessions are launched from the dashboard. The TUI reads your `config.toml` sessions list, lets you pick one or more, and creates the Kitty tabs. Inside the picker, press `/` to filter the list by title or account (case-insensitive substring); `esc` clears an active query, and a second `esc` exits the picker.

### Config management

| Command                   | Description                                          |
| ------------------------- | ---------------------------------------------------- |
| `ccm config init`         | Create example config                                |
| `ccm config edit`         | Open config in `$EDITOR`                             |
| `ccm config show`         | Print current config                                 |
| `ccm config path`         | Print config file path                               |
| `ccm config validate`     | Validate config                                      |
| `ccm config hooks-sync`   | Install hooks into all account `settings.json` files |
| `ccm config hooks-status` | Show per-account hook sync status                    |

### Daemon

The daemon manages tab icon state and macOS notifications:

```bash
ccm daemon start     # foreground, blocks the tab (useful with --debug)
ccm daemon restart   # stop + relaunch detached, returns immediately
ccm daemon status
ccm daemon stop
```

The daemon starts automatically when the first hook event arrives. It communicates via a Unix socket at `~/.local/share/cc-manager/daemon.sock`.

### Hooks

Install hooks for all accounts:

```bash
ccm config hooks-sync
```

This writes `ccm hook-indicator <event>` entries into each account's Claude Code `settings.json`. Check drift with `ccm config hooks-status`.

## Config reference

```toml
[defaults]
sort = "alpha"           # "alpha" or "account"
refresh_interval = 2     # TUI refresh seconds
guard_duration = 1       # state-guard seconds
idle_threshold = 300     # seconds of inactivity before a session is grouped as Idle
claude_flags = []        # global Claude flags for all sessions

[defaults.notifications]
error = false
permission = true
stop = true

[defaults.preview]
direction = "horizontal" # "horizontal" | "vertical"
ratio = 0.5              # fraction of width (horizontal) or height (vertical) given to preview
refresh_ms = 500         # tick interval while preview is open
min_cols = 100           # below this, horizontal split is disabled
min_rows = 30            # below this, vertical split is disabled

[[accounts]]
name = "work"            # required
config_dir = "~/.config/claude-work"  # required, tilde-expanded
color = "blue"           # required

[[sessions]]
title = "W:api"          # required (tab-initial:name convention)
directory = "~/projects/api"  # required
account = "work"         # required, must match an account name
claude_flags = []        # optional; omit to inherit defaults
env = {}                 # optional; key=value env vars injected at launch
groups = []              # optional; for filtering in dashboard

[hooks.overrides.work]
disabled_events = ["Stop"]   # suppress events for this account
```

## Troubleshooting

**Tabs not colored** — Verify `color` values match the palette names (green, blue, red, orange, purple, teal, pink, yellow, gray, cyan).

**No sessions found** — Run `ccm config path` to confirm the config path. Run `ccm config validate` to check for errors.

**Hooks not running** — Run `ccm config hooks-status`. If drifted or missing, run `ccm config hooks-sync`.

**Socket not found** — Confirm `kitty.conf` has `allow_remote_control yes` and `listen_on unix:/tmp/kitty-{kitty_pid}`. Restart Kitty after adding.

**Debug output** — Run with `--debug` or set `CCM_DEBUG=1`.
