#!/usr/bin/env python3
"""Session notes overlay — opens $EDITOR with a per-session .md file.

Launched via F9. Uses the @result_handler(no_ui=True) pattern to open
the user's preferred editor as a Kitty overlay. Notes are stored as
plain files in ~/.config/cc-manager/notes/{sanitized_title}.md.
"""

import os
import shlex
import sys
from pathlib import Path


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

from cc_dashboard.config import load_accounts, load_defaults
from cc_dashboard.config_notes import (
    _resolve_notes_editor,
    notes_file_for_title,
)
from cc_dashboard.remote.shared import _strip_icons


def main(args=None):
    pass


from kittens.tui.handler import result_handler


@result_handler(no_ui=True)
def handle_result(args, result, target_window_id, boss):
    load_accounts()
    load_defaults()

    # Get the active tab directly from boss
    tab = boss.active_tab
    if not tab:
        return

    # Check this is a managed Claude session
    has_cc = False
    for w in tab.windows:
        uv = getattr(w, "user_vars", {}) or {}
        if uv.get("cc_account"):
            has_cc = True
            break
    if not has_cc:
        return

    # tab.name is the user-set title (via --tab-title / set-tab-title),
    # which is the stable session title like "⚡W:project".
    # tab.title falls through to the active window's dynamic process title
    # when tab.name is unset, which would give wrong names like "spec commit".
    title_raw = getattr(tab, "name", "") or ""
    title = _strip_icons(title_raw)
    if not title:
        return

    editor = _resolve_notes_editor()
    filepath = notes_file_for_title(title)

    # Ensure the file exists so the editor opens cleanly
    if not os.path.isfile(filepath):
        Path(filepath).touch()

    boss.call_remote_control(
        None,
        ("launch", "--type=overlay", *shlex.split(editor), filepath),
    )


if __name__ == "__main__":
    main()
