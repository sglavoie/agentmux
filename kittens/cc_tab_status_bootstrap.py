import importlib
import os
import sys
from collections.abc import Callable
from typing import Any

try:
    result_handler = importlib.import_module("kittens.tui.handler").result_handler
except ImportError:

    def result_handler(*_args: Any, **_kwargs: Any) -> Callable[[Any], Any]:
        def _decorator(func: Any) -> Any:
            return func

        return _decorator


def kitten_dir() -> str:
    """Return the directory containing the current kitten script."""
    for candidate in (
        globals().get("__file__"),
        sys.argv[0] if sys.argv else None,
    ):
        if candidate and os.path.isfile(candidate):
            return os.path.dirname(os.path.realpath(candidate))
    return os.path.expanduser("~/.config/kitty")


def prepare_cc_dashboard_imports() -> None:
    """Ensure local helper imports resolve and stale dashboard modules are flushed."""
    sys.path.insert(0, kitten_dir())

    # Flush cached cc_dashboard modules so Kitty's long-lived Python process
    # always picks up the current source files without requiring a restart.
    for module_name in list(sys.modules):
        if module_name.startswith("cc_dashboard"):
            del sys.modules[module_name]
