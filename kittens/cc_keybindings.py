#!/usr/bin/env python3
"""Keybinding quick-reference overlay for cc-manager-kitty.

Displays all ccm-managed keybindings (Kitty-global and dashboard-internal)
in a scrollable full-screen curses overlay with less-like navigation and
search.  Dismiss with q or Escape.
"""

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

_SECTIONS = [
    (
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
            ("F12", "Broadcast input to all tab windows"),
            ("Cmd+1..0", "Switch to tab 1-10"),
            ("Cmd+L", "Toggle between last two tabs"),
            ("Ctrl+Shift+D", "Open session dashboard"),
            ("Ctrl+Shift+P", "Quick-switch overlay"),
        ],
    ),
    (
        "Dashboard — Navigation",
        [
            ("k / Up", "Move selection up"),
            ("j / Down", "Move selection down"),
            ("Enter", "Focus & switch to selected tab"),
            ("q", "Quit dashboard"),
        ],
    ),
    (
        "Dashboard — Session Actions",
        [
            ("t", "Open actions menu for selected/marked sessions"),
            (
                "Menu: Restart / Close / Move",
                "Available for marked sessions (when marks exist)",
            ),
            (
                "Menu: Restart opts / Clone / Override / Rename / Worktree",
                "Single-session only",
            ),
            ("Enter", "Apply selected action in the actions menu"),
            ("Esc", "Back from the actions menu"),
            ("n", "Open new session form"),
            ("l", "Launch session from config"),
            ("e", "Edit selected session config entry"),
            ("D", "Delete session from config"),
            ("S", "Save running sessions to config"),
            ("p", "Peek at selected session output"),
        ],
    ),
    (
        "Dashboard — Multi-Select",
        [
            ("Space", "Mark / unmark session"),
            ("U", "Clear all marks"),
            ("t", "Actions apply to marked sessions when marks exist"),
        ],
    ),
    (
        "Dashboard — View",
        [
            ("d", "Toggle detail pane"),
            ("a", "Toggle all / managed tabs"),
            ("Ctrl+r", "Refresh session list"),
        ],
    ),
    (
        "Dashboard — Search",
        [
            ("/", "Start search"),
            ("Enter", "Accept search query"),
            ("Esc", "Cancel search"),
        ],
    ),
    (
        "Dashboard — Account Filter",
        [
            ("f", "Enter / exit filter mode"),
            ("1-9", "Toggle account visibility"),
            ("Esc", "Clear filter"),
        ],
    ),
]

# Pre-build content lines: (kind, text1, text2)
_LINES = []
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


def _draw(stdscr, scroll, search_query, match_lines, match_index, search_active,
          search_cursor):
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

    content_height = rows - 3
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
                _draw_highlighted(stdscr, y, 2, text1, header_attr, search_query, hi,
                                  cols - 3)
            else:
                safe_addstr(stdscr, y, 2, text1, header_attr)
        elif kind == "binding":
            if line_idx in match_set and search_query:
                hi = current_match_attr if is_current else match_attr
                _draw_highlighted(stdscr, y, key_col, text1, normal_attr, search_query,
                                  hi, desc_col - key_col - 1)
                _draw_highlighted(stdscr, y, desc_col, text2, dim_attr, search_query,
                                  hi, cols - desc_col - 1)
            else:
                safe_addstr(stdscr, y, key_col, text1, normal_attr)
                safe_addstr(stdscr, y, desc_col, text2[: cols - desc_col - 1], dim_attr)

    # Footer
    if search_active:
        prompt = f"/{search_query}"
        safe_addstr(stdscr, rows - 1, 2, prompt, curses.color_pair(CP_NORMAL))
        curses.curs_set(1)
        # Position cursor after the search text
        try:
            stdscr.move(rows - 1, 2 + 1 + search_cursor)
        except curses.error:
            pass
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
                stdscr, rows - 1, 2,
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
    content_height = rows - 3
    max_scroll = max(0, len(_LINES) - content_height)

    # Search state
    search_active = False
    search_query = ""
    search_cursor = 0
    match_lines = []
    match_index = 0

    while True:
        rows = stdscr.getmaxyx()[0]
        content_height = rows - 3
        max_scroll = max(0, len(_LINES) - content_height)
        scroll = min(scroll, max_scroll)

        _draw(stdscr, scroll, search_query, match_lines, match_index, search_active,
              search_cursor)
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
                        search_query[: search_cursor - 1]
                        + search_query[search_cursor:]
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
