"""Peek and config content overlay views."""

import curses

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
    _shorten_home,
    draw_footer,
)


def draw_config_show(stdscr, label, path, lines, scroll):
    """Draw a full-screen scrollable file viewer for a config file."""
    stdscr.clear()
    rows, cols = stdscr.getmaxyx()
    header_attr = curses.color_pair(CP_HEADER) | curses.A_BOLD
    normal_attr = curses.color_pair(CP_NORMAL)
    dim_attr = curses.color_pair(CP_DIM)

    safe_addstr(stdscr, 0, 2, f"{label}: {_shorten_home(path)}", header_attr)
    content_height = rows - 3

    if not lines or (len(lines) == 1 and lines[0] == "(file not found)"):
        display_lines = lines if lines else ["(file not found)"]
        for i, line in enumerate(display_lines[:content_height]):
            safe_addstr(stdscr, i + 1, 2, line[: cols - 3], dim_attr)
    else:
        for i, line in enumerate(lines[scroll : scroll + content_height]):
            safe_addstr(stdscr, i + 1, 2, line[: cols - 3], normal_attr)

    total = len(lines) if lines else 0
    if total > content_height:
        pos = f"[{scroll + 1}-{min(scroll + content_height, total)}/{total}]"
        safe_addstr(stdscr, rows - 2, 2, f"j/k to scroll  {pos}", dim_attr)

    safe_addstr(stdscr, rows - 1, 2, "q/Esc Close  R Reload config", dim_attr)


def draw_peek_overlay(
    stdscr,
    session_title,
    lines,
    scroll,
    extent,
    error="",
    search_query="",
    search_active=False,
    search_cursor=0,
    match_line_indices=None,
    match_index=0,
):
    """Draw a full-screen scrollable view of a session's terminal output."""
    stdscr.clear()
    rows, cols = stdscr.getmaxyx()
    header_attr = curses.color_pair(CP_HEADER) | curses.A_BOLD
    normal_attr = curses.color_pair(CP_NORMAL)
    dim_attr = curses.color_pair(CP_DIM)
    warn_attr = curses.color_pair(CP_WARN)
    match_attr = curses.color_pair(CP_MATCH)
    span_attr = curses.color_pair(CP_WARN) | curses.A_BOLD
    current_attr = curses.color_pair(CP_ATTENTION) | curses.A_BOLD

    if match_line_indices is None:
        match_line_indices = []
    match_set = set(match_line_indices)
    current_line_idx = match_line_indices[match_index] if match_line_indices else -1

    extent_label = "scrollback" if extent == "all" else "screen"
    safe_addstr(stdscr, 0, 2, f"Peek: {session_title} [{extent_label}]", header_attr)

    content_height = rows - 3
    if error:
        safe_addstr(stdscr, 2, 2, error, warn_attr)
    elif not lines or (len(lines) == 1 and not lines[0].strip()):
        safe_addstr(stdscr, 2, 2, "(empty output)", dim_attr)
    else:
        for i, line in enumerate(lines[scroll : scroll + content_height]):
            abs_idx = scroll + i
            _render_peek_line(
                stdscr,
                i + 1,
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

    draw_footer(stdscr, "peek_search" if search_active else "peek")
