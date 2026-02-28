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


def _kitten_dir():
    """Return the directory containing this kitten script.

    When Kitty runs a kitten via ``kitten foo.py``, ``__file__`` may not be
    defined.  Fall back to ``sys.argv[0]`` (which Kitty sets to the script
    path) and finally to ``~/.config/kitty/`` where ``make install`` symlinks
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
from cc_dashboard.ui import dashboard


def main(args=None):
    """Entry point for both the Kitty kitten protocol and standalone usage.

    When launched via ``kitten /path/to/claude_dashboard.py``, Kitty calls this
    function in a child process attached to an overlay window. When run directly,
    ``curses.wrapper`` manages the terminal setup and teardown.
    """
    load_accounts()
    load_defaults()
    load_sessions()

    curses.wrapper(dashboard)
    return ""


# Kitty kitten protocol: handle_result runs in the Kitty main process after
# main() returns.  All tab actions are performed from within main() via
# subprocess calls to kitty @, so handle_result is intentionally a no-op.
try:
    from kittens.tui.handler import result_handler  # type: ignore[import-untyped]

    @result_handler()
    def handle_result(args, answer, target_window_id, boss):
        pass

except ImportError:
    pass


if __name__ == "__main__":
    main(sys.argv)
