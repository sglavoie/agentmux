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
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.realpath(__file__)))

from cc_dashboard.config import load_accounts, load_defaults
from cc_dashboard.kitty_remote import get_kitty_socket
from cc_dashboard.ui import dashboard


def main(args=None):
    """Entry point for both the Kitty kitten protocol and standalone usage.

    When launched via ``kitten /path/to/claude_dashboard.py``, Kitty calls this
    function in a child process attached to an overlay window. When run directly,
    ``curses.wrapper`` manages the terminal setup and teardown.
    """
    load_accounts()
    load_defaults()

    socket = get_kitty_socket()
    if not socket:
        print(
            "Error: No kitty socket found. "
            "Is Kitty running with remote control enabled?"
        )
        input("Press Enter to close...")
        return ""

    curses.wrapper(lambda stdscr: dashboard(stdscr, socket))
    return ""


# Kitty kitten protocol: handle_result runs in the Kitty main process after
# main() returns.  All tab actions are performed from within main() via
# subprocess calls to kitty @, so handle_result is intentionally a no-op.
try:
    from kittens.tui.handler import result_handler

    @result_handler(no_ui=True)
    def handle_result(args, answer, target_window_id, boss):
        pass

except ImportError:
    pass


if __name__ == "__main__":
    main(sys.argv)
