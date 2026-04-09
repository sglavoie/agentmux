"""Shared layout helpers for picker-style overlays."""

import curses

from cc_dashboard.colors import CP_BORDER, CP_DIM, CP_HEADER
from cc_dashboard.drawing import draw_border, safe_addstr


def centered_box(stdscr, box_w, box_h):
    """Return centered overlay bounds and current screen size."""
    rows, cols = stdscr.getmaxyx()
    box_y = max(0, (rows - box_h) // 2)
    box_x = max(0, (cols - box_w) // 2)
    return rows, cols, box_y, box_x


def draw_overlay_box(stdscr, box_y, box_x, box_w, box_h):
    """Draw a dimmed boxed overlay and return the dim attribute."""
    blank_line = " " * box_w
    dim_attr = curses.color_pair(CP_DIM)
    for dy in range(box_h):
        safe_addstr(stdscr, box_y + dy, box_x, blank_line, dim_attr)
    draw_border(
        stdscr,
        box_y,
        box_x,
        box_w,
        box_h,
        curses.color_pair(CP_BORDER) | curses.A_BOLD,
    )
    return dim_attr


def draw_overlay_title(
    stdscr, box_y, box_x, box_w, title, subtitle=None, subtitle_attr=None
):
    """Draw centered overlay title and optional subtitle."""
    safe_addstr(
        stdscr,
        box_y,
        box_x + max(0, (box_w - len(title)) // 2),
        title,
        curses.color_pair(CP_HEADER) | curses.A_BOLD,
    )
    if subtitle is not None:
        attr = subtitle_attr if subtitle_attr is not None else curses.color_pair(CP_DIM)
        safe_addstr(
            stdscr,
            box_y + 1,
            box_x + max(0, (box_w - len(subtitle)) // 2),
            subtitle[: box_w - 2],
            attr,
        )
