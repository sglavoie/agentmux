#!/usr/bin/env python3
"""Claude Code Dashboard — interactive session picker kitten for Kitty.

Launch via the kitten action in kitty.conf:
    map ctrl+shift+d kitten /path/to/kittens/claude_dashboard.py

Or run standalone in a Kitty overlay:
    launch --type=overlay python3 /path/to/kittens/claude_dashboard.py

Lists all tabs running Claude Code, color-coded by account.
Navigate with j/k or arrows, Enter to focus, x to close, n to create new,
r to refresh, q/Esc to dismiss.
"""

import curses
import json
import os
import sys


def _kitten_dir():
    """Return the directory containing this kitten script.

    When Kitty runs a kitten via ``kitten foo.py``, ``__file__`` may not be
    defined.  Fall back to ``sys.argv[0]`` (which Kitty sets to the script
    path) and finally to ``~/.config/kitty/`` where ``just install`` symlinks
    the kittens.
    """
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

from cc_dashboard.config import load_accounts, load_defaults, load_sessions
from cc_dashboard.remote.command import (
    find_existing_dashboard,
    get_host_tab_info,
    get_kitty_socket,
    kitty_cmd,
)
from cc_dashboard.remote.shared import _ICON_DASHBOARD, _strip_dashboard_marker
from cc_dashboard.ui import dashboard


def _dashboard_title(title):
    """Return *title* with the dashboard marker prepended exactly once."""
    if title.startswith(_ICON_DASHBOARD):
        return title
    return _ICON_DASHBOARD + title


def _restore_host_title(socket, tab_id, was_managed):
    """Remove the dashboard marker from the current host tab title.

    For managed tabs, strips the marker and restores the explicit title.
    For unmanaged tabs, resets the title to empty string so Kitty falls
    back to the dynamic window title.
    """
    if not was_managed:
        # Reset to dynamic title by clearing the user-set tab name.
        restore = kitty_cmd(socket, "set-tab-title", "--match", f"id:{tab_id}", "")
        return restore is not None and restore.returncode == 0

    result = kitty_cmd(socket, "ls")
    if not result or result.returncode != 0:
        return False
    try:
        data = json.loads(result.stdout)
    except (json.JSONDecodeError, TypeError):
        return False
    if not isinstance(data, list):
        return False

    for os_win in data:
        for tab in os_win.get("tabs", []):
            if tab.get("id") != tab_id:
                continue
            title = tab.get("title", "")
            cleaned_title = _strip_dashboard_marker(title)
            if cleaned_title == title:
                return True
            restore = kitty_cmd(
                socket,
                "set-tab-title",
                "--match",
                f"id:{tab_id}",
                cleaned_title,
            )
            return restore is not None and restore.returncode == 0
    return False


def main(args=None):
    """Entry point for both the Kitty kitten protocol and standalone usage.

    When launched via ``kitten /path/to/claude_dashboard.py``, Kitty calls this
    function in a child process attached to an overlay window. When run directly,
    ``curses.wrapper`` manages the terminal setup and teardown.
    """
    socket = get_kitty_socket()
    if socket:
        existing = find_existing_dashboard(socket)
        if existing:
            kitty_cmd(socket, "focus-tab", "--match", f"id:{existing['tab_id']}")
            print(
                "Dashboard already running — focused existing instance.",
                file=sys.stderr,
            )
            return ""

    host_tab_id = None
    host_was_managed = False
    host_original_title = ""
    if socket:
        host_info = get_host_tab_info(socket)
        if host_info:
            host_tab_id = host_info.get("tab_id")
            host_was_managed = host_info.get("is_managed", False)
            title = host_info.get("title", "")
            host_original_title = _strip_dashboard_marker(title)
            if host_tab_id is not None:
                kitty_cmd(
                    socket,
                    "set-tab-title",
                    "--match",
                    f"id:{host_tab_id}",
                    _dashboard_title(title),
                )

    try:
        load_accounts()
        load_defaults()
        load_sessions()

        curses.wrapper(dashboard)
    finally:
        if socket and host_tab_id is not None:
            _restore_host_title(socket, host_tab_id, host_was_managed)

    if host_tab_id is not None:
        return json.dumps(
            {
                "host_tab_id": host_tab_id,
                "host_original_title": host_original_title,
                "was_managed": host_was_managed,
            }
        )
    return ""


try:
    from kittens.tui.handler import result_handler

    @result_handler()
    def handle_result(args, answer, target_window_id, boss):
        if not answer:
            return
        try:
            data = json.loads(answer)
        except (json.JSONDecodeError, TypeError):
            return
        tab_id = data.get("host_tab_id")
        if not tab_id:
            return
        was_managed = data.get("was_managed", False)
        # Backup cleanup if the subprocess-based restore in main() does not
        # land before overlay teardown completes.
        if was_managed:
            original_title = data.get("host_original_title", "")
            if original_title:
                boss.call_remote_control(
                    None,
                    ("set-tab-title", "--match", f"id:{tab_id}", original_title),
                )
        else:
            # Reset to dynamic title for unmanaged tabs.
            boss.call_remote_control(
                None,
                ("set-tab-title", "--match", f"id:{tab_id}", ""),
            )

except ImportError:
    pass


if __name__ == "__main__":
    main(sys.argv)
