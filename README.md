# cc-manager-kitty

Opinionated Claude Code session manager with support for multiple accounts using Kitty terminal tabs with direct tab switching, color-coded accounts, and a status overview.

## Prerequisites

Kitty must be running with remote control enabled. Your `kitty.conf` should include:

```plaintext
allow_remote_control yes
listen_on unix:/tmp/kitty-{kitty_pid}
```

You also need `jq` installed (`brew install jq` on macOS).

## Setup

### 1. Install

Clone the repo and run `make install`:

```bash
git clone <repo-url> ~/path/to/cc-manager-kitty
cd ~/path/to/cc-manager-kitty
make install
```

This creates symlinks for the binaries into `~/.local/bin/`, the Kitty kitten and config into `~/.config/kitty/`, and the Zsh completions into `~/.zfunc/`. Running it again is safe (idempotent). To remove everything, run `make uninstall`.

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
cp config/accounts.example ~/.config/cc-manager/accounts.conf
```

Edit `~/.config/cc-manager/accounts.conf` to define your Claude accounts. Each line has three pipe-delimited fields:

```plaintext
name | claude_config_dir | color
```

The `name` is an identifier you reference from `sessions.conf`. The `claude_config_dir` is the path to the Claude config directory for that account (empty or `~/.claude` means the default). The `color` is a name from the built-in palette: green, blue, red, orange, purple, teal, pink, yellow, gray, or cyan.

Example with three accounts:

```plaintext
work     | ~/.claude            | green
personal | ~/.claude-personal   | blue
client-x | ~/.claude-client-x   | orange
```

If `accounts.conf` does not exist, cc-manager falls back to two default accounts: `work` (green, default config) and `personal` (blue, `~/.claude-personal`).

### 4. Create your sessions config

```bash
cp config/sessions.example ~/.config/cc-manager/sessions.conf
```

Edit `~/.config/cc-manager/sessions.conf` to define your sessions. The format is pipe-delimited:

```plaintext
title | directory | account [| claude_flags]
```

The `account` field references an entry from `accounts.conf`. Tab titles are conventionally prefixed with an initial for the account (e.g. `W:` for work, `P:` for personal). The optional 4th column lets you override the global Claude flags for that specific session (see step 5 below).

### 5. (Optional) Set global Claude flags

If you want to pass flags like `--dangerously-skip-permissions` to every Claude session by default, create a defaults file:

```bash
cp config/defaults.example ~/.config/cc-manager/defaults.conf
```

Edit `~/.config/cc-manager/defaults.conf` and uncomment the `claude_flags` line:

```plaintext
claude_flags = --dangerously-skip-permissions
```

This applies to all sessions unless overridden per-session in `sessions.conf`. To override the global flags for a specific session, add a 4th column to that session's line. Use `-` to disable flags entirely for one session:

```plaintext
W:project-alpha  | ~/projects/alpha  | work
W:project-beta   | ~/projects/beta   | work  | --dangerously-skip-permissions
W:safe-project   | ~/projects/safe   | work  | -
```

In this example, `project-alpha` inherits whatever is in `defaults.conf`, `project-beta` always gets `--dangerously-skip-permissions` regardless of the global default, and `safe-project` never gets any flags even if the global default is set. There is no merging; the per-session value completely replaces the global default.

## Usage

### Launch all configured sessions

```bash
cc-launch
```

Reads `~/.config/cc-manager/sessions.conf` and creates a Kitty tab for each entry. Tabs are colored according to the palette color assigned to each account in `accounts.conf`. Running it again is safe since it skips tabs that already exist (idempotent).

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

### Quick status overview

```bash
cc-status
```

Same idea as `cc-launch --list` but with dynamically sized columns for cleaner output.

### Interactive session dashboard

Press `ctrl+shift+d` to open the dashboard as a Kitty overlay. It shows all tabs running Claude Code in a navigable list, color-coded by account using the palette from `accounts.conf`.

Keybindings inside the dashboard:

```plaintext
↑ / k       Move selection up
↓ / j       Move selection down
Enter       Focus the selected tab (closes the dashboard)
t           Open session actions menu for selected tab
x           Close the selected session (asks for confirmation)
R           Restart the selected session (asks for confirmation)
m           Move selected session to another Kitty instance
n           Create a new session (opens a title/directory/account form)
Ctrl+r      Refresh the session list
q / Esc     Dismiss the dashboard
```

The new-session form accepts a title, working directory, account type, and optional Claude flags. Press Enter to advance between fields and Tab to cycle through the available accounts. Leave the flags field empty to inherit the global default from `defaults.conf`, or enter `-` to disable flags for that session. The new tab is created immediately with the appropriate Claude command and tab coloring.

### Save current sessions to config

```bash
cc-launch --save
```

Snapshots all currently running Claude sessions back to the config file (`~/.config/cc-manager/sessions.conf` by default). The existing file is backed up with a `.bak` extension before overwriting. Note that per-session flag overrides (the 4th column) are not preserved by `--save` since live Kitty tab state has no knowledge of what flags were used at launch time. You can also save to a specific file:

```bash
cc-launch --save ~/my-snapshot.conf
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

### Session file for Kitty startup

If you want sessions to launch automatically when Kitty starts, use a Kitty session file:

```bash
kitty --session sessions/example.conf
```

Copy `sessions/example.conf` and customize it for your projects. This approach is independent of `cc-launch` and uses Kitty's native session format.

## Configuration

All config files live in `~/.config/cc-manager/` by default. Override their locations with environment variables:

```bash
CC_MANAGER_CONFIG=~/my-sessions.conf cc-launch
CC_MANAGER_ACCOUNTS=~/my-accounts.conf cc-launch
CC_MANAGER_DEFAULTS=~/my-defaults.conf cc-launch
```

The Kitty socket is auto-detected from `KITTY_LISTEN_ON` or by finding the most recent `/tmp/kitty-*` socket. You can also override it per-command with `--socket` (see "Multiple Kitty instances" above).

## Shell completions

Zsh completions for both `cc-launch` and `cc-status` are installed to `~/.zfunc/` by `make install`. Make sure your `~/.zshrc` includes `~/.zfunc` in `fpath` and calls `compinit`:

```bash
fpath=(~/.zfunc $fpath)
autoload -Uz compinit && compinit
```

This enables tab completion for all subcommands, tab titles (for `--close`), account types, socket paths, and file arguments.

## File layout

```plaintext
Makefile                        Install/uninstall symlinks
cc-manager.conf                 Kitty config snippet (include from kitty.conf)
bin/cc-launch                   Session launcher and manager
bin/cc-status                   Status overview tool
config/accounts.example         Example account definitions (colors, config dirs)
config/sessions.example         Example session definitions (with optional flags column)
config/defaults.example         Example global defaults (claude_flags)
sessions/example.conf           Example Kitty session file for startup
kittens/claude_dashboard.py     Interactive session picker (overlay kitten)
completions/_cc-launch          Zsh completion for cc-launch
completions/_cc-status          Zsh completion for cc-status
```
