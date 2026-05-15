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

The footer shows a stable key palette (`space focus · e peek · r restart · x close · c config · C clear · o open dir · y copy title · l launch · / filter · p preview · ? help`); entries that don't apply to the cursor row dim instead of disappearing, so positions never shift. Press `?` for the full keymap.

#### Actions overlay

Press `enter` on a row to open a centered Actions overlay listing every per-row action next to its keybinding (focus tab, peek, restart, close, override account, clear icon, open dir, copy title). Press the action's key to run it; press `esc` or `q` to back out. Unavailable actions render dim — for example, "peek" is dim on non-active rows, and "clear icon" is dim unless the row carries an attention badge. The overlay exists for discoverability; direct shortcuts (`r`, `x`, `c`, `C`, `o`, `y`, …) keep working from the list without opening it.

#### Filter DSL

Press `/` to open the filter input, or `f` to open it pre-filled with `@` for an account match. The filter applies live as you type; `enter` keeps the filter, `esc` discards your in-progress edit and restores the prior filter. From the list, `esc` clears the active filter.

| Token            | Meaning                                                                            |
| ---------------- | ---------------------------------------------------------------------------------- |
| `word`           | Case-insensitive substring against title, directory, or account                    |
| `@account`       | Account match (case-insensitive)                                                   |
| `!state`         | State filter — accepts `active`, `idle`, `bolt`, `key`, `stop`, `error`            |
| `\@foo` / `\!x`  | Literal leading `@` or `!` in a substring token                                    |

Tokens are whitespace-separated and AND-combined. Examples:

- `api` — sessions whose title, directory, or account contains "api"
- `@work !key` — work-account sessions awaiting permission
- `api @work` — equivalent to "title contains api AND account is work"
- `!active` — every session in the Active group (bolt + recent empty-state rows)

##### Filter chips

While a filter is active, each component renders as a removable chip beneath the header — substring chips read `▌/api ✕`, account chips inherit the account's palette color (`▌@work ✕`), and attention-state chips render in orange (`▌!key ✕`).

| Key       | Effect                                                       |
| --------- | ------------------------------------------------------------ |
| `1`–`9`   | Remove the chip at that position (1 = leftmost)              |
| `tab`     | Cycle focus through the chips (focused chip is underlined)   |
| `enter`   | Remove the focused chip (falls through to focus tab if none) |
| `esc`     | Clear every chip and the filter                              |

### Session launch

New sessions are launched from the dashboard. The TUI reads your `config.toml` sessions list, lets you pick one or more, and creates the Kitty tabs.

Inside the picker, a sticky `/<buffer>█` prompt is always visible at the top — start typing to filter live (no `/` keystroke needed). The buffer accepts the same filter DSL as the list (`@account`, `!state`, substring), so `@work` narrows to a single account and `api` filters by substring. Backspace edits the buffer; `esc` clears a non-empty buffer (keeps the picker open), and a second `esc` exits the picker. Selections are keyed by the original session index, so toggled rows stay selected even if the filter hides them.

Picker shortcuts that stay live during filtering: `space` toggle, `enter` launch, `o` open dir, `p` preview, `j`/`k` (or arrows) navigate.

Press `p` to toggle the same preview pane the dashboard uses. In the picker the right pane shows the highlighted session's config (title, directory, account, flags, env, groups) and, if a session with the same title is currently running, a `── running ──` divider followed by the tail of its scrollback. The pane auto-disables when the terminal width drops below `defaults.preview.min_cols`. The `p` flag is shared with the dashboard — toggling it in either context applies everywhere.

#### Launch progress

When a multi-session launch is dispatched from the picker, a transient toast above the footer reports progress (`Launched 1/3 · W:api`, then `2/3 …`, finishing in green at `N/N`). Per-session failures push a separate red toast naming the failed session so successes keep counting. Toasts auto-expire after a few seconds.

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

Installed events: `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `SubagentStart`, `SubagentStop`, `PostToolUse`, `PostToolUseFailure`, `PermissionRequest`, `PermissionDenied`, `Stop`, `StopFailure`, `TaskCompleted`, `SessionEnd`, `Notification`, `PreCompact`, `PostCompact`, `Elicitation`, `ElicitationResult`. `StopFailure` covers turns that end abnormally (API error or context-limit stop); `PreCompact`/`PostCompact` bracket auto/manual context compaction; `Elicitation` and `PermissionDenied` extend the existing permission-request flow to MCP elicitations and auto-mode denials. Suppress per-account with `hooks.overrides.<account>.disabled_events`.

### Tab icons

The tab title is prefixed with a state icon driven by the most recent hook event:

| Icon | State          | Trigger                                                  |
| ---- | -------------- | -------------------------------------------------------- |
| ⚡   | active         | Claude is processing                                     |
| 🤖   | subagent       | A subagent / Task is running                             |
| 🌀   | compact        | Context compaction in progress                           |
| 🛑   | stop           | Turn or session ended cleanly                            |
| 🔑   | permission     | Approval or MCP elicitation needed                       |
| ❗   | failure        | Tool failure mid-turn OR terminal stop (API / context)   |

The ❗ icon is shared by transient tool failures (cleared by the next `active` event) and terminal stop-failures (terminal — survives until the next prompt that you yourself drive).

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
stop_failure = true       # fire macOS notification when a turn ends abnormally
                          # (API error or context-limit stop); opt out per-account
                          # via hooks.overrides.<account>.disabled_events = ["StopFailure"]

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
