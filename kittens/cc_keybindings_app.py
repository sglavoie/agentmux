"""Application entry point for the keybindings overlay."""

from __future__ import annotations

import curses

from cc_dashboard.colors import init_colors
from cc_dashboard.config import load_accounts
from cc_keybindings_data import LINES, compute_matches, scroll_to_line
from cc_keybindings_ui import draw_overlay


def run_overlay(stdscr: curses.window) -> None:
    """Event loop for the keybinding overlay."""
    curses.set_escdelay(25)
    curses.curs_set(0)
    init_colors()
    stdscr.timeout(-1)

    scroll = 0
    rows = stdscr.getmaxyx()[0]
    content_height = max(1, rows - 3)
    max_scroll = max(0, len(LINES) - content_height)

    search_active = False
    search_query = ""
    search_cursor = 0
    match_lines: list[int] = []
    match_index = 0

    while True:
        rows = stdscr.getmaxyx()[0]
        content_height = max(1, rows - 3)
        max_scroll = max(0, len(LINES) - content_height)
        scroll = min(scroll, max_scroll)

        draw_overlay(
            stdscr,
            LINES,
            scroll,
            search_query,
            match_lines,
            match_index,
            search_active,
            search_cursor,
        )
        stdscr.refresh()

        key = stdscr.getch()
        if key == -1:
            continue

        if search_active:
            if key == 27:
                search_active = False
                search_query = ""
                search_cursor = 0
                match_lines = []
                match_index = 0
            elif key in (10, 13, curses.KEY_ENTER):
                search_active = False
                if match_lines:
                    scroll = scroll_to_line(
                        match_lines[match_index],
                        scroll,
                        content_height,
                        max_scroll,
                    )
            elif key in (curses.KEY_BACKSPACE, 127, 8):
                if search_cursor > 0:
                    search_query = (
                        search_query[: search_cursor - 1] + search_query[search_cursor:]
                    )
                    search_cursor -= 1
                    match_lines = compute_matches(LINES, search_query)
                    match_index = 0
            elif key == curses.KEY_LEFT:
                if search_cursor > 0:
                    search_cursor -= 1
            elif key == curses.KEY_RIGHT:
                if search_cursor < len(search_query):
                    search_cursor += 1
            elif 32 <= key <= 126:
                character = chr(key)
                search_query = (
                    search_query[:search_cursor]
                    + character
                    + search_query[search_cursor:]
                )
                search_cursor += 1
                match_lines = compute_matches(LINES, search_query)
                match_index = 0
            continue

        if key in (ord("j"), curses.KEY_DOWN):
            if scroll < max_scroll:
                scroll += 1
            continue

        if key in (ord("k"), curses.KEY_UP):
            if scroll > 0:
                scroll -= 1
            continue

        if key in (ord("d"), 4):
            scroll = min(max_scroll, scroll + content_height // 2)
            continue

        if key in (ord("u"), 21):
            scroll = max(0, scroll - content_height // 2)
            continue

        if key == ord("g"):
            scroll = 0
            continue

        if key == ord("G"):
            scroll = max_scroll
            continue

        if key == ord("/"):
            search_active = True
            search_query = ""
            search_cursor = 0
            match_lines = []
            match_index = 0
            continue

        if key == ord("n") and match_lines:
            match_index = (match_index + 1) % len(match_lines)
            scroll = scroll_to_line(
                match_lines[match_index],
                scroll,
                content_height,
                max_scroll,
            )
            continue

        if key == ord("N") and match_lines:
            match_index = (match_index - 1) % len(match_lines)
            scroll = scroll_to_line(
                match_lines[match_index],
                scroll,
                content_height,
                max_scroll,
            )
            continue

        if key == 27:
            if search_query:
                search_query = ""
                match_lines = []
                match_index = 0
                continue
            break

        if key == ord("q"):
            break


def main(_args: list[str] | None = None) -> None:
    """Run the keybindings overlay."""
    load_accounts()
    curses.wrapper(run_overlay)
