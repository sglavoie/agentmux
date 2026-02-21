# Kitty Launch Command Reference

Source: https://sw.kovidgoyal.net/kitty/launch/

## Overview

The `launch` action creates new windows, tabs, or OS windows in Kitty. It can be used both in `kitty.conf` key mappings and via remote control (`kitten @ launch`).

## Window Types (`--type`)

- `window` (default): new Kitty window in the current tab
- `tab`: new tab in the current OS window
- `os-window`: new operating system window
- `overlay`: overlay covering the active window (used by kittens)
- `overlay-main`: overlay that functions as the primary window
- `background`: invisible process with no window

## Options Relevant to cc-manager

### Identity and Titles

`--title VALUE` sets the window title. `--tab-title VALUE` sets the tab title independently. Both accept `current` to copy from the source window.

### Working Directory

`--cwd DIR` sets the child process working directory. Special values: `current` (source window's directory), `last_reported` (requires shell integration), `oldest` (oldest foreground process directory), `root` (original directory when window was created).

### Environment and User Variables

`--env NAME=VALUE` sets environment variables in the launched process. Repeatable for multiple variables. Use `NAME=` for empty string or just `NAME` to unset.

`--var NAME=VALUE` sets Kitty user variables on the window. These are stored in Kitty's internal state (visible in `kitten @ ls` output under `user_vars`) and can be matched with `--match var:NAME=VALUE`. This is the mechanism cc-manager uses to tag tabs with `cc_account=NAME`.

### Focus Control

`--keep-focus` (or `--dont-take-focus`) prevents the new window/tab from stealing focus, keeping the user in the current window.

### Process Lifecycle

`--hold` keeps the window open at a shell prompt after the launched command exits, preventing the tab from disappearing if the process terminates.

### Permissions

`--allow-remote-control` enables the launched process to use `kitten @` commands. `--remote-control-password` restricts which remote control actions the process can perform.

## Special Placeholders

Command arguments support these placeholders: `@selection` (current selection), `@active-kitty-window-id` (active window ID), `@cursor-x` / `@cursor-y` (cursor position), `@line-count` (STDIN lines), `@scrolled-by` (scroll position).
