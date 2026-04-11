"""Shared constants for the keybinding audit."""

from __future__ import annotations

import re
from pathlib import Path

ROOT_DIR = Path(__file__).resolve().parents[2]
KITTENS_DIR = ROOT_DIR / "kittens"
DASHBOARD_DIR = KITTENS_DIR / "cc_dashboard"

CURSES_KEY_MAP = {
    "KEY_UP": "up",
    "KEY_DOWN": "down",
    "KEY_LEFT": "left",
    "KEY_RIGHT": "right",
    "KEY_ENTER": "enter",
    "KEY_BACKSPACE": "backspace",
    "KEY_HOME": "home",
    "KEY_END": "end",
    "KEY_NPAGE": "npage",
    "KEY_PPAGE": "ppage",
    "KEY_BTAB": "shift-tab",
}

NUMERIC_KEY_MAP: dict[int, str] = {
    27: "esc",
    10: "enter",
    13: "enter",
    127: "backspace",
    8: "backspace",
    9: "tab",
    353: "shift-tab",
}

for index, character in enumerate("abcdefghijklmnopqrstuvwxyz", 1):
    NUMERIC_KEY_MAP[index] = f"ctrl+{character}"

KEY_EQUIVALENCES: list[set[str]] = [
    {"enter", "ctrl+j", "ctrl+m"},
    {"backspace", "ctrl+h"},
    {"tab", "ctrl+i"},
    {"j", "k", "up", "down"},
    {"q", "esc"},
    {"npage", "ctrl+f"},
    {"ppage", "ctrl+b"},
    {" ", "space"},
]

EQUIV_LOOKUP: dict[str, set[str]] = {}
for group in KEY_EQUIVALENCES:
    for key in group:
        EQUIV_LOOKUP.setdefault(key, set()).update(group)

HINT_KEY_NORMALIZE: dict[str, str] = {
    "enter": "enter",
    "esc": "esc",
    "tab": "tab",
    "space": "space",
    "up": "up",
    "dn": "down",
    "down": "down",
    "shift-tab": "shift-tab",
    "s-tab": "shift-tab",
    "any": "any",
}

TEXT_INPUT_KEYS = {
    "backspace",
    "ctrl+h",
    "left",
    "right",
    "home",
    "end",
    "ctrl+a",
    "ctrl+e",
}

TEXT_INPUT_MODES = {
    "batch_group",
    "rename",
    "clone",
    "worktree",
    "new",
    "new_dir_browse",
    "prefs",
    "prefs_array",
    "restart_options",
    "hooks_add",
    "peek_search",
    "actlog_search",
    "actlog_browse_search",
    "actlog_gsearch_search",
}

MODES_WITH_SEARCH_SUBMODE = {
    "list",
    "pick",
    "delete",
    "peek",
    "actlog",
    "actlog_browse",
    "actlog_gsearch",
}

SEARCH_SUBMODE_IMPLICIT_KEYS = {
    "/",
    "enter",
    "ctrl+j",
    "ctrl+m",
    "esc",
    "backspace",
    "ctrl+h",
    "left",
    "right",
    "j",
    "k",
    "up",
    "down",
}

MODE_EXTRA_IGNORE: dict[str, set[str]] = {
    "list": {str(digit) for digit in range(1, 10)},
    "pick": {str(digit) for digit in range(1, 10)},
    "new": {"."},
}

RE_ORD = re.compile(r"""ord\(["'](.?)["']\)""")
RE_CURSES_KEY = re.compile(r"curses\.(KEY_\w+)")
RE_NUMERIC = re.compile(r"\b(\d{1,3})\b")
RE_KEY_RANGE = re.compile(
    r"""ord\(["'](\d)["']\)\s*<=\s*key\s*<=\s*ord\(["'](\d)["']\)"""
)
RE_PRINTABLE_RANGE = re.compile(r"32\s*<=\s*key\s*[<]=?\s*12[67]")
RE_FUNCTION_DEF = re.compile(r"^(def (\w+)\()")

HANDLER_FILES = [
    DASHBOARD_DIR / "loop_mode_list.py",
    DASHBOARD_DIR / "loop_modes_dashboard.py",
    DASHBOARD_DIR / "loop_modes_forms.py",
    DASHBOARD_DIR / "loop_modes_hooks.py",
    DASHBOARD_DIR / "loop_modes_config.py",
    DASHBOARD_DIR / "loop_modes_delete.py",
    DASHBOARD_DIR / "loop_modes_prefs.py",
    DASHBOARD_DIR / "loop_viewers.py",
]

FUNCTION_MODES: dict[str, list[str]] = {
    "handle_list_mode": ["list"],
    "handle_actions_mode": ["actions"],
    "handle_restart_options_mode": ["restart_options"],
    "handle_sort_pick_mode": ["sort_pick"],
    "handle_confirm_modes": [
        "confirm",
        "confirm_restart",
        "confirm_batch_clone",
        "confirm_batch_close",
        "confirm_batch_restart",
        "confirm_save",
    ],
    "handle_color_picker_mode": ["color_picker"],
    "handle_move_picker_mode": ["move_picker"],
    "handle_override_mode": ["override"],
    "handle_rename_mode": ["rename"],
    "handle_worktree_mode": ["worktree"],
    "handle_batch_group_mode": ["batch_group"],
    "handle_clone_mode": ["clone"],
    "handle_new_mode": ["new"],
    "handle_confirm_mkdir_mode": ["confirm_mkdir"],
    "handle_pick_mode": ["pick"],
    "handle_hooks_modes": [
        "hooks",
        "hooks_event",
        "hooks_delete_confirm",
        "hooks_sync_confirm",
        "hooks_matcher_pick",
        "hooks_add",
        "hooks_info",
        "hooks_example",
    ],
    "_handle_hooks_add": ["hooks_add"],
    "handle_config_modes": ["config_pick", "config_action", "config_show"],
    "handle_delete_mode": ["delete", "delete_confirm"],
    "handle_actlog_mode": ["actlog", "actlog_browse", "actlog_gsearch"],
    "handle_peek_mode": ["peek"],
    "handle_prefs_mode": ["prefs"],
    "handle_prefs_array_mode": ["prefs_array"],
    "handle_group_actions_mode": ["group_actions"],
}

MODE_NAME_MAP: dict[str, str] = {
    "move": "move_picker",
}

EMBEDDED_SUBMODES: set[str] = {
    "filter",
    "group_filter",
    "pick_group_filter",
    "new_dir_browse",
    "peek_search",
    "actlog_search",
    "actlog_browse_search",
    "actlog_gsearch_search",
}
