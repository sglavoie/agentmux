#!/usr/bin/env python3
"""Keybinding quick-reference overlay for cc-manager-kitty.

Displays all ccm-managed keybindings (Kitty-global and dashboard-internal)
in a scrollable full-screen curses overlay with less-like navigation and
search.  Dismiss with q or Escape.
"""

import contextlib
import curses
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

for _mod in list(sys.modules):
    if _mod.startswith("cc_dashboard"):
        del sys.modules[_mod]

from cc_dashboard.colors import (
    CP_DIM,
    CP_HEADER,
    CP_MATCH,
    CP_NORMAL,
    init_colors,
)
from cc_dashboard.config import load_accounts
from cc_dashboard.drawing import safe_addstr
from cc_dashboard.views_shared import _FOOTER_HINTS

# --- Kitty Global section (hardcoded — keys from kitty.conf, not footer hints) ---

_KITTY_GLOBAL_SECTION = (
    "Kitty Global",
    [
        ("F1", "Keybinding reference (this overlay)"),
        ("F2", "Toggle star / pin on current tab"),
        ("F3", "Toggle error / warning markers"),
        ("F4", "Clear markers and state icons"),
        ("F5", "Toggle flag on current tab"),
        ("F6", "Set tab color (color picker overlay)"),
        ("F7", "Set tab status (status overlay)"),
        ("F8", "Session info overlay"),
        ("F9", "Edit session notes"),
        ("F10", "Toggle dim on current tab"),
        ("F12", "Broadcast input to all tab windows"),
        ("Cmd+1..0", "Switch to tab 1-10"),
        ("Cmd+L", "Toggle between last two tabs"),
        ("Ctrl+Shift+D", "Open session dashboard"),
        ("Ctrl+Shift+P", "Quick-switch overlay"),
    ],
)

# --- Auto-generation configuration ---

_MODE_DISPLAY_NAMES: dict[str, str] = {
    "list": "Session List",
    "filter": "Account Filter",
    "actions": "Session Actions",
    "sort_pick": "Sort Picker",
    "color_picker": "Color Picker",
    "peek": "Session Peek",
    "actlog": "Activity Log",
    "actlog_browse": "Log Browser",
    "actlog_gsearch": "Global Log Search",
    "config_pick": "Config Browser",
    "config_action": "Config Action",
    "new": "New / Edit Session",
    "new_dir_browse": "Directory Browser",
    "pick": "Launch from Config",
    "rename": "Rename Session",
    "clone": "Clone Session",
    "worktree": "Create Worktree",
    "override": "Config Dir Override",
    "move": "Move to Instance",
    "restart_options": "Restart Options",
    "delete": "Delete Session",
    "hooks": "Hooks Manager",
    "hooks_event": "Hook Events",
    "hooks_add": "Add / Edit Hook",
    "hooks_matcher_pick": "Matcher Picker",
    "hooks_info": "Hook Info",
    "hooks_example": "Hook Example",
}

_MODE_ORDER: list[str] = [
    # Main list and navigation
    "list",
    "filter",
    # Actions and pickers
    "actions",
    "sort_pick",
    "color_picker",
    # Viewers
    "peek",
    "actlog",
    "actlog_browse",
    "actlog_gsearch",
    # Config management
    "config_pick",
    "config_action",
    # Session forms
    "new",
    "new_dir_browse",
    "pick",
    "rename",
    "clone",
    "worktree",
    "override",
    "move",
    "restart_options",
    "delete",
    # Hooks management
    "hooks",
    "hooks_event",
    "hooks_add",
    "hooks_matcher_pick",
    "hooks_info",
    "hooks_example",
]

_SKIP_F1_MODES: set[str] = {
    # Confirmation dialogs (y/n only)
    "confirm",
    "confirm_restart",
    "confirm_batch_close",
    "confirm_batch_restart",
    "confirm_save",
    "delete_confirm",
    "hooks_delete_confirm",
    "hooks_sync_confirm",
    # Search input sub-modes (standard text input)
    "peek_search",
    "actlog_search",
    "actlog_browse_search",
    "actlog_gsearch_search",
}

_DESCRIPTION_OVERRIDES: dict[tuple[str, str], str] = {
    ("list", "Enter"): "Focus & switch to selected tab",
    ("list", "Ctrl+F"): "Search across all activity logs",
}


def _parse_hint(hint: str) -> tuple[str | None, str]:
    """Parse a footer hint into (key, description).

    Returns (None, full_text) for descriptive entries (containing ':' before
    the first space, e.g. "Marked: Restart/Close/Move").
    """
    colon = hint.find(":")
    space = hint.find(" ")
    if colon != -1 and (space == -1 or colon < space):
        return (None, hint)
    if space == -1:
        return (hint, "")
    key = hint[:space]
    desc = hint[space + 1 :]
    # Convert ^x to Ctrl+x (but not compound ^X/^Y patterns like ^D/^U)
    if key.startswith("^") and "/" not in key and len(key) == 2:
        key = f"Ctrl+{key[1]}"
    return (key, desc)


def _build_sections() -> list[tuple[str, list[tuple[str, str]]]]:
    """Build F1 overlay sections from _FOOTER_HINTS."""
    sections: list[tuple[str, list[tuple[str, str]]]] = [_KITTY_GLOBAL_SECTION]
    for mode in _MODE_ORDER:
        hints = _FOOTER_HINTS.get(mode, [])
        bindings: list[tuple[str, str]] = []
        for hint in hints:
            raw_key, desc = _parse_hint(hint)
            override = _DESCRIPTION_OVERRIDES.get((mode, raw_key or ""))
            if override:
                desc = override
            bindings.append((raw_key or "", desc))
        display_name = _MODE_DISPLAY_NAMES.get(mode, mode.replace("_", " ").title())
        sections.append((display_name, bindings))
    return sections


# Validate: every mode in _FOOTER_HINTS must be in _MODE_ORDER or _SKIP_F1_MODES
_unregistered = set(_FOOTER_HINTS) - set(_MODE_ORDER) - _SKIP_F1_MODES
assert not _unregistered, (
    f"Footer hint modes not registered in _MODE_ORDER or _SKIP_F1_MODES: "
    f"{sorted(_unregistered)}"
)

_SECTIONS = _build_sections()

# Pre-build content lines: (kind, text1, text2)
_LINES: list[tuple[str, str, str]] = []
for _sect_name, _bindings in _SECTIONS:
    _LINES.append(("header", _sect_name, ""))
    for _key, _desc in _bindings:
        _LINES.append(("binding", _key, _desc))
    _LINES.append(("blank", "", ""))
if _LINES and _LINES[-1][0] == "blank":
    _LINES.pop()


def _compute_matches(query):
    """Return sorted list of line indices whose text contains query."""
    if not query:
        return []
    q = query.lower()
    return [
        i
        for i, (_kind, t1, t2) in enumerate(_LINES)
        if q in t1.lower() or q in t2.lower()
    ]


def _draw_highlighted(stdscr, y, x, text, base_attr, query, match_attr, max_w):
    """Draw text with matching substrings highlighted."""
    text = text[:max_w]
    if not query:
        safe_addstr(stdscr, y, x, text, base_attr)
        return
    ql = query.lower()
    tl = text.lower()
    pos = 0
    cx = x
    while pos < len(text):
        idx = tl.find(ql, pos)
        if idx == -1:
            safe_addstr(stdscr, y, cx, text[pos:], base_attr)
            break
        if idx > pos:
            safe_addstr(stdscr, y, cx, text[pos:idx], base_attr)
            cx += idx - pos
        safe_addstr(stdscr, y, cx, text[idx : idx + len(ql)], match_attr)
        cx += len(ql)
        pos = idx + len(ql)


def _draw(
    stdscr, scroll, search_query, match_lines, match_index, search_active, search_cursor
):
    """Render the keybinding reference overlay."""
    stdscr.erase()
    rows, cols = stdscr.getmaxyx()
    header_attr = curses.color_pair(CP_HEADER) | curses.A_BOLD
    normal_attr = curses.color_pair(CP_NORMAL)
    dim_attr = curses.color_pair(CP_DIM)
    match_attr = curses.color_pair(CP_MATCH) | curses.A_BOLD
    current_match_attr = curses.color_pair(CP_MATCH) | curses.A_BOLD | curses.A_REVERSE

    key_col = 4
    desc_col = 20

    safe_addstr(stdscr, 0, 2, "Keybinding Reference", header_attr)

    content_height = max(1, rows - 3)
    visible = _LINES[scroll : scroll + content_height]
    match_set = set(match_lines)
    current_match_line = match_lines[match_index] if match_lines else -1

    for i, (kind, text1, text2) in enumerate(visible):
        y = i + 1
        line_idx = scroll + i
        is_current = line_idx == current_match_line
        if kind == "header":
            if line_idx in match_set and search_query:
                hi = current_match_attr if is_current else match_attr
                _draw_highlighted(
                    stdscr, y, 2, text1, header_attr, search_query, hi, cols - 3
                )
            else:
                safe_addstr(stdscr, y, 2, text1, header_attr)
        elif kind == "binding":
            if line_idx in match_set and search_query:
                hi = current_match_attr if is_current else match_attr
                _draw_highlighted(
                    stdscr,
                    y,
                    key_col,
                    text1,
                    normal_attr,
                    search_query,
                    hi,
                    desc_col - key_col - 1,
                )
                _draw_highlighted(
                    stdscr,
                    y,
                    desc_col,
                    text2,
                    dim_attr,
                    search_query,
                    hi,
                    cols - desc_col - 1,
                )
            else:
                safe_addstr(stdscr, y, key_col, text1, normal_attr)
                safe_addstr(stdscr, y, desc_col, text2[: cols - desc_col - 1], dim_attr)

    # Footer
    if search_active:
        prompt = f"/{search_query}"
        safe_addstr(stdscr, rows - 1, 2, prompt, curses.color_pair(CP_NORMAL))
        curses.curs_set(1)
        # Position cursor after the search text
        with contextlib.suppress(curses.error):
            stdscr.move(rows - 1, 2 + 1 + search_cursor)
    else:
        curses.curs_set(0)
        end = min(scroll + content_height, len(_LINES))
        pos = f"[{scroll + 1}-{end}/{len(_LINES)}]"
        hints = "j/k d/u g/G scroll  / search"
        if match_lines:
            hints += f"  n/N match ({match_index + 1}/{len(match_lines)})"
        hints += "  q quit"
        safe_addstr(stdscr, rows - 2, 2, f"{hints}  {pos}", dim_attr)
        if search_query and not match_lines:
            safe_addstr(stdscr, rows - 1, 2, "No matches", dim_attr)
        elif search_query and match_lines:
            safe_addstr(
                stdscr,
                rows - 1,
                2,
                f"Search: {search_query}  ({match_index + 1}/{len(match_lines)})",
                dim_attr,
            )


def _scroll_to_line(target, scroll, content_height, max_scroll):
    """Return scroll position that makes target line visible, centered if possible."""
    if target < scroll:
        return max(0, target - content_height // 3)
    if target >= scroll + content_height:
        return min(max_scroll, target - content_height // 3)
    return scroll


def _run(stdscr):
    """Event loop for the keybinding overlay."""
    curses.set_escdelay(25)
    curses.curs_set(0)
    init_colors()
    stdscr.timeout(-1)

    scroll = 0
    rows = stdscr.getmaxyx()[0]
    content_height = max(1, rows - 3)
    max_scroll = max(0, len(_LINES) - content_height)

    # Search state
    search_active = False
    search_query = ""
    search_cursor = 0
    match_lines: list[int] = []
    match_index = 0

    while True:
        rows = stdscr.getmaxyx()[0]
        content_height = max(1, rows - 3)
        max_scroll = max(0, len(_LINES) - content_height)
        scroll = min(scroll, max_scroll)

        _draw(
            stdscr,
            scroll,
            search_query,
            match_lines,
            match_index,
            search_active,
            search_cursor,
        )
        stdscr.refresh()

        key = stdscr.getch()
        if key == -1:
            continue

        if search_active:
            if key in (27,):  # Escape — cancel search
                search_active = False
                search_query = ""
                search_cursor = 0
                match_lines = []
                match_index = 0
            elif key in (10, 13, curses.KEY_ENTER):  # Enter — accept
                search_active = False
                if match_lines:
                    scroll = _scroll_to_line(
                        match_lines[match_index], scroll, content_height, max_scroll
                    )
            elif key in (curses.KEY_BACKSPACE, 127, 8):
                if search_cursor > 0:
                    search_query = (
                        search_query[: search_cursor - 1] + search_query[search_cursor:]
                    )
                    search_cursor -= 1
                    match_lines = _compute_matches(search_query)
                    match_index = 0
            elif key == curses.KEY_LEFT:
                if search_cursor > 0:
                    search_cursor -= 1
            elif key == curses.KEY_RIGHT:
                if search_cursor < len(search_query):
                    search_cursor += 1
            elif 32 <= key <= 126:
                ch = chr(key)
                search_query = (
                    search_query[:search_cursor] + ch + search_query[search_cursor:]
                )
                search_cursor += 1
                match_lines = _compute_matches(search_query)
                match_index = 0
            continue

        # --- Non-search key handling ---

        # Scroll down (single line)
        if key in (ord("j"), curses.KEY_DOWN):
            if scroll < max_scroll:
                scroll += 1
            continue

        # Scroll up (single line)
        if key in (ord("k"), curses.KEY_UP):
            if scroll > 0:
                scroll -= 1
            continue

        # Half-page down: d or Ctrl+d
        if key in (ord("d"), 4):
            scroll = min(max_scroll, scroll + content_height // 2)
            continue

        # Half-page up: u or Ctrl+u
        if key in (ord("u"), 21):
            scroll = max(0, scroll - content_height // 2)
            continue

        # Top
        if key == ord("g"):
            scroll = 0
            continue

        # Bottom
        if key == ord("G"):
            scroll = max_scroll
            continue

        # Enter search mode
        if key == ord("/"):
            search_active = True
            search_query = ""
            search_cursor = 0
            match_lines = []
            match_index = 0
            continue

        # Next match
        if key == ord("n") and match_lines:
            match_index = (match_index + 1) % len(match_lines)
            scroll = _scroll_to_line(
                match_lines[match_index], scroll, content_height, max_scroll
            )
            continue

        # Previous match
        if key == ord("N") and match_lines:
            match_index = (match_index - 1) % len(match_lines)
            scroll = _scroll_to_line(
                match_lines[match_index], scroll, content_height, max_scroll
            )
            continue

        # Escape: clear search if active, otherwise quit
        if key == 27:
            if search_query:
                search_query = ""
                match_lines = []
                match_index = 0
                continue
            break

        # Quit
        if key == ord("q"):
            break


def main(_args=None):
    load_accounts()
    curses.wrapper(_run)


if __name__ == "__main__":
    main()
