# Kitty Custom Kittens Reference

Source: https://sw.kovidgoyal.net/kitty/kittens/custom/

## Overview

Kittens are Python programs that run inside Kitty as overlay windows. They can present a TUI, interact with terminal content, and execute actions within the Kitty process after completion. The `claude_dashboard.py` in this project is a custom kitten.

## Required Entry Points

A kitten needs two functions:

`main(args: list[str]) -> str` runs in an overlay window. This is where the kitten's UI logic lives. The return value is passed to `handle_result()`. The `args` parameter receives arguments from the key mapping or command line.

`handle_result(args, result, target_window_id, boss)` runs inside the Kitty process after the overlay closes. The `boss` object provides access to Kitty internals. The `result` parameter is whatever `main()` returned (as a string). Use this to perform actions like pasting text, managing windows, or calling remote control commands.

## The Boss Object

Inside `handle_result()`, the `boss` object provides:

- `boss.window_id_map.get(target_window_id)` to retrieve the target window object
- `boss.active_tab` for the current tab
- `boss.call_remote_control()` to execute remote control commands with the same syntax as `kitten @`

Using `boss.call_remote_control()` is recommended over directly accessing undocumented internal APIs, since the remote control interface is stable.

## Receiving Screen Data

Decorate `handle_result` with `@result_handler(type_of_input='...')` to receive terminal content via STDIN. Options: `text` / `ansi` (current window), `history` / `ansi-history` (with scrollback), `output` (last command output, requires shell integration), `selection` (selected text).

## Headless Kittens

Use `@result_handler(no_ui=True)` to skip the overlay entirely. The kitten runs `handle_result` directly without showing any UI, useful for pure automation.

## Remote Control from main()

Decorate with `@kitten_ui(allow_remote_control=True)`, then call `main.remote_control([...])` to query Kitty state before or during UI presentation. This is how a kitten can call `ls` or other commands while its overlay is active.

## Running a Kitten

From `kitty.conf`: `map ctrl+k kitten mykitten.py arg1 arg2`. From the command line or remote control: `kitten @ launch --type=overlay kitten mykitten.py`. The working directory defaults to the active window's current directory.

## Debugging

`print()` output appears in the kitten's overlay window. For output visible in Kitty's own process logs, use `from kittens.tui.loop import debug`.

## Note on claude_dashboard.py

The dashboard kitten in this project does not use the standard `main()` / `handle_result()` pattern. Instead, it runs a full curses-based application and calls `kitten @` commands directly via `subprocess.run`. This is a valid approach for kittens that manage their own lifecycle and don't need to return data to the Kitty process.
