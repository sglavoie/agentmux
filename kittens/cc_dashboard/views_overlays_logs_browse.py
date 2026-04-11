"""Activity log browse overlay."""

from __future__ import annotations

import curses
import time
from typing import Any

from cc_dashboard.colors import CP_DIM, CP_HEADER, CP_MATCH, CP_NORMAL
from cc_dashboard.drawing import _pad_to_width, safe_addstr
from cc_dashboard.views_shared import _draw_peek_search_bar, draw_footer


def draw_actlog_browse(
    stdscr: curses.window,
    entries: list[dict[str, Any]],
    selected: int,
    scroll: int,
    search_query: str = "",
    search_active: bool = False,
    search_cursor: int = 0,
    match_line_indices: list[int] | None = None,
    match_index: int = 0,
) -> None:
    """Draw a tabular listing of all activity log files."""
    stdscr.clear()
    rows, cols = stdscr.getmaxyx()
    header_attr = curses.color_pair(CP_HEADER) | curses.A_BOLD
    normal_attr = curses.color_pair(CP_NORMAL)
    dim_attr = curses.color_pair(CP_DIM)
    match_attr = curses.color_pair(CP_MATCH)

    if match_line_indices is None:
        match_line_indices = []
    match_set = set(match_line_indices)

    safe_addstr(stdscr, 0, 2, "Activity Logs -- Browse All", header_attr)

    if not entries:
        safe_addstr(stdscr, 2, 4, "No activity log files found", dim_attr)
        draw_footer(stdscr, "actlog_browse")
        return

    id_w = 14
    mtime_w = 18
    count_w = 8
    size_w = 10
    fixed_w = id_w + mtime_w + count_w + 2 + size_w + 5
    title_w = max(20, cols - fixed_w)

    hdr = (
        "Session ID".ljust(id_w)
        + "Title".ljust(title_w)
        + "Modified".ljust(mtime_w)
        + "Events".rjust(count_w)
        + "  "
        + "Size".rjust(size_w)
    )
    header_y = 2
    safe_addstr(stdscr, header_y, 4, hdr[: cols - 5], dim_attr)

    content_height = rows - header_y - 3
    visible = entries[scroll : scroll + content_height]

    for i, entry in enumerate(visible):
        y = header_y + 1 + i
        if y >= rows - 2:
            break

        abs_idx = scroll + i
        is_selected = abs_idx == selected
        is_match = abs_idx in match_set
        if is_selected:
            attr = normal_attr | curses.A_REVERSE
        elif is_match:
            attr = match_attr
        else:
            attr = normal_attr

        sid_short = entry["session_id"][: id_w - 2]
        title = (entry.get("title", "") or "ended")[: title_w - 2]
        mtime_str = time.strftime("%Y-%m-%d %H:%M", time.localtime(entry["mtime"]))
        count_str = str(entry["event_count"])

        sz = entry["size"]
        if sz < 1024:
            size_str = f"{sz} B"
        elif sz < 1024 * 1024:
            size_str = f"{sz / 1024:.1f} KB"
        else:
            size_str = f"{sz / (1024 * 1024):.1f} MB"

        row_text = (
            sid_short.ljust(id_w)
            + title.ljust(title_w)
            + mtime_str.ljust(mtime_w)
            + count_str.rjust(count_w)
            + "  "
            + size_str.rjust(size_w)
        )
        safe_addstr(stdscr, y, 4, _pad_to_width(row_text[: cols - 5], cols - 5), attr)

    total = len(entries)
    if search_active:
        _draw_peek_search_bar(
            stdscr,
            search_query,
            search_cursor,
            len(match_line_indices),
        )
    elif match_line_indices:
        indicator = f"[match {match_index + 1}/{len(match_line_indices)}]"
        if total > content_height:
            pos = f"[{scroll + 1}-{min(scroll + content_height, total)}/{total}]"
            safe_addstr(stdscr, rows - 2, 2, f"{indicator}  {pos}", dim_attr)
        else:
            safe_addstr(stdscr, rows - 2, 2, indicator, dim_attr)
    elif total > content_height:
        pos = f"[{scroll + 1}-{min(scroll + content_height, total)}/{total}]"
        safe_addstr(stdscr, rows - 2, 2, pos, dim_attr)

    draw_footer(stdscr, "actlog_browse_search" if search_active else "actlog_browse")
