"""Rendering helpers for the keybindings overlay."""

from __future__ import annotations

import contextlib
import curses

from cc_dashboard.colors import CP_DIM, CP_HEADER, CP_MATCH, CP_NORMAL
from cc_dashboard.drawing import safe_addstr


def draw_highlighted(
    stdscr: curses.window,
    y: int,
    x: int,
    text: str,
    base_attr: int,
    query: str,
    match_attr: int,
    max_width: int,
) -> None:
    """Draw text with matching substrings highlighted."""
    text = text[:max_width]
    if not query:
        safe_addstr(stdscr, y, x, text, base_attr)
        return

    lowered_query = query.lower()
    lowered_text = text.lower()
    position = 0
    cursor_x = x
    while position < len(text):
        match_index = lowered_text.find(lowered_query, position)
        if match_index == -1:
            safe_addstr(stdscr, y, cursor_x, text[position:], base_attr)
            break
        if match_index > position:
            safe_addstr(stdscr, y, cursor_x, text[position:match_index], base_attr)
            cursor_x += match_index - position
        safe_addstr(
            stdscr,
            y,
            cursor_x,
            text[match_index : match_index + len(lowered_query)],
            match_attr,
        )
        cursor_x += len(lowered_query)
        position = match_index + len(lowered_query)


def draw_overlay(
    stdscr: curses.window,
    lines: list[tuple[str, str, str]],
    scroll: int,
    search_query: str,
    match_lines: list[int],
    match_index: int,
    search_active: bool,
    search_cursor: int,
) -> None:
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
    visible = lines[scroll : scroll + content_height]
    match_set = set(match_lines)
    current_match_line = match_lines[match_index] if match_lines else -1

    for row_index, (kind, text1, text2) in enumerate(visible):
        y = row_index + 1
        line_index = scroll + row_index
        is_current = line_index == current_match_line
        if kind == "header":
            if line_index in match_set and search_query:
                highlight_attr = current_match_attr if is_current else match_attr
                draw_highlighted(
                    stdscr,
                    y,
                    2,
                    text1,
                    header_attr,
                    search_query,
                    highlight_attr,
                    cols - 3,
                )
            else:
                safe_addstr(stdscr, y, 2, text1, header_attr)
            continue

        if kind == "binding":
            if line_index in match_set and search_query:
                highlight_attr = current_match_attr if is_current else match_attr
                draw_highlighted(
                    stdscr,
                    y,
                    key_col,
                    text1,
                    normal_attr,
                    search_query,
                    highlight_attr,
                    desc_col - key_col - 1,
                )
                draw_highlighted(
                    stdscr,
                    y,
                    desc_col,
                    text2,
                    dim_attr,
                    search_query,
                    highlight_attr,
                    cols - desc_col - 1,
                )
            else:
                safe_addstr(stdscr, y, key_col, text1, normal_attr)
                safe_addstr(stdscr, y, desc_col, text2[: cols - desc_col - 1], dim_attr)

    if search_active:
        prompt = f"/{search_query}"
        safe_addstr(stdscr, rows - 1, 2, prompt, curses.color_pair(CP_NORMAL))
        curses.curs_set(1)
        with contextlib.suppress(curses.error):
            stdscr.move(rows - 1, 2 + 1 + search_cursor)
        return

    curses.curs_set(0)
    end = min(scroll + content_height, len(lines))
    position = f"[{scroll + 1}-{end}/{len(lines)}]"
    hints = "j/k d/u g/G scroll  / search"
    if match_lines:
        hints += f"  n/N match ({match_index + 1}/{len(match_lines)})"
    hints += "  q quit"
    safe_addstr(stdscr, rows - 2, 2, f"{hints}  {position}", dim_attr)
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
