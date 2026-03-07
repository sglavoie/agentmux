#!/usr/bin/env python3
"""Tab status overlay for setting a status suffix on the current tab's title.

Launched via F7, displays an interactive picker to set a status suffix
like " - reviewing" or " - WIP" on the current tab's title.
"""

import curses
import json
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

# Flush cached cc_dashboard modules so Kitty's long-lived Python process
# always picks up the current source files without requiring a restart.
for _mod in list(sys.modules):
    if _mod.startswith("cc_dashboard"):
        del sys.modules[_mod]

from cc_dashboard.colors import CP_BORDER, CP_DIM, CP_HEADER, CP_INPUT, CP_NORMAL, CP_WARN, init_colors
from cc_dashboard.config import load_accounts, load_defaults
from cc_dashboard.config_core import _save_tab_statuses
from cc_dashboard.drawing import draw_border, safe_addstr
from cc_dashboard.remote.actions_core import do_rename
from cc_dashboard.remote.command import get_kitty_socket, kitty_cmd
from cc_dashboard.remote.shared import (
    _append_override_marker,
    _strip_icons,
    _strip_override_marker,
)

_DEFAULT_STATUSES = ["reviewing", "WIP", "blocked", "testing", "done", "paused", "debugging"]


def _load_statuses():
    """Load status presets from config state. Falls back to defaults."""
    from cc_dashboard import config_state as _state

    statuses = _state._default_tab_statuses
    if not statuses:
        return list(_DEFAULT_STATUSES)
    # Deduplicate while preserving order
    seen = set()
    result = []
    for s in statuses:
        if s not in seen:
            seen.add(s)
            result.append(s)
    return result


def _extract_status(title):
    """Split icon-stripped title into (base, status_or_None)."""
    if " - " in title:
        base, status = title.split(" - ", 1)
        return base, status
    return title, None


def _set_status(title, status):
    """Return title with status set or cleared."""
    base, _ = _extract_status(title)
    return f"{base} - {status}" if status else base


def _get_current_session(socket):
    """Look up the current tab's session info via KITTY_WINDOW_ID."""
    win_id_str = os.environ.get("KITTY_WINDOW_ID", "")
    if not win_id_str:
        return None
    try:
        target_win_id = int(win_id_str)
    except ValueError:
        return None

    result = kitty_cmd(socket, "ls")
    if not result or result.returncode != 0:
        return None
    try:
        data = json.loads(result.stdout)
    except (json.JSONDecodeError, TypeError):
        return None

    target_tab = None
    for os_win in data:
        for tab in os_win.get("tabs", []):
            for win in tab.get("windows", []):
                if win.get("id") == target_win_id:
                    target_tab = tab
                    break
            if target_tab:
                break
        if target_tab:
            break

    if target_tab is None:
        return None

    raw_title = target_tab.get("title", "")
    title = _strip_icons(raw_title)
    for win in target_tab.get("windows", []):
        uv = win.get("user_vars", {})
        if uv.get("cc_account"):
            return {
                "tab_id": target_tab.get("id", 0),
                "title": title,
                "raw_title": raw_title,
                "account": uv.get("cc_account", ""),
            }

    return None


def _draw(stdscr, session, statuses, selected, custom_mode, custom_value, custom_cursor, confirm_delete, delete_target):
    """Render the status picker popup."""
    stdscr.erase()
    rows, cols = stdscr.getmaxyx()

    current_status = _extract_status(
        _strip_override_marker(session["title"])
    )[1]

    # Items: presets + Custom... + Clear status
    total_items = len(statuses) + 2
    box_w = min(cols - 4, 40)
    box_h = total_items + 6  # border-title + subtitle + blank + items + blank + footer
    box_y = max(0, (rows - box_h) // 2)
    box_x = max(0, (cols - box_w) // 2)

    dim_attr = curses.color_pair(CP_DIM)
    blank_line = " " * box_w
    for dy in range(box_h):
        safe_addstr(stdscr, box_y + dy, box_x, blank_line, dim_attr)
    draw_border(stdscr, box_y, box_x, box_w, box_h, curses.color_pair(CP_BORDER) | curses.A_BOLD)

    # Header
    header = "Set Tab Status"
    safe_addstr(
        stdscr, box_y, box_x + max(0, (box_w - len(header)) // 2),
        header, curses.color_pair(CP_HEADER) | curses.A_BOLD,
    )

    # Subtitle
    base_title = _extract_status(_strip_override_marker(session["title"]))[0]
    sub = f'for "{base_title}"'
    safe_addstr(
        stdscr, box_y + 1, box_x + max(0, (box_w - len(sub)) // 2),
        sub, dim_attr,
    )

    normal_attr = curses.color_pair(CP_NORMAL)
    item_y = box_y + 3

    # Presets
    for i, preset in enumerate(statuses):
        y = item_y + i
        if y >= rows - 1:
            break
        sel = i == selected
        if confirm_delete and i == delete_target:
            label = f'  Delete "{preset}"? y/n'
            attr = curses.color_pair(CP_WARN) | curses.A_BOLD
        else:
            marker = "▸ " if sel else "  "
            check = "✓ " if current_status == preset else "  "
            label = f"{marker}{check}{preset}"
            attr = normal_attr | (curses.A_BOLD if sel else 0)
        safe_addstr(stdscr, y, box_x + 2, label, attr)

    # Custom option
    custom_idx = len(statuses)
    custom_y = item_y + custom_idx
    if custom_y < rows - 1:
        sel = selected == custom_idx
        marker = "▸ " if sel else "  "
        if custom_mode:
            prefix = f"{marker}  Custom: "
            input_attr = curses.color_pair(CP_INPUT)
            safe_addstr(stdscr, custom_y, box_x + 2, prefix, normal_attr | curses.A_BOLD)
            # Draw input field
            field_x = box_x + 2 + len(prefix)
            field_w = box_w - len(prefix) - 4
            display = custom_value[:field_w]
            safe_addstr(stdscr, custom_y, field_x, display + " " * max(0, field_w - len(display)), input_attr)
            # Cursor
            cursor_x = field_x + min(custom_cursor, field_w - 1)
            curses.curs_set(1)
            try:
                stdscr.move(custom_y, cursor_x)
            except curses.error:
                pass
        else:
            curses.curs_set(0)
            check = "✓ " if current_status and current_status not in statuses else "  "
            label = f"{marker}{check}Custom..."
            attr = normal_attr | (curses.A_BOLD if sel else 0)
            safe_addstr(stdscr, custom_y, box_x + 2, label, attr)

    # Clear option
    clear_idx = len(statuses) + 1
    clear_y = item_y + clear_idx
    if clear_y < rows - 1:
        sel = selected == clear_idx
        marker = "▸ " if sel else "  "
        label = f"{marker}  Clear status"
        attr = normal_attr | (curses.A_BOLD if sel else 0)
        safe_addstr(stdscr, clear_y, box_x + 2, label, attr)

    # Footer
    footer_y = item_y + total_items + 1
    if footer_y < rows - 1:
        status_display = current_status or "(none)"
        footer_text = f"Current: {status_display}"
        safe_addstr(stdscr, footer_y, box_x + 2, footer_text, dim_attr)
        if not custom_mode:
            hint = "d Delete"
            hint_x = box_x + box_w - len(hint) - 2
            if hint_x > box_x + 2 + len(footer_text) + 2:
                safe_addstr(stdscr, footer_y, hint_x, hint, dim_attr)


def _run(stdscr, session):
    """Curses event loop for the tab status overlay."""
    curses.set_escdelay(25)
    curses.curs_set(0)
    init_colors()
    stdscr.timeout(-1)

    statuses = _load_statuses()

    current_status = _extract_status(
        _strip_override_marker(session["title"])
    )[1]

    total = len(statuses) + 2  # presets + Custom + Clear

    # Pre-select current status if it matches a preset
    selected = len(statuses)  # default to Custom
    if current_status:
        for i, p in enumerate(statuses):
            if p == current_status:
                selected = i
                break

    custom_mode = False
    custom_value = ""
    custom_cursor = 0
    confirm_delete = False
    delete_target = -1

    while True:
        _draw(stdscr, session, statuses, selected, custom_mode, custom_value, custom_cursor, confirm_delete, delete_target)
        stdscr.refresh()

        key = stdscr.getch()
        if key == -1:
            continue

        if custom_mode:
            if key == 27:  # Esc - exit custom mode
                custom_mode = False
                curses.curs_set(0)
            elif key in (10, 13, curses.KEY_ENTER):
                stripped = custom_value.strip()
                if stripped:
                    return ("custom", stripped)
            elif key in (127, 8, curses.KEY_BACKSPACE):
                if custom_cursor > 0:
                    custom_value = custom_value[:custom_cursor - 1] + custom_value[custom_cursor:]
                    custom_cursor -= 1
            elif key == curses.KEY_LEFT:
                if custom_cursor > 0:
                    custom_cursor -= 1
            elif key == curses.KEY_RIGHT:
                if custom_cursor < len(custom_value):
                    custom_cursor += 1
            elif key == curses.KEY_HOME or key == 1:  # Ctrl+A
                custom_cursor = 0
            elif key == curses.KEY_END or key == 5:  # Ctrl+E
                custom_cursor = len(custom_value)
            elif 32 <= key <= 126:
                custom_value = custom_value[:custom_cursor] + chr(key) + custom_value[custom_cursor:]
                custom_cursor += 1
        elif confirm_delete:
            if key == ord("y"):
                statuses.pop(delete_target)
                _save_tab_statuses(statuses)
                total = len(statuses) + 2
                if selected >= len(statuses):
                    selected = max(0, len(statuses) - 1)
            confirm_delete = False
            delete_target = -1
        else:
            if key in (27, ord("q"), ord("Q")):
                return None
            elif key in (curses.KEY_UP, ord("k"), ord("K")):
                selected = (selected - 1) % total
            elif key in (curses.KEY_DOWN, ord("j"), ord("J")):
                selected = (selected + 1) % total
            elif key in (10, 13, curses.KEY_ENTER):
                if selected < len(statuses):
                    return ("preset", statuses[selected])
                elif selected == len(statuses):
                    # Custom mode
                    custom_mode = True
                    custom_value = ""
                    custom_cursor = 0
                else:
                    # Clear
                    return ("clear", "")
            elif key in (ord("d"), ord("x")):
                if selected < len(statuses):
                    confirm_delete = True
                    delete_target = selected


def main(args=None):
    load_accounts()
    load_defaults()
    socket = os.environ.get("KITTY_LISTEN_ON", "") or get_kitty_socket()
    session = _get_current_session(socket)
    if session is None:
        return ""

    result = curses.wrapper(_run, session)
    if result is not None:
        kind, value = result
        title = _strip_override_marker(session["title"])
        had_override = session["title"] != title
        new_title = _set_status(title, value if value else None)
        if had_override:
            new_title = _append_override_marker(new_title)
        do_rename(socket, session["tab_id"], new_title)

        # Auto-save custom values to config (deduplicated)
        if kind == "custom" and value:
            statuses = _load_statuses()
            if value not in statuses:
                statuses.append(value)
                _save_tab_statuses(statuses)
    return ""


if __name__ == "__main__":
    main()
