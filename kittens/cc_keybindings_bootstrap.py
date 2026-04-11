"""Bootstrap helpers for the cc_keybindings kitten."""

from __future__ import annotations

import os
import sys


def kitten_dir() -> str:
    """Return the directory containing this kitten script."""
    for candidate in (
        globals().get("__file__"),
        sys.argv[0] if sys.argv else None,
    ):
        if candidate and os.path.isfile(candidate):
            return os.path.dirname(os.path.realpath(candidate))
    return os.path.expanduser("~/.config/kitty")


def prepare_cc_dashboard_imports() -> None:
    """Ensure sibling modules are importable and dashboard modules are fresh."""
    module_dir = kitten_dir()
    if module_dir not in sys.path:
        sys.path.insert(0, module_dir)

    for module_name in list(sys.modules):
        if module_name.startswith("cc_dashboard"):
            del sys.modules[module_name]
