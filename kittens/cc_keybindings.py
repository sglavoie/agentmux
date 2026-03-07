#!/usr/bin/env python3
"""Keybinding quick-reference overlay for cc-manager-kitty.

Displays all ccm-managed keybindings (Kitty-global and dashboard-internal)
in a scrollable full-screen curses overlay.  Dismiss with q, Escape, or
any non-scroll key.
"""

import curses
import os
import sys


def _kitten_dir():
    """Return the directory containing this kitten script."""
    for candidate in (
        globals().get("__file__"),
        sys.argv[0] if sys.argv else None,
    ):
        if candidate and os.path.isfile(candidate):
            return os.path.dirname(os.path.realpath(candidate))
    return os.path.expanduser("~/.config/kitty")


sys.path.insert(0, _kitten_dir())

for _mod in list(sys.modules):
    if _mod.startswith("cc_dashboard"):
        del sys.modules[_mod]

from cc_dashboard.colors import (
    CP_DIM,
    CP_HEADER,
    CP_NORMAL,
    init_colors,
)
from cc_dashboard.config import load_accounts
from cc_dashboard.drawing import safe_addstr

_SECTIONS = [
    (
        "Kitty Global",
        [
            ("F1", "Keybinding reference (this overlay)"),
            ("F2", "Toggle star / pin on current tab"),
            ("F3", "Toggle error / warning markers"),
            ("F4", "Clear markers and state icons"),
            ("F5", "Toggle flag on current tab"),
            ("F6", "Set tab color (color picker overlay)"),
            ("F7", "Set tab status (status overlay)"),
            ("F8", "Session info overlay"),
            ("F12", "Broadcast input to all tab windows"),
            ("Cmd+1..0", "Switch to tab 1-10"),
            ("Cmd+L", "Toggle between last two tabs"),
            ("Ctrl+Shift+D", "Open session dashboard"),
            ("Ctrl+Shift+P", "Quick-switch overlay"),
        ],
    ),
    (
        "Dashboard — Navigation",
        [
            ("k / Up", "Move selection up"),
            ("j / Down", "Move selection down"),
            ("Enter", "Focus & switch to selected tab"),
            ("q", "Quit dashboard"),
        ],
    ),
    (
        "Dashboard — Session Actions",
        [
            ("t", "Open actions menu for selected/marked sessions"),
            (
                "Menu: Restart / Close / Move",
                "Available for marked sessions (when marks exist)",
            ),
            (
                "Menu: Restart opts / Clone / Override / Rename / Worktree",
                "Single-session only",
            ),
            ("Enter", "Apply selected action in the actions menu"),
            ("Esc", "Back from the actions menu"),
            ("n", "Open new session form"),
            ("l", "Launch session from config"),
            ("e", "Edit selected session config entry"),
            ("D", "Delete session from config"),
            ("S", "Save running sessions to config"),
            ("p", "Peek at selected session output"),
        ],
    ),
    (
        "Dashboard — Multi-Select",
        [
            ("Space", "Mark / unmark session"),
            ("U", "Clear all marks"),
            ("t", "Actions apply to marked sessions when marks exist"),
        ],
    ),
    (
        "Dashboard — View",
        [
            ("d", "Toggle detail pane"),
            ("a", "Toggle all / managed tabs"),
            ("Ctrl+r", "Refresh session list"),
        ],
    ),
    (
        "Dashboard — Search",
        [
            ("/", "Start search"),
            ("Enter", "Accept search query"),
            ("Esc", "Cancel search"),
        ],
    ),
    (
        "Dashboard — Account Filter",
        [
            ("f", "Enter / exit filter mode"),
            ("1-9", "Toggle account visibility"),
            ("Esc", "Clear filter"),
        ],
    ),
]

# Pre-build content lines: (kind, text1, text2)
_LINES = []
for _sect_name, _bindings in _SECTIONS:
    _LINES.append(("header", _sect_name, ""))
    for _key, _desc in _bindings:
        _LINES.append(("binding", _key, _desc))
    _LINES.append(("blank", "", ""))
if _LINES and _LINES[-1][0] == "blank":
    _LINES.pop()


def _draw(stdscr, scroll):
    """Render the keybinding reference overlay."""
    stdscr.erase()
    rows, cols = stdscr.getmaxyx()
    header_attr = curses.color_pair(CP_HEADER) | curses.A_BOLD
    normal_attr = curses.color_pair(CP_NORMAL)
    dim_attr = curses.color_pair(CP_DIM)

    key_col = 4
    desc_col = 20

    safe_addstr(stdscr, 0, 2, "Keybinding Reference", header_attr)

    content_height = rows - 3
    visible = _LINES[scroll : scroll + content_height]

    for i, (kind, text1, text2) in enumerate(visible):
        y = i + 1
        if kind == "header":
            safe_addstr(stdscr, y, 2, text1, header_attr)
        elif kind == "binding":
            safe_addstr(stdscr, y, key_col, text1, normal_attr)
            safe_addstr(stdscr, y, desc_col, text2[: cols - desc_col - 1], dim_attr)

    if len(_LINES) > content_height:
        end = min(scroll + content_height, len(_LINES))
        pos = f"[{scroll + 1}-{end}/{len(_LINES)}]"
        safe_addstr(stdscr, rows - 2, 2, f"j/k to scroll  {pos}", dim_attr)

    safe_addstr(stdscr, rows - 1, 2, "Press any key to close", dim_attr)


def _run(stdscr):
    """Event loop for the keybinding overlay."""
    curses.set_escdelay(25)
    curses.curs_set(0)
    init_colors()
    stdscr.timeout(-1)

    scroll = 0
    max_scroll = max(0, len(_LINES) - (stdscr.getmaxyx()[0] - 3))

    while True:
        _draw(stdscr, scroll)
        stdscr.refresh()

        key = stdscr.getch()
        if key == -1:
            continue

        # Scroll down
        if key in (ord("j"), curses.KEY_DOWN):
            if scroll < max_scroll:
                scroll += 1
            continue

        # Scroll up
        if key in (ord("k"), curses.KEY_UP):
            if scroll > 0:
                scroll -= 1
            continue

        # Any other key dismisses the overlay
        break


def main(args=None):
    load_accounts()
    curses.wrapper(_run)


if __name__ == "__main__":
    main()
