#!/usr/bin/env python3
"""Color picker overlay for the current tab.

Launched via F6, displays an interactive color picker to change the
current tab's color override without opening the full dashboard.
"""

import curses
import json
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

# Flush cached cc_dashboard modules so Kitty's long-lived Python process
# always picks up the current source files without requiring a restart.
for _mod in list(sys.modules):
    if _mod.startswith("cc_dashboard"):
        del sys.modules[_mod]

from cc_dashboard import config as _cfg
from cc_dashboard.colors import CP_DIM, init_colors
from cc_dashboard.config import load_accounts, load_defaults
from cc_dashboard.config_data import PALETTE_NAMES
from cc_dashboard.drawing import safe_addstr
from cc_dashboard.remote.actions_core import do_set_color
from cc_dashboard.remote.command import get_kitty_socket, kitty_cmd
from cc_dashboard.remote.shared import _strip_icons


def _get_current_session(socket):
    """Look up the current tab's session info via KITTY_WINDOW_ID.

    The overlay kitten runs in a new window within the same tab, so
    KITTY_WINDOW_ID points to the overlay window (which has no user vars).
    We first locate the tab, then scan all windows in that tab for the
    cc_account user var to find the actual Claude session window.
    """
    win_id_str = os.environ.get("KITTY_WINDOW_ID", "")
    if not win_id_str:
        return None
    try:
        target_win_id = int(win_id_str)
    except ValueError:
        return None

    result = kitty_cmd(socket, "ls")
    if not result or result.returncode != 0:
        return None
    try:
        data = json.loads(result.stdout)
    except (json.JSONDecodeError, TypeError):
        return None

    # Find the tab containing the overlay window
    target_tab = None
    for os_win in data:
        for tab in os_win.get("tabs", []):
            for win in tab.get("windows", []):
                if win.get("id") == target_win_id:
                    target_tab = tab
                    break
            if target_tab:
                break
        if target_tab:
            break

    if target_tab is None:
        return None

    # Scan all windows in the tab for the one with cc_account user vars
    title = _strip_icons(target_tab.get("title", ""))
    for win in target_tab.get("windows", []):
        uv = win.get("user_vars", {})
        if uv.get("cc_account"):
            return {
                "tab_id": target_tab.get("id", 0),
                "title": title,
                "account": uv.get("cc_account", ""),
                "cc_color": uv.get("cc_color", ""),
                "cc_config_dir": uv.get("cc_config_dir", ""),
                "claude_window_id": win.get("id", 0),
            }

    return None


def _run(stdscr, session, account_color):
    """Curses event loop for the color picker overlay."""
    curses.curs_set(0)
    init_colors()
    stdscr.timeout(-1)

    from cc_dashboard.views_overlays_pickers import draw_color_picker

    current_color = session.get("cc_color") or account_color
    total = len(PALETTE_NAMES) + 1  # 10 colors + reset

    # Start with the current color selected
    selected = 0
    for i, name in enumerate(PALETTE_NAMES):
        if name == current_color:
            selected = i
            break

    while True:
        stdscr.erase()
        draw_color_picker(
            stdscr, session, PALETTE_NAMES, current_color, account_color, selected
        )
        stdscr.refresh()

        key = stdscr.getch()
        if key == -1:
            continue

        if key in (27, ord("q"), ord("Q")):
            return None

        if key in (curses.KEY_UP, ord("k"), ord("K")):
            selected = (selected - 1) % total
        elif key in (curses.KEY_DOWN, ord("j"), ord("J")):
            selected = (selected + 1) % total
        elif key in (10, 13, curses.KEY_ENTER):
            if selected < len(PALETTE_NAMES):
                chosen = PALETTE_NAMES[selected]
                # If chosen matches account default, treat as reset
                if chosen == account_color:
                    chosen = ""
            else:
                chosen = ""  # reset option
            return chosen


def main(args=None):
    load_accounts()
    load_defaults()

    socket = os.environ.get("KITTY_LISTEN_ON", "") or get_kitty_socket()
    session = _get_current_session(socket)
    if session is None:
        return ""

    account = session["account"]
    if not account:
        return ""

    account_color = _cfg._accounts.get(account, {}).get("color", "gray")

    result = curses.wrapper(_run, session, account_color)
    if result is not None:
        do_set_color(socket, session, result)

    return ""


if __name__ == "__main__":
    main()
