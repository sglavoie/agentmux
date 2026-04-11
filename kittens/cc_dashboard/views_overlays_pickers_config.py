"""Config-related picker overlays."""

import curses
import os

from cc_dashboard import config as _cfg
from cc_dashboard.colors import CP_NORMAL
from cc_dashboard.drawing import safe_addstr
from cc_dashboard.views_overlays_pickers_shared import (
    centered_box,
    draw_overlay_box,
    draw_overlay_title,
)
from cc_dashboard.views_shared import _shorten_home


def draw_override_picker(stdscr, session, override_selected, batch_count=None):
    """Draw a config-dir selection overlay for override."""
    dirs = _cfg._unique_config_dirs()
    if not dirs:
        return
    rows, cols = stdscr.getmaxyx()

    home = os.path.expanduser("~")

    def _short(path):
        if path.startswith(home):
            return "~" + path[len(home) :]
        return path

    display_dirs = [_short(path) for path in dirs]

    current_dir = session.get("config_dir", "")
    if not current_dir:
        current_dir = _cfg._accounts.get(session.get("account", ""), {}).get(
            "config_dir", ""
        )

    max_dir_len = max(len(display) for display in display_dirs)
    box_w = min(cols - 4, max(36, max_dir_len + 20))
    box_h = len(dirs) + 4
    _, _, box_y, box_x = centered_box(stdscr, box_w, box_h)

    dim_attr = draw_overlay_box(stdscr, box_y, box_x, box_w, box_h)
    subtitle = (
        f"for {batch_count} marked session(s)"
        if batch_count is not None
        else f'for "{session["title"]}"'
    )
    draw_overlay_title(
        stdscr, box_y, box_x, box_w, "Override Config Dir", subtitle, dim_attr
    )

    normal_attr = curses.color_pair(CP_NORMAL)
    for i, display in enumerate(display_dirs):
        y = box_y + 3 + i
        if y >= rows - 1:
            break
        suffix = " (current)" if dirs[i] == current_dir else ""
        label = f"{'> ' if i == override_selected else '  '}{display}{suffix}"
        attr = normal_attr | (curses.A_BOLD if i == override_selected else 0)
        safe_addstr(stdscr, y, box_x + 2, label, attr)


def draw_config_picker(stdscr, config_entries, selected):
    """Draw a centered config file picker overlay."""
    if not config_entries:
        return

    display_rows = []
    for label, path in config_entries:
        suffix = "" if os.path.isfile(path) else " (missing)"
        display_rows.append(f"{label}: {_shorten_home(path)}{suffix}")

    max_row_len = max(len(row) for row in display_rows)
    rows, cols = stdscr.getmaxyx()
    box_w = min(cols - 4, max(40, max_row_len + 10))
    box_h = len(config_entries) + 3
    _, _, box_y, box_x = centered_box(stdscr, box_w, box_h)

    draw_overlay_box(stdscr, box_y, box_x, box_w, box_h)
    draw_overlay_title(stdscr, box_y, box_x, box_w, "Config Files")

    normal_attr = curses.color_pair(CP_NORMAL)
    for i, display in enumerate(display_rows):
        y = box_y + 2 + i
        if y >= rows - 1:
            break
        marker = "> " if i == selected else "  "
        attr = normal_attr | (curses.A_BOLD if i == selected else 0)
        safe_addstr(stdscr, y, box_x + 2, f"{marker}{display}"[: box_w - 4], attr)


def draw_config_action_picker(stdscr, label, path, selected):
    """Draw a centered action picker for a selected config file."""
    actions = ["Show contents", "Edit in editor", "Show file path"]
    subtitle = f"{label}: {_shorten_home(path)}"

    max_action_len = max(len(action) for action in actions)
    rows, cols = stdscr.getmaxyx()
    box_w = min(cols - 4, max(40, max(max_action_len, len(subtitle)) + 10))
    box_h = len(actions) + 4
    _, _, box_y, box_x = centered_box(stdscr, box_w, box_h)

    dim_attr = draw_overlay_box(stdscr, box_y, box_x, box_w, box_h)
    draw_overlay_title(
        stdscr, box_y, box_x, box_w, f"Config: {label}", subtitle, dim_attr
    )

    normal_attr = curses.color_pair(CP_NORMAL)
    for i, action in enumerate(actions):
        y = box_y + 3 + i
        if y >= rows - 1:
            break
        marker = "> " if i == selected else "  "
        attr = normal_attr | (curses.A_BOLD if i == selected else 0)
        safe_addstr(stdscr, y, box_x + 2, f"{marker}{action}", attr)
