#!/usr/bin/env python3
"""Audit keybinding documentation completeness.

Compares keys handled in dashboard code against _FOOTER_HINTS to catch
keys that are handled in code but missing from footer hints.

Exit code 0 if clean, 1 if gaps found.
"""

import ast
import os
import re
import sys

_KITTENS_DIR = os.path.join(os.path.dirname(os.path.dirname(__file__)), "kittens")
_DASHBOARD_DIR = os.path.join(_KITTENS_DIR, "cc_dashboard")

# --- Step 1: Parse _FOOTER_HINTS from source (no curses import needed) ---


def _extract_footer_hints() -> dict[str, list[str]]:
    """Parse _FOOTER_HINTS from views_shared.py using AST."""
    path = os.path.join(_DASHBOARD_DIR, "views_shared.py")
    with open(path) as f:
        tree = ast.parse(f.read(), path)
    for node in ast.walk(tree):
        if (
            isinstance(node, ast.Assign)
            and len(node.targets) == 1
            and isinstance(node.targets[0], ast.Name)
            and node.targets[0].id == "_FOOTER_HINTS"
        ):
            return ast.literal_eval(node.value)
    raise RuntimeError("Could not find _FOOTER_HINTS in views_shared.py")


# --- Step 2: Parse key handlers from code ---

# curses.KEY_* -> normalized name
_CURSES_KEY_MAP = {
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

# Numeric key constants -> normalized name
_NUMERIC_KEY_MAP: dict[int, str] = {
    27: "esc",
    10: "enter",
    13: "enter",
    127: "backspace",
    8: "backspace",
    9: "tab",
    353: "shift-tab",
}

# Ctrl+letter numeric codes (1=Ctrl+a .. 26=Ctrl+z)
for _i, _ch in enumerate("abcdefghijklmnopqrstuvwxyz", 1):
    _NUMERIC_KEY_MAP[_i] = f"ctrl+{_ch}"

# Key equivalence groups: documenting any key in a group covers the others.
# This handles Enter = Ctrl+j = Ctrl+m, Backspace = Ctrl+h, j/k = Up/Down, etc.
_KEY_EQUIVALENCES: list[set[str]] = [
    {"enter", "ctrl+j", "ctrl+m"},
    {"backspace", "ctrl+h"},
    {"tab", "ctrl+i"},
    {"j", "k", "up", "down"},
    {"q", "esc"},
    {"npage", "ctrl+f"},
    {"ppage", "ctrl+b"},
    {" ", "space"},
]

# Build a lookup: key -> set of all equivalent keys
_EQUIV_LOOKUP: dict[str, set[str]] = {}
for _group in _KEY_EQUIVALENCES:
    for _k in _group:
        _EQUIV_LOOKUP.setdefault(_k, set()).update(_group)

# Hint key name -> canonical form
_HINT_KEY_NORMALIZE: dict[str, str] = {
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

# Keys that are standard text-input handling (not worth documenting)
_TEXT_INPUT_KEYS = {
    "backspace",
    "ctrl+h",
    "left",
    "right",
    "home",
    "end",
    "ctrl+a",
    "ctrl+e",
}

# Modes where printable char input (32-126) is expected text entry
_TEXT_INPUT_MODES = {
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

# Modes that contain an embedded search sub-mode (search_active block).
# The sub-mode handles standard text input + enter/esc/backspace/arrows,
# which should not be reported as undocumented for the parent mode.
_MODES_WITH_SEARCH_SUBMODE = {
    "list",
    "pick",
    "delete",
    "peek",
    "actlog",
    "actlog_browse",
    "actlog_gsearch",
}

# Keys handled by embedded search sub-modes (standard text editing + nav)
_SEARCH_SUBMODE_IMPLICIT_KEYS = {
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

# Per-mode extra keys to ignore (handled by sub-modes within the handler).
# list: digits 1-9 are handled by embedded account_filter_mode block,
# documented in the "filter" footer hints rather than "list".
# new: "." is handled by the dir_browse sub-mode embedded in handle_new_mode,
# documented in the "new_dir_browse" footer hints rather than "new".
_MODE_EXTRA_IGNORE: dict[str, set[str]] = {
    "list": {str(d) for d in range(1, 10)},
    "pick": {str(d) for d in range(1, 10)},
    "new": {"."},
}

# Regex patterns
_RE_ORD = re.compile(r"""ord\(["'](.?)["']\)""")
_RE_CURSES_KEY = re.compile(r"curses\.(KEY_\w+)")
_RE_NUMERIC = re.compile(r"\b(\d{1,3})\b")
_RE_KEY_RANGE = re.compile(
    r"""ord\(["'](\d)["']\)\s*<=\s*key\s*<=\s*ord\(["'](\d)["']\)"""
)
_RE_PRINTABLE_RANGE = re.compile(r"32\s*<=\s*key\s*[<]=?\s*12[67]")

_HANDLER_FILES = [
    os.path.join(_DASHBOARD_DIR, "loop_mode_list.py"),
    os.path.join(_DASHBOARD_DIR, "loop_modes_dashboard.py"),
    os.path.join(_DASHBOARD_DIR, "loop_modes_forms.py"),
    os.path.join(_DASHBOARD_DIR, "loop_modes_hooks.py"),
    os.path.join(_DASHBOARD_DIR, "loop_modes_config.py"),
    os.path.join(_DASHBOARD_DIR, "loop_modes_delete.py"),
    os.path.join(_DASHBOARD_DIR, "loop_modes_prefs.py"),
    os.path.join(_DASHBOARD_DIR, "loop_viewers.py"),
]

# Function name -> list of modes it handles
_FUNCTION_MODES: dict[str, list[str]] = {
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

# Footer hint mode -> code mode name (when they differ)
_MODE_NAME_MAP: dict[str, str] = {
    "move": "move_picker",
}

# Sub-modes embedded in parent handlers via state flags (e.g. search_active,
# account_filter_mode) rather than mode == "x" checks. Their keys are covered
# by the parent mode's analysis; we skip them in the "no handler found" report.
_EMBEDDED_SUBMODES: set[str] = {
    "filter",  # in handle_list_mode via account_filter_mode
    "group_filter",  # in handle_list_mode via group_filter_mode
    "pick_group_filter",  # in handle_pick_mode via pick_group_filter_mode
    "new_dir_browse",  # in handle_new_mode via dir_browse_active
    "peek_search",  # in handle_peek_mode via peek_search_active
    "actlog_search",  # in handle_actlog_mode via actlog_search_active
    "actlog_browse_search",  # in handle_actlog_mode via actlog_browse_search_active
    "actlog_gsearch_search",  # in handle_actlog_mode via actlog_gsearch_search_active
}


def _extract_keys_from_line(line: str) -> set[str]:
    """Extract normalized key identifiers from a single line of code."""
    keys: set[str] = set()

    for m in _RE_ORD.finditer(line):
        ch = m.group(1)
        if ch:
            keys.add(ch.lower())

    for m in _RE_CURSES_KEY.finditer(line):
        name = m.group(1)
        if name in _CURSES_KEY_MAP:
            keys.add(_CURSES_KEY_MAP[name])

    if "key" in line and ("==" in line or "in (" in line or "in(" in line):
        for m in _RE_NUMERIC.finditer(line):
            val = int(m.group(1))
            if val in _NUMERIC_KEY_MAP:
                keys.add(_NUMERIC_KEY_MAP[val])

    return keys


def _extract_keys_for_mode_block(lines: list[str]) -> set[str]:
    """Extract all key identifiers from a block of handler code."""
    keys: set[str] = set()
    has_printable_range = False
    for line in lines:
        keys.update(_extract_keys_from_line(line))
        if _RE_PRINTABLE_RANGE.search(line):
            has_printable_range = True
        for m in _RE_KEY_RANGE.finditer(line):
            lo, hi = m.group(1), m.group(2)
            for c in range(int(lo), int(hi) + 1):
                keys.add(str(c))
    if has_printable_range:
        keys.add("_printable_")
    return keys


def _parse_handler_functions(filepath: str) -> dict[str, list[str]]:
    """Parse a handler file and return {function_name: [lines]}."""
    functions: dict[str, list[str]] = {}
    with open(filepath) as f:
        source_lines = f.readlines()

    current_func = None
    current_lines: list[str] = []

    for line in source_lines:
        stripped = line.rstrip()
        func_match = re.match(r"^(def (\w+)\()", stripped)
        if func_match:
            if current_func:
                functions[current_func] = current_lines
            current_func = func_match.group(2)
            current_lines = [stripped]
            continue
        if current_func:
            if stripped and not stripped.startswith(" ") and not stripped.startswith("#"):
                functions[current_func] = current_lines
                current_func = None
                current_lines = []
            else:
                current_lines.append(stripped)

    if current_func:
        functions[current_func] = current_lines

    return functions


def _split_mode_blocks(
    lines: list[str], modes: list[str]
) -> dict[str, list[str]]:
    """Split function lines into per-mode blocks based on mode string checks."""
    if len(modes) == 1:
        return {modes[0]: lines}

    blocks: dict[str, list[str]] = {m: [] for m in modes}
    current_mode = None

    for line in lines:
        for mode in modes:
            if f'"{mode}"' in line or f"'{mode}'" in line:
                if "mode ==" in line or "mode !=" in line or 'mode"] ==' in line:
                    current_mode = mode
                    break
        if current_mode and current_mode in blocks:
            blocks[current_mode].append(line)

    return blocks


def _extract_all_code_keys() -> dict[str, set[str]]:
    """Extract handled keys per mode from all handler files."""
    code_keys: dict[str, set[str]] = {}

    for filepath in _HANDLER_FILES:
        if not os.path.exists(filepath):
            continue
        functions = _parse_handler_functions(filepath)

        for func_name, func_lines in functions.items():
            if func_name not in _FUNCTION_MODES:
                continue
            modes = _FUNCTION_MODES[func_name]
            mode_blocks = _split_mode_blocks(func_lines, modes)

            for mode, block_lines in mode_blocks.items():
                keys = _extract_keys_for_mode_block(block_lines)
                if mode not in code_keys:
                    code_keys[mode] = set()
                code_keys[mode].update(keys)

    return code_keys


# --- Step 3: Normalize and compare ---


def _normalize_hint_key(k: str) -> str:
    """Normalize a hint key to match code key format."""
    k_lower = k.lower()
    if k_lower in _HINT_KEY_NORMALIZE:
        return _HINT_KEY_NORMALIZE[k_lower]
    if k_lower.startswith("ctrl+"):
        return k_lower
    if k_lower.startswith("^") and len(k_lower) == 2:
        return f"ctrl+{k_lower[1]}"
    return k_lower


def _expand_hint_keys(hints: list[str]) -> set[str]:
    """Expand footer hint keys into normalized code-comparable keys.

    Also expands key equivalences: documenting "Enter" covers ctrl+j/ctrl+m,
    documenting "j/k" covers up/down, etc.
    """
    raw_keys: set[str] = set()
    for hint in hints:
        colon = hint.find(":")
        space = hint.find(" ")
        if colon != -1 and (space == -1 or colon < space):
            continue  # descriptive entry
        if space == -1:
            token = hint
        else:
            token = hint[:space]

        if "Any" in token:
            raw_keys.add("_any_")
            continue

        # Handle range patterns like "1-N" or "1-9"
        range_match = re.match(r"(\d)-[N\d]", token)
        if range_match:
            for d in range(1, 10):
                raw_keys.add(str(d))
            continue

        for part in token.split("/"):
            raw_keys.add(_normalize_hint_key(part))

    # Expand equivalences: if "enter" is documented, also cover ctrl+j, ctrl+m
    expanded = set(raw_keys)
    for k in raw_keys:
        if k in _EQUIV_LOOKUP:
            expanded.update(_EQUIV_LOOKUP[k])
    return expanded


def _format_key(k: str) -> str:
    """Format a normalized key for display."""
    if k.startswith("ctrl+"):
        return f"Ctrl+{k[5:]}"
    return k


def main() -> int:
    footer_hints = _extract_footer_hints()
    code_keys = _extract_all_code_keys()

    print("=== Keybinding Documentation Audit ===")
    print()
    print("Checking code handlers against footer hints...")
    print()

    modes_with_gaps = 0
    modes_checked = 0

    for mode, hints in sorted(footer_hints.items()):
        if mode in _EMBEDDED_SUBMODES:
            continue
        code_mode = _MODE_NAME_MAP.get(mode, mode)
        if code_mode not in code_keys:
            continue

        modes_checked += 1
        hint_keys = _expand_hint_keys(hints)
        handled_keys = code_keys[code_mode]

        # Always-ignored synthetic keys
        ignore = {"_printable_", "_any_"}

        # For text-input modes, standard editing keys are not worth documenting
        if mode in _TEXT_INPUT_MODES:
            ignore.update(_TEXT_INPUT_KEYS)

        # For modes with embedded search sub-modes, the search sub-mode
        # handles text input + navigation keys that are already documented
        # in the corresponding *_search footer hints
        if mode in _MODES_WITH_SEARCH_SUBMODE:
            ignore.update(_TEXT_INPUT_KEYS)
            ignore.update(_SEARCH_SUBMODE_IMPLICIT_KEYS)

        # Per-mode extra ignores (e.g., filter digits in list mode)
        if mode in _MODE_EXTRA_IGNORE:
            ignore.update(_MODE_EXTRA_IGNORE[mode])

        undocumented = handled_keys - hint_keys - ignore

        if not undocumented:
            print(f"  {mode}: OK")
        else:
            modes_with_gaps += 1
            display_keys = sorted(_format_key(k) for k in undocumented)
            print(
                f"  {mode}: {len(undocumented)} key(s) in code "
                f"but not in footer hints:"
            )
            for dk in display_keys:
                print(f"    {dk}")

    # Report modes with no code handler found (skip known embedded sub-modes)
    no_handler = set()
    for mode in footer_hints:
        code_mode = _MODE_NAME_MAP.get(mode, mode)
        if code_mode not in code_keys and mode not in _EMBEDDED_SUBMODES:
            no_handler.add(mode)
    if no_handler:
        print()
        print(f"  Note: {len(no_handler)} mode(s) with no code handler found:")
        for m in sorted(no_handler):
            print(f"    {m}")

    print()
    if modes_with_gaps:
        print(f"Summary: {modes_with_gaps} mode(s) with undocumented keys")
        return 1
    print(f"Summary: All {modes_checked} checked mode(s) clean")
    return 0


if __name__ == "__main__":
    sys.exit(main())
