# Kitty Remote Control Reference

Source: https://sw.kovidgoyal.net/kitty/remote-control/

## Enabling Remote Control

Remote control is enabled in `kitty.conf` with `allow_remote_control yes` or by launching Kitty with `-o allow_remote_control=yes`. For socket-based control from outside Kitty windows, start Kitty with `--listen-on unix:/tmp/mykitty` or set `listen_on` in `kitty.conf`. The environment variable `KITTY_LISTEN_ON` is automatically set inside Kitty windows and is used as the default socket when `--to` is not specified.

## Command Syntax

All commands use the form `kitten @ [--to SOCKET] COMMAND [OPTIONS]`. The older `kitty @` form still works but `kitten @` is preferred. Without `--to`, commands use `KITTY_LISTEN_ON`. Add `--no-response` to skip waiting for confirmation.

## Commands Used by cc-manager

### `kitten @ ls`

Returns a JSON tree of all OS windows, tabs, and windows. The structure is: top-level array of OS windows, each containing a `tabs` array, each tab containing a `windows` array. Each window object includes `id`, `title`, `cwd`, `pid`, `cmdline` (array of command-line arguments), and `user_vars` (dict of user variables set via `--var`). Each tab includes `id`, `title`, and `is_focused`.

### `kitten @ launch`

Creates new windows or tabs. Key options for this project:

- `--type=tab` creates a new tab
- `--title TITLE` sets the window title
- `--tab-title TITLE` sets the tab title
- `--cwd DIR` sets the working directory
- `--env NAME=VALUE` sets environment variables (repeatable)
- `--var NAME=VALUE` sets Kitty user variables (repeatable), retrievable later via `kitten @ ls` in the `user_vars` dict
- `--keep-focus` / `--dont-take-focus` keeps focus on the current window
- `--hold` keeps the window open after the command exits

### `kitten @ set-tab-color`

Sets tab bar colors for specific tabs. Color fields include `active_bg`, `active_fg`, `inactive_bg`, `inactive_fg`. Colors are specified as hex values (e.g., `active_bg=#ff0000`). Pass `none` to reset a field to defaults. Use `--match` to target specific tabs.

### `kitten @ close-tab`

Closes one or more tabs. Supports `--match` for targeting, `--self` to close the tab containing the command window, and `--ignore-no-match` to suppress errors when no tabs match.

### `kitten @ set-tab-title`

Changes a tab's title. Takes the new title as a positional argument. Supports `--match` and `--self`.

### `kitten @ focus-tab`

Switches focus to a specified tab. Supports `--match` for targeting.

## Match Expressions

The `--match` flag uses `field:query` syntax. For tab-level commands, available fields are: `id`, `index`, `title`, `window_id`, `window_title`, `pid`, `cwd`, `cmdline`, `env`, `var`, `state`, `session`, `recent`. Numeric fields (`id`, `index`, `pid`, `recent`) interpret queries as numbers. Text fields (`title`, `cmdline`, `cwd`) interpret queries as regular expressions. The special value `all` matches everything.

Combine expressions with Boolean operators: `and`, `or`, `not`, and parentheses. Example: `not state:focused and state:parent_focused`.

The `var` field matches user variables set via `--var` at launch time, which is how cc-manager identifies its managed tabs (`var:cc_account`).

## Security

For granular control, use `allow_remote_control password` with `remote_control_password` to restrict which commands are available. A custom `my_rc_auth.py` script can implement arbitrary authorization logic.
