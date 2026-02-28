#!/usr/bin/env python3
"""Lightweight fuzzy-finder overlay for quick Claude session switching.

Displays a search bar and filtered list of running Claude sessions.
Type to filter by title, account, or cwd; arrows or ctrl+n/ctrl+p to
navigate; Enter to focus the selected tab; Escape to dismiss.
"""

import curses
import os
import sys
import time


def _kitten_dir():
    """Return the directory containing this kitten script."""
    for candidate in (
        globals().get("__file__"),
        sys.argv[0] if sys.argv else None,
    ):
        if candidate and os.path.isfile(candidate):
            return os.path.dirname(os.path.realpath(candidate))
    return os.path.expanduser("~/.config/kitty")


sys.path.insert(0, _kitten_dir())

# Flush cached cc_dashboard modules so Kitty's long-lived Python process
# always picks up the current source files without requiring a restart.
for _mod in list(sys.modules):
    if _mod.startswith("cc_dashboard"):
        del sys.modules[_mod]

from cc_dashboard import config as _cfg
from cc_dashboard.colors import (
    CP_ATTENTION,
    CP_DIM,
    CP_HEADER,
    CP_INPUT_ACTIVE,
    CP_WARN,
    _cp_for_account,
    init_colors,
)
from cc_dashboard.config import load_accounts, load_defaults
from cc_dashboard.drawing import _pad_to_width, safe_addstr, shorten_path
from cc_dashboard.filters import (
    SORT_LABELS,
    SORT_MODES,
    _apply_sort,
    _filter_sessions,
)
from cc_dashboard.remote import do_cross_instance_focus
from cc_dashboard.remote.command import get_kitty_socket
from cc_dashboard.remote.sessions import query_all_sessions
from cc_dashboard.remote.shared import format_duration
from cc_dashboard.views import draw_sort_picker


def draw_search_input(stdscr, query, cursor_pos, match_count, total_count):
    """Draw the always-active search bar at the top of the overlay."""
    _rows, _cols = stdscr.getmaxyx()
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


def draw_session_list(stdscr, sessions, selected, multi_instance=False):
    """Draw the filtered session list below the search bar."""
    rows, cols = stdscr.getmaxyx()

    if not sessions:
        safe_addstr(stdscr, 4, 3, "No matching sessions.", curses.color_pair(CP_DIM))
        return

    c_title = 22
    c_acct = 12
    c_branch = 20
    c_status = 16
    c_inst = 10 if multi_instance else 0
    c_dir = max(12, cols - c_title - c_acct - c_branch - c_status - c_inst - 6)

    hdr = f"  {'Title':<{c_title}}{'Account':<{c_acct}}"
    if multi_instance:
        hdr += f"{'Instance':<{c_inst}}"
    hdr += f"{'Branch':<{c_branch}}{'Status':<{c_status}}{'Directory':<{c_dir}}"
    safe_addstr(stdscr, 3, 1, hdr, curses.color_pair(CP_HEADER) | curses.A_BOLD)

    avail = rows - 6
    total = len(sessions)
    now = int(time.time())
    idle_threshold = _cfg._default_idle_alert_minutes * 60

    offset = 0 if total <= avail else max(0, min(selected - avail // 2, total - avail))

    for i in range(offset, min(offset + avail, total)):
        screen_y = 4 + (i - offset)
        session = sessions[i]
        sel = i == selected

        cursor_mark = ">" if sel else " "
        cp = _cp_for_account(session["account"], selected=sel)

        # Build branch display
        git_info = session.get("git")
        if git_info:
            branch_display = git_info["branch"]
            if git_info["dirty"]:
                branch_display += "*"
            if len(branch_display) > c_branch - 1:
                branch_display = branch_display[: c_branch - 2] + "\u2026"
        else:
            branch_display = "-"

        # Build status text with idle duration
        status_text = session["status"]
        idle_secs = 0
        activity = session.get("activity")
        if activity:
            idle_secs = now - activity["last_event_ts"]
            status_text = f"{session['status']} {format_duration(idle_secs)}"

        title_padded = _pad_to_width(session["title"], c_title)
        line = f"{cursor_mark} {title_padded}{session['account']:<{c_acct}}"
        if multi_instance:
            line += f"{session.get('instance_label', ''):<{c_inst}}"
        line += f"{branch_display:<{c_branch}}"

        row_attr = curses.color_pair(cp)
        if sel:
            row_attr |= curses.A_BOLD
        safe_addstr(stdscr, screen_y, 1, line, row_attr)

        # Render status with color highlighting
        status_x = 1 + len(line)
        if session["status"] in ("permission", "error"):
            status_attr = curses.color_pair(CP_ATTENTION) | curses.A_BOLD
        elif (
            idle_threshold > 0
            and session["status"] in ("idle", "stopped")
            and activity
            and idle_secs >= idle_threshold
        ):
            status_attr = curses.color_pair(CP_WARN) | curses.A_BOLD
        else:
            status_attr = row_attr
        safe_addstr(
            stdscr, screen_y, status_x, f"{status_text:<{c_status}}", status_attr
        )

        # Render directory after status with normal row color
        dir_x = status_x + c_status
        short_dir = shorten_path(session["cwd"], c_dir - 1)
        safe_addstr(stdscr, screen_y, dir_x, short_dir, row_attr)


def draw_footer(stdscr, sort_mode="default"):
    """Draw the keybinding hints at the bottom."""
    rows, cols = stdscr.getmaxyx()
    text = "ctrl+p Up  ctrl+n Down  Enter Switch  s Sort"
    label = SORT_LABELS.get(sort_mode, "")
    if label:
        text += f" [{label}]"
    text += "  Esc Quit"
    safe_addstr(stdscr, rows - 1, 2, text[: cols - 3], curses.color_pair(CP_DIM))


def quick_switch_main(stdscr):
    """Run the quick-switch event loop. Returns (socket, tab_id) to focus, or None."""
    curses.curs_set(0)
    init_colors()
    stdscr.timeout(100)

    all_sessions = query_all_sessions(managed_only=True)
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

    # Show instance label when sessions span multiple Kitty instances
    multi_instance = len({s.get("instance_label", "") for s in all_sessions}) > 1

    query = ""
    cursor = 0
    selected = 0
    sort_mode = "default"
    sort_picking = False
    sort_selected = 0
    filtered = list(all_sessions)

    while True:
        stdscr.erase()
        draw_search_input(stdscr, query, cursor, len(filtered), len(all_sessions))
        draw_session_list(stdscr, filtered, selected, multi_instance=multi_instance)
        draw_footer(stdscr, sort_mode)
        if sort_picking:
            draw_sort_picker(stdscr, sort_selected, sort_mode)
        stdscr.refresh()

        key = stdscr.getch()
        if key == -1:
            continue

        # Sort picker intercepts all keys when active
        if sort_picking:
            if key == 27:
                sort_picking = False
            elif key in (curses.KEY_UP, ord("k"), ord("K")):
                sort_selected = (sort_selected - 1) % len(SORT_MODES)
            elif key in (curses.KEY_DOWN, ord("j"), ord("J")):
                sort_selected = (sort_selected + 1) % len(SORT_MODES)
            elif key in (10, 13, curses.KEY_ENTER):
                sort_mode = SORT_MODES[sort_selected]
                filtered = _apply_sort(_filter_sessions(all_sessions, query), sort_mode)
                selected = min(selected, max(0, len(filtered) - 1))
                sort_picking = False
            continue

        # Escape — quit
        if key == 27:
            return None

        # Enter — focus selected
        if key in (10, 13, curses.KEY_ENTER):
            if filtered:
                sess = filtered[selected]
                return (sess.get("socket", ""), sess["tab_id"])
            return None

        # Navigation: arrow keys, ctrl+n/ctrl+p, ctrl+k
        if key in (curses.KEY_UP, 16) and filtered:  # 16=ctrl+p
            selected = (selected - 1) % len(filtered)
            continue
        if key in (curses.KEY_DOWN, 14) and filtered:  # 14=ctrl+n
            selected = (selected + 1) % len(filtered)
            continue

        # Sort picker
        if key == ord("s"):
            sort_picking = True
            sort_selected = SORT_MODES.index(sort_mode)
            continue

        # Backspace
        if key in (curses.KEY_BACKSPACE, 127, 8):
            if cursor > 0:
                query = query[: cursor - 1] + query[cursor:]
                cursor -= 1
                filtered = _apply_sort(_filter_sessions(all_sessions, query), sort_mode)
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
            filtered = _apply_sort(list(all_sessions), sort_mode)
            selected = 0
            continue

        # Printable characters
        if 32 <= key < 127:
            query = query[:cursor] + chr(key) + query[cursor:]
            cursor += 1
            filtered = _apply_sort(_filter_sessions(all_sessions, query), sort_mode)
            selected = min(selected, max(0, len(filtered) - 1))
            continue

    return None


def main(args=None):
    load_accounts()
    load_defaults()

    home_socket = os.environ.get("KITTY_LISTEN_ON", "") or get_kitty_socket()
    result = None

    def run(stdscr):
        nonlocal result
        result = quick_switch_main(stdscr)

    curses.wrapper(run)

    if result is not None:
        sock, tab_id = result
        do_cross_instance_focus(sock, tab_id, home_socket)


if __name__ == "__main__":
    main()
