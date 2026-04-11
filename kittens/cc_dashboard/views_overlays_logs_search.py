"""Global activity log search overlay."""

from __future__ import annotations

import curses
import time
from typing import Any

from cc_dashboard.colors import CP_DIM, CP_HEADER, CP_INPUT_ACTIVE, CP_NORMAL, CP_WARN
from cc_dashboard.drawing import _pad_to_width, safe_addstr
from cc_dashboard.views_shared import _render_peek_line, draw_footer


def draw_actlog_gsearch(
    stdscr: curses.window,
    query: str,
    cursor: int,
    results: list[dict[str, Any]],
    selected: int,
    scroll: int,
    loading: bool = False,
    total_scanned: int = 0,
    log_index: list[dict[str, Any]] | None = None,
    search_active: bool = True,
) -> None:
    """Draw the global activity log search view."""
    del total_scanned

    stdscr.clear()
    rows, cols = stdscr.getmaxyx()
    header_attr = curses.color_pair(CP_HEADER) | curses.A_BOLD
    normal_attr = curses.color_pair(CP_NORMAL)
    dim_attr = curses.color_pair(CP_DIM)
    warn_attr = curses.color_pair(CP_WARN)
    span_attr = curses.color_pair(CP_WARN) | curses.A_BOLD

    safe_addstr(stdscr, 0, 2, "Global Log Search", header_attr)

    prefix = "/ "
    safe_addstr(stdscr, 1, 2, prefix, warn_attr)
    x = 2 + len(prefix)
    before = query[:cursor]
    after = query[cursor:]
    cursor_ch = after[0] if after else " "
    after_rest = after[1:] if len(after) > 1 else ""

    input_attr = curses.color_pair(CP_INPUT_ACTIVE)
    if search_active:
        safe_addstr(stdscr, 1, x, before, input_attr)
        safe_addstr(
            stdscr, 1, x + len(before), cursor_ch, input_attr | curses.A_REVERSE
        )
        if after_rest:
            safe_addstr(stdscr, 1, x + len(before) + 1, after_rest, input_attr)
    else:
        safe_addstr(stdscr, 1, x, query, input_attr)

    result_x = x + len(query) + 2
    n_files = len(log_index) if log_index else 0
    if results:
        info = f"({len(results)} results in {n_files} files)"
        safe_addstr(stdscr, 1, result_x, info, dim_attr)
    elif query and len(query) >= 3 and not loading:
        info = f"(0 results in {n_files} files)"
        safe_addstr(stdscr, 1, result_x, info, dim_attr)

    if loading:
        safe_addstr(stdscr, 2, 4, "Loading...", warn_attr)
    elif not query or len(query) < 3:
        safe_addstr(stdscr, 2, 4, "(type 3+ chars to search)", dim_attr)

    title_w = min(20, max(10, cols // 5))
    safe_addstr(stdscr, 3, 4, "Session".ljust(title_w) + "Time      Details", dim_attr)

    content_start = 4
    content_height = rows - content_start - 2
    if content_height < 1:
        draw_footer(
            stdscr, "actlog_gsearch_search" if search_active else "actlog_gsearch"
        )
        return

    visible = results[scroll : scroll + content_height]
    for i, result in enumerate(visible):
        y = content_start + i
        if y >= rows - 2:
            break

        abs_idx = scroll + i
        is_selected = abs_idx == selected

        title_short = (result.get("title", "") or "?")[: title_w - 2]
        ts = result.get("ts")
        time_str = time.strftime("%H:%M:%S", time.localtime(ts)) if ts else "??:??:??"
        detail = result.get("formatted_line", "")
        if detail.startswith(time_str):
            detail = detail[len(time_str) :].lstrip()

        row_text = title_short.ljust(title_w) + time_str + "  " + detail
        clipped = row_text[: cols - 5]

        attr = normal_attr | curses.A_REVERSE if is_selected else normal_attr

        if query and not is_selected:
            _render_peek_line(
                stdscr,
                y,
                " " * 4 + clipped,
                cols,
                query,
                True,
                False,
                normal_attr,
                normal_attr,
                span_attr,
                span_attr,
            )
        else:
            safe_addstr(stdscr, y, 4, _pad_to_width(clipped, cols - 5), attr)

    total = len(results)
    if total > content_height:
        pos = f"[{scroll + 1}-{min(scroll + content_height, total)}/{total}]"
        safe_addstr(stdscr, rows - 2, 2, pos, dim_attr)

    draw_footer(stdscr, "actlog_gsearch_search" if search_active else "actlog_gsearch")
