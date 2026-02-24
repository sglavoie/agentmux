#!/usr/bin/env python3
"""Lightweight fuzzy-finder overlay for quick Claude session switching.

Displays a search bar and filtered list of running Claude sessions.
Type to filter by title, account, or cwd; arrows or ctrl+n/ctrl+p to
navigate; Enter to focus the selected tab; Escape to dismiss.
"""

import curses
import os
import sys

if "__file__" in dir():
    sys.path.insert(0, os.path.dirname(os.path.realpath(__file__)))

from cc_dashboard.config import load_accounts
from cc_dashboard.kitty_remote import do_focus, get_kitty_socket, query_sessions
from cc_dashboard.ui import (
    CP_DIM,
    CP_HEADER,
    CP_INPUT_ACTIVE,
    CP_WARN,
    _cp_for_account,
    _filter_sessions,
    _pad_to_width,
    init_colors,
    safe_addstr,
    shorten_path,
)


def draw_search_input(stdscr, query, cursor_pos, match_count, total_count):
    """Draw the always-active search bar at the top of the overlay."""
    rows, cols = stdscr.getmaxyx()
    y = 1

    prefix = " > "
    safe_addstr(stdscr, y, 1, prefix, curses.color_pair(CP_WARN) | curses.A_BOLD)

    x = 1 + len(prefix)
    before = query[:cursor_pos]
    after = query[cursor_pos:]
    cursor_ch = after[0] if after else " "
    after_rest = after[1:] if len(after) > 1 else ""

    safe_addstr(stdscr, y, x, before, curses.color_pair(CP_INPUT_ACTIVE))
    safe_addstr(
        stdscr,
        y,
        x + len(before),
        cursor_ch,
        curses.color_pair(CP_INPUT_ACTIVE) | curses.A_REVERSE,
    )
    if after_rest:
        safe_addstr(
            stdscr,
            y,
            x + len(before) + 1,
            after_rest,
            curses.color_pair(CP_INPUT_ACTIVE),
        )

    hint = f"  {match_count}/{total_count}"
    safe_addstr(
        stdscr,
        y,
        x + max(len(query), 1) + 1,
        hint,
        curses.color_pair(CP_DIM),
    )


def draw_session_list(stdscr, sessions, selected):
    """Draw the filtered session list below the search bar."""
    rows, cols = stdscr.getmaxyx()

    if not sessions:
        safe_addstr(stdscr, 4, 3, "No matching sessions.", curses.color_pair(CP_DIM))
        return

    c_title = 26
    c_acct = 12
    c_dir = max(12, cols - c_title - c_acct - 6)

    hdr = f"  {'Title':<{c_title}}{'Account':<{c_acct}}{'Directory':<{c_dir}}"
    safe_addstr(stdscr, 3, 1, hdr, curses.color_pair(CP_HEADER) | curses.A_BOLD)

    avail = rows - 6
    total = len(sessions)

    if total <= avail:
        offset = 0
    else:
        offset = max(0, min(selected - avail // 2, total - avail))

    for i in range(offset, min(offset + avail, total)):
        screen_y = 4 + (i - offset)
        session = sessions[i]
        sel = i == selected

        cursor_mark = ">" if sel else " "
        cp = _cp_for_account(session["account"], selected=sel)
        short_dir = shorten_path(session["cwd"], c_dir - 1)
        title_padded = _pad_to_width(session["title"], c_title)
        line = f"{cursor_mark} {title_padded}{session['account']:<{c_acct}}{short_dir}"

        attr = curses.color_pair(cp)
        if sel:
            attr |= curses.A_BOLD
        safe_addstr(stdscr, screen_y, 1, line, attr)


def draw_footer(stdscr):
    """Draw the keybinding hints at the bottom."""
    rows, cols = stdscr.getmaxyx()
    text = "ctrl+p Up  ctrl+n Down  Enter Switch  Esc Quit"
    safe_addstr(stdscr, rows - 1, 2, text[: cols - 3], curses.color_pair(CP_DIM))


def quick_switch_main(stdscr, socket):
    """Run the quick-switch event loop. Returns the tab_id to focus, or None."""
    curses.curs_set(0)
    init_colors()
    stdscr.timeout(100)

    all_sessions = query_sessions(socket, managed_only=True)
    if not all_sessions:
        stdscr.erase()
        safe_addstr(
            stdscr, 2, 3, "No running Claude sessions found.", curses.color_pair(CP_DIM)
        )
        safe_addstr(stdscr, 3, 3, "Press any key to exit.", curses.color_pair(CP_DIM))
        stdscr.refresh()
        stdscr.timeout(-1)
        stdscr.getch()
        return None

    query = ""
    cursor = 0
    selected = 0
    filtered = list(all_sessions)

    while True:
        stdscr.erase()
        draw_search_input(stdscr, query, cursor, len(filtered), len(all_sessions))
        draw_session_list(stdscr, filtered, selected)
        draw_footer(stdscr)
        stdscr.refresh()

        key = stdscr.getch()
        if key == -1:
            continue

        # Escape — quit
        if key == 27:
            return None

        # Enter — focus selected
        if key in (10, 13, curses.KEY_ENTER):
            if filtered:
                return filtered[selected]["tab_id"]
            return None

        # Navigation: arrow keys, ctrl+n/ctrl+p, ctrl+k
        if key in (curses.KEY_UP, 16) and filtered:  # 16=ctrl+p
            selected = (selected - 1) % len(filtered)
            continue
        if key in (curses.KEY_DOWN, 14) and filtered:  # 14=ctrl+n
            selected = (selected + 1) % len(filtered)
            continue

        # Backspace
        if key in (curses.KEY_BACKSPACE, 127, 8):
            if cursor > 0:
                query = query[: cursor - 1] + query[cursor:]
                cursor -= 1
                filtered = _filter_sessions(all_sessions, query)
                selected = min(selected, max(0, len(filtered) - 1))
            continue

        # Cursor movement within the search input
        if key == curses.KEY_LEFT:
            cursor = max(0, cursor - 1)
            continue
        if key == curses.KEY_RIGHT:
            cursor = min(len(query), cursor + 1)
            continue

        # Ctrl+a / Ctrl+e for home/end in search input
        if key == 1:  # ctrl+a
            cursor = 0
            continue
        if key == 5:  # ctrl+e
            cursor = len(query)
            continue

        # Ctrl+u to clear input
        if key == 21:  # ctrl+u
            query = ""
            cursor = 0
            filtered = list(all_sessions)
            selected = 0
            continue

        # Printable characters
        if 32 <= key < 127:
            query = query[:cursor] + chr(key) + query[cursor:]
            cursor += 1
            filtered = _filter_sessions(all_sessions, query)
            selected = min(selected, max(0, len(filtered) - 1))
            continue

    return None


def main(args=None):
    load_accounts()

    socket = get_kitty_socket()
    if not socket:
        print(
            "Error: No kitty socket found. "
            "Is Kitty running with remote control enabled?",
            file=sys.stderr,
        )
        sys.exit(1)

    result_tab_id = None

    def run(stdscr):
        nonlocal result_tab_id
        result_tab_id = quick_switch_main(stdscr, socket)

    curses.wrapper(run)

    if result_tab_id is not None:
        do_focus(socket, result_tab_id)


if __name__ == "__main__":
    main()
