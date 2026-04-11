"""Primary activity log viewer overlay."""

from __future__ import annotations

import curses
from typing import Any

from cc_dashboard.colors import (
    CP_ATTENTION,
    CP_DIM,
    CP_HEADER,
    CP_MATCH,
    CP_NORMAL,
    CP_WARN,
)
from cc_dashboard.drawing import safe_addstr
from cc_dashboard.views_shared import (
    _draw_peek_search_bar,
    _render_peek_line,
    draw_footer,
)


def draw_actlog_view(
    stdscr: curses.window,
    title: str,
    lines: list[str],
    scroll: int,
    summary: dict[str, Any] | None,
    search_query: str = "",
    search_active: bool = False,
    search_cursor: int = 0,
    match_line_indices: list[int] | None = None,
    match_index: int = 0,
    selected: int = -1,
) -> None:
    """Draw a full-screen scrollable activity log viewer."""
    stdscr.clear()
    rows, cols = stdscr.getmaxyx()
    header_attr = curses.color_pair(CP_HEADER) | curses.A_BOLD
    normal_attr = curses.color_pair(CP_NORMAL)
    dim_attr = curses.color_pair(CP_DIM)
    match_attr = curses.color_pair(CP_MATCH)
    span_attr = curses.color_pair(CP_WARN) | curses.A_BOLD
    current_attr = curses.color_pair(CP_ATTENTION) | curses.A_BOLD

    if match_line_indices is None:
        match_line_indices = []
    match_set = set(match_line_indices)
    current_line_idx = match_line_indices[match_index] if match_line_indices else -1

    safe_addstr(stdscr, 0, 2, f"Activity Log: {title}", header_attr)

    if summary:
        total = summary.get("total", 0)
        stop_count = summary.get("stop_count", 0)
        tool_counts = summary.get("tool_counts", [])
        top_tools = "  ".join(f"{name}:{cnt}" for name, cnt in tool_counts[:5])
        stat_line = f"Events: {total}  Stops: {stop_count}"
        if top_tools:
            stat_line += f"  Top: {top_tools}"
        safe_addstr(stdscr, 1, 2, stat_line[: cols - 3], dim_attr)

        duration = summary.get("duration", "")
        if duration:
            safe_addstr(stdscr, 2, 2, f"Duration: {duration}", dim_attr)

    content_start = 3
    content_height = rows - content_start - 2

    if not lines:
        safe_addstr(stdscr, content_start, 2, "(no log entries)", dim_attr)
    else:
        visible = lines[scroll : scroll + content_height]
        for i, line in enumerate(visible):
            abs_idx = scroll + i
            if abs_idx == selected:
                safe_addstr(
                    stdscr,
                    content_start + i,
                    0,
                    line[: cols - 1],
                    normal_attr | curses.A_REVERSE,
                )
                continue
            _render_peek_line(
                stdscr,
                content_start + i,
                line,
                cols,
                search_query,
                abs_idx in match_set,
                abs_idx == current_line_idx,
                normal_attr,
                match_attr,
                span_attr,
                current_attr,
            )

    total = len(lines) if lines else 0
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
        safe_addstr(stdscr, rows - 2, 2, f"j/k to scroll  {pos}", dim_attr)

    draw_footer(stdscr, "actlog_search" if search_active else "actlog")
