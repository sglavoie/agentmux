"""Filter and sorting picker overlays."""

import curses

from cc_dashboard.colors import CP_DIM, CP_NORMAL
from cc_dashboard.drawing import safe_addstr
from cc_dashboard.filters import SORT_MODES
from cc_dashboard.views_overlays_pickers_shared import (
    centered_box,
    draw_overlay_box,
    draw_overlay_title,
)

_SORT_DESCRIPTIONS = {
    "default": "Default Kitty tab-bar order",
    "name": "Alphabetical by title",
    "account": "Grouped by account, alpha within",
    "group": "Grouped by session group, alpha within",
    "status": "Error -> permission -> active -> idle -> stopped",
    "idle": "Most idle first",
    "activity": "Most recently active first",
}

_SORT_DISPLAY_LABELS = {
    "default": "Tab order",
    "name": "Name",
    "account": "Account",
    "group": "Group",
    "status": "Status",
    "idle": "Idle",
    "activity": "Activity",
}


def draw_sort_picker(stdscr, selected, current_mode):
    """Draw a centered sort mode picker overlay."""
    rows, cols = stdscr.getmaxyx()
    label_col = max(len(value) for value in _SORT_DISPLAY_LABELS.values()) + 2

    display_rows = []
    for mode in SORT_MODES:
        check = "* " if mode == current_mode else "  "
        label = _SORT_DISPLAY_LABELS[mode].ljust(label_col)
        display_rows.append(f"{check}{label}{_SORT_DESCRIPTIONS[mode]}")

    max_row_len = max(len(row) for row in display_rows)
    box_w = min(cols - 4, max(40, max_row_len + 10))
    box_h = len(SORT_MODES) + 3
    _, _, box_y, box_x = centered_box(stdscr, box_w, box_h)

    draw_overlay_box(stdscr, box_y, box_x, box_w, box_h)
    draw_overlay_title(stdscr, box_y, box_x, box_w, "Sort By")

    normal_attr = curses.color_pair(CP_NORMAL)
    for i, display in enumerate(display_rows):
        y = box_y + 2 + i
        if y >= rows - 1:
            break
        marker = "> " if i == selected else "  "
        attr = normal_attr | (curses.A_BOLD if i == selected else 0)
        safe_addstr(stdscr, y, box_x + 2, f"{marker}{display}"[: box_w - 4], attr)


def draw_group_picker(stdscr, sessions, group_filter):
    """Draw a centered group filter picker overlay.

    Works for both dashboard sessions and config sessions -- the only
    requirement is that each item has a "group" key.
    """
    rows, cols = stdscr.getmaxyx()
    groups = sorted({session.get("group", "") or "(ungrouped)" for session in sessions})
    if not groups:
        return

    display_rows = []
    for i, name in enumerate(groups):
        if i >= 9:
            break
        enabled = group_filter is None or name in group_filter
        check = "[x] " if enabled else "[ ] "
        display_rows.append((f"{i + 1}: {check}{name}", enabled))

    max_row_len = max(len(row[0]) for row in display_rows)
    box_w = min(cols - 4, max(30, max_row_len + 8))
    box_h = len(display_rows) + 3
    _, _, box_y, box_x = centered_box(stdscr, box_w, box_h)

    dim_attr = draw_overlay_box(stdscr, box_y, box_x, box_w, box_h)
    draw_overlay_title(stdscr, box_y, box_x, box_w, "Groups")

    normal_attr = curses.color_pair(CP_NORMAL)
    for i, (display, enabled) in enumerate(display_rows):
        y = box_y + 2 + i
        if y >= rows - 1:
            break
        attr = normal_attr | curses.A_BOLD if enabled else dim_attr
        safe_addstr(stdscr, y, box_x + 2, display[: box_w - 4], attr)
