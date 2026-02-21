# cc-manager-kitty

Opinionated Claude Code session manager across two accounts (work and personal) using Kitty terminal tabs with direct tab switching, color-coded accounts, and a status overview.

## Prerequisites

Kitty must be running with remote control enabled. Your `kitty.conf` should include:

```
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

```
include cc-manager.conf
```

Then reload Kitty config with `ctrl+shift+f5`.

This gives you `cmd+1` through `cmd+0` for tabs 1-10, `cmd+l` to toggle between the last two tabs, error marker highlighting on `f3`, broadcast input on `f1`, and `ctrl+shift+d` to open the interactive session dashboard.

### 3. Create your sessions config

```bash
mkdir -p ~/.config/cc-manager
cp config/sessions.example ~/.config/cc-manager/sessions.conf
```

Edit `~/.config/cc-manager/sessions.conf` to define your sessions. The format is pipe-delimited with three fields per line:

```
title | directory | account
```

The account field is either `work` (uses the default `claude` binary) or `personal` (runs `claude` with `CLAUDE_CONFIG_DIR=~/.claude-personal`). Tab titles are conventionally prefixed `W:` for work and `P:` for personal.

## Usage

### Launch all configured sessions

```bash
cc-launch
```

Reads `~/.config/cc-manager/sessions.conf` and creates a Kitty tab for each entry. Tabs are colored green for work accounts and blue for personal accounts. Running it again is safe since it skips tabs that already exist (idempotent).

### Add an ad-hoc session

```bash
cc-launch --add "W:hotfix" ~/projects/hotfix work
cc-launch --add "P:experiment" ~/scratch personal
```

The account argument defaults to `work` if omitted.

### Close a session

```bash
cc-launch --close "W:hotfix"
```

### List active Claude sessions

```bash
cc-launch --list
```

Shows a table of all Kitty tabs currently running Claude, with their index, title, account, directory, and focus status.

### Quick status overview

```bash
cc-status
```

Same idea as `cc-launch --list` but with dynamically sized columns for cleaner output.

### Interactive session dashboard

Press `ctrl+shift+d` to open the dashboard as a Kitty overlay. It shows all tabs running Claude Code in a navigable list, color-coded green for work accounts and blue for personal accounts.

Keybindings inside the dashboard:

```
↑ / k       Move selection up
↓ / j       Move selection down
Enter       Focus the selected tab (closes the dashboard)
x           Close the selected session (asks for confirmation)
n           Create a new session (opens a title/directory/account form)
r           Refresh the session list
q / Esc     Dismiss the dashboard
```

The new-session form accepts a title, working directory, and account type. Press Enter to advance between fields and Tab to toggle the account between work and personal. The new tab is created immediately with the appropriate Claude command and tab coloring.

### Save current sessions to config

```bash
cc-launch --save
```

Snapshots all currently running Claude sessions back to the config file (`~/.config/cc-manager/sessions.conf` by default). The existing file is backed up with a `.bak` extension before overwriting. You can also save to a specific file:

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

Override the default config file location with the `CC_MANAGER_CONFIG` environment variable:

```bash
CC_MANAGER_CONFIG=~/my-sessions.conf cc-launch
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

```
Makefile                        Install/uninstall symlinks
cc-manager.conf                 Kitty config snippet (include from kitty.conf)
bin/cc-launch                   Session launcher and manager
bin/cc-status                   Status overview tool
config/sessions.example         Example session definitions
sessions/example.conf           Example Kitty session file for startup
kittens/claude_dashboard.py     Interactive session picker (overlay kitten)
completions/_cc-launch          Zsh completion for cc-launch
completions/_cc-status          Zsh completion for cc-status
```
