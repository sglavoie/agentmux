# cc-manager-kitty

Opinionated Claude Code session manager with support for multiple accounts using Kitty terminal tabs with direct tab switching, color-coded accounts, and a status overview.

## Prerequisites

Kitty must be running with remote control enabled. Your `kitty.conf` should include:

```plaintext
allow_remote_control yes
listen_on unix:/tmp/kitty-{kitty_pid}
```

You also need `jq` installed (`brew install jq` on macOS) and Python 3.13+ (`brew install python` on macOS, or use your preferred Python version manager).

## Setup

### 1. Install

Clone the repo and run `just install`:

```bash
git clone <repo-url> ~/path/to/cc-manager-kitty
cd ~/path/to/cc-manager-kitty
just install
```

This creates symlinks for the binaries into `~/.local/bin/`, the Kitty kitten and config into `~/.config/kitty/`, and the Zsh completions into `~/.zfunc/`. Running it again is safe (idempotent). To remove everything, run `just uninstall`.

### 2. Include the Kitty config snippet

Add this line to your `kitty.conf`:

```plaintext
include cc-manager.conf
```

Then reload Kitty config with `ctrl+shift+f5`.

This gives you `cmd+1` through `cmd+0` for tabs 1-10, `cmd+l` to toggle between the last two tabs, error marker highlighting on `f3`, a keybinding quick-reference on `f1`, broadcast input on `f12`, and `ctrl+shift+d` to open the interactive session dashboard.

### 3. Define your accounts

```bash
mkdir -p ~/.config/cc-manager
cp config/accounts.example.toml ~/.config/cc-manager/accounts.toml
```

Edit `~/.config/cc-manager/accounts.toml` to define your Claude accounts. Each `[[accounts]]` entry has three keys:

- `name` — Identifier you reference from `sessions.toml`
- `config_dir` — Path to the Claude config directory (`~/.claude` = default)
- `color` — Tab color from the built-in palette: green, blue, red, orange, purple, teal, pink, yellow, gray, or cyan

Example with three accounts:

```toml
[[accounts]]
name = "work"
config_dir = "~/.claude"
color = "green"

[[accounts]]
name = "personal"
config_dir = "~/.claude-personal"
color = "blue"

[[accounts]]
name = "client-x"
config_dir = "~/.claude-client-x"
color = "orange"
```

If `accounts.toml` does not exist, cc-manager falls back to two default accounts: `work` (green, default config) and `personal` (blue, `~/.claude-personal`).

### 4. Create your sessions config

```bash
cp config/sessions.example.toml ~/.config/cc-manager/sessions.toml
```

Edit `~/.config/cc-manager/sessions.toml` to define your sessions. Each `[[sessions]]` entry has three required keys and several optional ones:

- `title` — Tab title (convention: prefix with account initial, e.g. `W:` for work, `P:` for personal)
- `directory` — Working directory (`~` is expanded)
- `account` — Account name as defined in `accounts.toml`
- `flags` — *(optional)* Per-session Claude flags, overriding the global default (see step 5)
- `project_window` — *(optional)* Per-session layout override (`stack`, `vsplit`, `hsplit`, `split`, or `-` to disable)
- `env` — *(optional)* Space-separated `KEY=VALUE` pairs injected as environment variables
- `group` — *(optional)* Group name for organizing related sessions in the dashboard

Example:

```toml
[[sessions]]
title = "W:project-alpha"
directory = "~/projects/alpha"
account = "work"

[[sessions]]
title = "P:dotfiles"
directory = "~/dotfiles"
account = "personal"
flags = "--dangerously-skip-permissions"
```

### 5. (Optional) Set global defaults

If you want to pass flags like `--dangerously-skip-permissions` to every Claude session by default, create a defaults file:

```bash
cp config/defaults.example.toml ~/.config/cc-manager/defaults.toml
```

Edit `~/.config/cc-manager/defaults.toml` and uncomment the `claude_flags` line:

```toml
[defaults]
claude_flags = "--dangerously-skip-permissions"
```

This applies to all sessions unless overridden per-session in `sessions.toml`. Use the `flags` key in a session entry to override, or set it to `"-"` to disable flags entirely for that session:

```toml
# Inherits global defaults
[[sessions]]
title = "W:project-alpha"
directory = "~/projects/alpha"
account = "work"

# Explicit flags regardless of global default
[[sessions]]
title = "W:project-beta"
directory = "~/projects/beta"
account = "work"
flags = "--dangerously-skip-permissions"

# No flags even if global default is set
[[sessions]]
title = "W:safe-project"
directory = "~/projects/safe"
account = "work"
flags = "-"
```

There is no merging; the per-session value completely replaces the global default.

## Usage

### The `ccm` command

`ccm` is the unified CLI entry point. All functionality is available through subcommands:

| Command | Description |
|---------|-------------|
| `ccm launch [args]` | Manage and launch sessions (delegates to `cc-launch`) |
| `ccm status [args]` | Show active session status (delegates to `cc-status`) |
| `ccm star` | Toggle star/pin on active tab |
| `ccm config <subcmd>` | Manage config files (`edit`, `show`, `path`, `init`, `sync`, `validate`) |
| `ccm hooks <subcmd>` | Manage hooks (`sync`, `status`) |
| `ccm logs <subcmd>` | Query and manage activity logs (`list`, `show`, `summary`, `clean`, `dir`) |
| `ccm dashboard` | Launch the curses dashboard |
| `ccm switch` | Quick-switch: fuzzy-find and jump to a session |
| `ccm notify` | Send a test notification (requires `alerter`) |
| `ccm audit` | Check upstream Claude Code feature compatibility |
| `ccm idle-watch` | Manage background idle notification watcher |
| `ccm help [cmd]` | Show help for any command |

Run `ccm help <command>` for details on any subcommand. The `cc-launch` and `cc-status` commands also work directly.

### Launch all configured sessions

```bash
cc-launch
```

Reads `~/.config/cc-manager/sessions.toml` and creates a Kitty tab for each entry. Tabs are colored according to the palette color assigned to each account in `accounts.toml`. Running it again is safe since it skips tabs that already exist (idempotent).

### Add an ad-hoc session

```bash
cc-launch --add "W:hotfix" ~/projects/hotfix work
cc-launch --add "P:experiment" ~/scratch personal
```

The account argument defaults to `work` if omitted.

### Close a session

```bash
cc-launch --close --instance K:12345 --tab-id 67
```

Destructive operations now use stable Kitty selectors to avoid title-collision mistakes.

### List active Claude sessions

```bash
cc-launch --list
```

Shows a table of all Kitty tabs currently running Claude, with index, tab id, title, account, instance, directory, and focus status.

Use the `Instance` + `TabID` columns as inputs to destructive commands.

### Restart, clone, or move a session safely

```bash
cc-launch --restart --instance K:12345 --tab-id 67
cc-launch --clone --instance K:12345 --tab-id 67
cc-launch --move --instance K:12345 --tab-id 67 --target-instance K:67890
```

### Launch or close by group

```bash
cc-launch --all --group backend
cc-launch --close --group backend
```

Launches or closes only sessions belonging to the named group (as defined by the `group` key in `sessions.toml`).

### Quick status overview

```bash
cc-status
```

Same idea as `cc-launch --list` but with dynamically sized columns for cleaner output.

Use `--json` for machine-readable output:

```bash
cc-status --json
```

### Interactive session dashboard

Press `ctrl+shift+d` to open the dashboard as a Kitty overlay. It shows all tabs running Claude Code in a navigable list, color-coded by account using the palette from `accounts.toml`.

Use `F1` for the complete keybinding reference. Core dashboard controls:

```plaintext
↑ / k       Move selection up
↓ / j       Move selection down
Enter       Focus the selected tab (closes the dashboard)
t           Open session actions menu (selected tab or all marked tabs)
Space       Mark / unmark a session for grouped actions
U           Clear all marks
n           Create a new session (opens a title/directory/account form)
Ctrl+r      Refresh the session list
s           Toggle sort order (config order / alphabetical / by group)
F2          Star/pin the selected session (pinned tabs sort to front)
g           Filter by group (in pick mode)
G           Group actions overlay (launch/close entire groups)
H           Open the hooks manager
q / Esc     Dismiss the dashboard
```

The new-session form accepts a title, working directory, account type, and optional Claude flags. Press Enter to advance between fields and Tab to cycle through the available accounts. Leave the flags field empty to inherit the global default from `defaults.toml`, or enter `-` to disable flags for that session. The new tab is created immediately with the appropriate Claude command and tab coloring.

### Save current sessions to config

```bash
cc-launch --save
```

Snapshots all currently running Claude sessions back to `~/.config/cc-manager/sessions.toml` (by default). The existing file is backed up with a `.bak` extension before overwriting. Note that per-session flag overrides are not preserved by `--save` since live Kitty tab state has no knowledge of what flags were used at launch time. You can also save to a specific file:

```bash
cc-launch --save ~/my-snapshot.toml
```

### Clean up stale sessions

```bash
cc-launch --cleanup
```

Finds tabs whose title follows the `W:`/`P:` naming convention but no longer have a Claude process running (the session exited or crashed). Lists the stale tabs and asks for confirmation before closing them.

### Multiple Kitty instances

When running multiple Kitty windows, auto-detection picks the most recently created socket. To target a specific instance, pass `--socket` to either command:

```bash
cc-launch --socket unix:/tmp/kitty-12345 --list
cc-status --socket unix:/tmp/kitty-12345
```

To see all available sockets and which ones are responsive:

```bash
cc-launch --sockets
```

### Environment variables

| Variable | Description |
|---|---|
| `CC_MANAGER_CONFIG` | Override config directory (default: `~/.config/cc-manager/`) |
| `CC_MANAGER_ACCOUNTS` | Override accounts config file path |
| `CC_MANAGER_DEFAULTS` | Override defaults config file path |
| `CCM_DEBUG` | Set to `1` to enable debug logging to stderr |
| `CCM_DEBUG_LOG` | Path to a file for debug log output (used by the dashboard) |
| `KITTY_LISTEN_ON` | Kitty sets this automatically; used for socket discovery |

### Session file for Kitty startup

If you want sessions to launch automatically when Kitty starts, use a Kitty session file:

```bash
kitty --session sessions/example.conf
```

Copy `sessions/example.conf` and customize it for your projects. This approach is independent of `cc-launch` and uses Kitty's native session format.

### Utility scripts

These are typically invoked via Kitty keybindings (defined in `cc-manager.conf`) rather than directly:

- `cc-toggle-star` — Toggle the star/pin icon on the active tab (bound to F2)
- `cc-toggle-flag` — Toggle a visual flag marker on the active tab
- `cc-idle-watch` — Background daemon that monitors sessions and sends idle notifications
- `cc-activity-log` — Hook script that logs session activity events to JSONL files
- `cc-audit` — Check which upstream Claude Code features your version supports

## Configuration

All config files live in `~/.config/cc-manager/` by default. Override their locations with environment variables:

```bash
CC_MANAGER_CONFIG=~/my-sessions.toml cc-launch
CC_MANAGER_ACCOUNTS=~/my-accounts.toml cc-launch
CC_MANAGER_DEFAULTS=~/my-defaults.toml cc-launch
```

The Kitty socket is auto-detected from `KITTY_LISTEN_ON` or by finding the most recent `/tmp/kitty-*` socket. You can also override it per-command with `--socket` (see "Multiple Kitty instances" above).

## Shell completions

Zsh completions for both `cc-launch` and `cc-status` are installed to `~/.zfunc/` by `just install`. Make sure your `~/.zshrc` includes `~/.zfunc` in `fpath` and calls `compinit`:

```bash
fpath=(~/.zfunc $fpath)
autoload -Uz compinit && compinit
```

This enables tab completion for all subcommands, tab titles (for `--close`), account types, socket paths, and file arguments.

## File layout

```plaintext
Justfile                        Install/uninstall symlinks
cc-manager.conf                 Kitty config snippet (include from kitty.conf)
bin/ccm                         Unified CLI entry point
bin/cc-launch                   Session launcher and manager
bin/cc-status                   Status overview tool
bin/cc-audit                    Upstream feature compatibility checker
bin/cc-idle-watch               Background idle notification watcher
bin/cc-toggle-star              Tab star/pin toggle (Kitty keybinding)
bin/cc-toggle-flag              Tab flag marker toggle (Kitty keybinding)
lib/cc-common.sh                Shared Bash library (sourced by cc-launch, cc-status)
lib/cc-common/                  Modular Bash library components
lib/cc-launch/                  cc-launch subcommand implementations
lib/ccm/                        ccm subcommand implementations
lib/toml_reader.py              TOML reader (stdlib tomllib)
lib/toml_writer.py              TOML serializer (no third-party deps)
lib/toml-helper.py              CLI bridge for Bash scripts (TSV output)
lib/config_validator.py         Config file validation
data/palette.json               Color palette definitions (10 named colors)
data/icons.json                 Icon and state indicator definitions
config/accounts.example.toml    Example account definitions
config/sessions.example.toml    Example session definitions
config/defaults.example.toml    Example global defaults
config/hooks.example.json       Example hooks configuration
sessions/example.conf           Example Kitty session file for startup
kittens/claude_dashboard.py     Dashboard entry point (Kitty kitten)
kittens/cc_dashboard/           Dashboard implementation modules
completions/_ccm                Zsh completion for ccm
completions/_cc-launch          Zsh completion for cc-launch
completions/_cc-status          Zsh completion for cc-status
tests/                          Test suite
docs/                           Kitty documentation reference
```
