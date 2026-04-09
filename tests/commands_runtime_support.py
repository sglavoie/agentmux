#!/usr/bin/env python3
"""Shared support for commands runtime regression tests."""

import sys
from importlib import import_module
from pathlib import Path
from typing import Any, cast

REPO_ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(REPO_ROOT / "kittens"))

cfg = cast(Any, import_module("cc_dashboard.config"))
loop_modes_forms_actions = cast(
    Any, import_module("cc_dashboard.loop_modes_forms_actions")
)
_resolve_session_runtime_options = import_module(
    "cc_dashboard.loop_helpers"
)._resolve_session_runtime_options
handle_list_mode = import_module("cc_dashboard.loop_mode_list").handle_list_mode
_resolve_live_or_config_options = import_module(
    "cc_dashboard.loop_modes_forms"
)._resolve_live_or_config_options
handle_override_mode = import_module(
    "cc_dashboard.loop_modes_forms"
).handle_override_mode
actions_core = cast(Any, import_module("cc_dashboard.remote.actions_core"))
actions_lifecycle = cast(Any, import_module("cc_dashboard.remote.actions_lifecycle"))

PASS = 0
FAIL = 0


def check(label: str, condition: bool) -> None:
    global PASS, FAIL
    if condition:
        PASS += 1
        print(f"  PASS  {label}")
    else:
        FAIL += 1
        print(f"  FAIL  {label}")
