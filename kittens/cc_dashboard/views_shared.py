"""Shared helpers and footer/status rendering for dashboard views."""

from __future__ import annotations

import curses
import os
from typing import Any

from cc_dashboard.colors import (
    CP_DIM,
    CP_INPUT_ACTIVE,
    CP_NORMAL,
    CP_WARN,
)
from cc_dashboard.drawing import _truncate_to_width, safe_addstr

_FOOTER_HINTS = {
    "list": [
        "q Quit",
        "Enter Focus",
        "x Close",
        "t Actions",
        "e Edit",
        "n New",
        "l Launch",
        "D Delete",
        "S Save",
        "C Config",
        "H Hooks",
        "L Log",
        "d Detail",
        "m Mute",
        "p Peek",
        "/ Search",
        "a All",
        "f Acct",
        "g Group",
        "G GroupAct",
        "s Sort",
        "z Fold",
        "Space Mark",
        "* GroupMark",
        "U Unmark",
        "P Prefs",
        "^r Refresh",
        "^F Search logs",
    ],
    "rename": ["Enter Confirm", "Esc Cancel"],
    "filter": ["1-N Toggle account", "f Done", "Esc Clear"],
    "group_filter": ["1-N Toggle group", "g Done", "Esc Clear"],
    "confirm": ["y Confirm close", "Any other key Cancel"],
    "confirm_restart": ["y Confirm restart", "Any other key Cancel"],
    "confirm_batch_close": ["y Confirm close all", "Any other key Cancel"],
    "confirm_batch_restart": ["y Confirm restart all", "Any other key Cancel"],
    "confirm_batch_clone": ["y Confirm clone all", "Any other key Cancel"],
    "confirm_save": ["y Confirm save", "Any other key Cancel"],
    "confirm_mkdir": ["y Create directory", "Any other key Cancel"],
    "new": ["Enter Next/Submit", "Tab Cycle", "Up/Dn Navigate", "Esc Cancel"],
    "new_dir_browse": [
        "Enter Select",
        "Up/Dn Navigate",
        ". Toggle hidden",
        "Esc Close",
    ],
    "pick": [
        "Enter Launch",
        "Space Mark",
        "U Unmark",
        "g Group",
        "i Instance",
        "O CfgDir",
        "/ Search",
        "Esc Back",
        "q Back",
    ],
    "pick_group_filter": ["1-N Toggle group", "g Done", "Esc Clear"],
    "override": ["j/k Navigate", "Enter Confirm", "Esc Cancel"],
    "move": ["j/k Navigate", "Enter Move", "Esc Cancel"],
    "peek": [
        "j/k Scroll",
        "^D/^U Half-page",
        "^F/^B Page",
        "G/g Top/Bot",
        "/ Search",
        "n/N Next/Prev",
        "r Refresh",
        "s Screen/Scrollback",
        "q/Esc Close",
    ],
    "peek_search": ["Enter Confirm", "Esc Cancel"],
    "config_pick": ["j/k Navigate", "Enter Select", "R Reload", "Esc Back"],
    "config_action": ["j/k Navigate", "Enter Confirm", "Esc Back"],
    "sort_pick": ["j/k Navigate", "Enter Select", "Esc Cancel"],
    "group_actions": ["j/k Navigate", "Enter Select", "Esc Back", "q Quit"],
    "color_picker": ["Esc Cancel", "j/k Navigate", "Enter Select"],
    "batch_group": ["Enter Apply", "Esc Cancel"],
    "actions": [
        "j/k Navigate",
        "Enter Apply",
        "Esc Back",
        "Marked: Restart/Close/Move",
        "Single: Restart opts/Clone/Override/Rename/Worktree",
    ],
    "clone": ["Enter Clone", "Esc Cancel"],
    "worktree": ["Enter Create", "Esc Cancel"],
    "restart_options": [
        "Enter Next/Apply",
        "Tab Cycle",
        "Up/Dn Navigate",
        "Esc Cancel",
    ],
    "delete": ["Enter Select", "/ Search", "q Back", "Esc Back"],
    "delete_confirm": ["y Confirm", "Any other key Cancel"],
    "hooks": [
        "j/k Navigate",
        "Enter Events",
        "s Sync account",
        "S Sync all",
        "q Back",
        "Esc Back",
    ],
    "hooks_event": [
        "j/k Navigate",
        "Space Toggle",
        "a Add",
        "e Edit",
        "d Delete",
        "i Info",
        "E Example",
        "s Sync",
        "q Back",
        "Esc Back",
    ],
    "hooks_delete_confirm": ["y Confirm delete", "Any other key Cancel"],
    "hooks_sync_confirm": ["y Confirm sync", "Any other key Cancel"],
    "hooks_add": [
        "Up/Dn Navigate",
        "Tab Next/Pick",
        "Shift-Tab Prev",
        "Space Toggle",
        "Enter Submit",
        "Esc Cancel",
    ],
    "hooks_matcher_pick": ["j/k Navigate", "Enter Select", "Esc Cancel"],
    "hooks_info": ["i Close", "Esc Close", "q Close"],
    "hooks_example": ["E Close", "Esc Close", "q Close"],
    "actlog": [
        "j/k Scroll",
        "^D/^U Half-page",
        "^F/^B Page",
        "G/g Top/Bot",
        "/ Search",
        "n/N Next/Prev",
        "b Browse all",
        "r Refresh",
        "Enter Detail",
        "q/Esc Close",
    ],
    "actlog_detail": ["Enter Close", "Esc Close", "q Close"],
    "actlog_search": ["Enter Confirm", "Esc Cancel"],
    "actlog_browse": [
        "j/k Navigate",
        "^D/^U Half-page",
        "^F/^B Page",
        "G/g Top/Bot",
        "/ Search",
        "n/N Next/Prev",
        "s Search all",
        "Enter View",
        "q/Esc Back",
    ],
    "actlog_browse_search": ["Enter Confirm", "Esc Cancel"],
    "actlog_gsearch": [
        "j/k Navigate",
        "^D/^U Half-page",
        "^F/^B Page",
        "G/g Top/Bot",
        "/ Search",
        "Enter View",
        "Esc Back",
    ],
    "actlog_gsearch_search": [
        "Enter Confirm",
        "Esc Clear",
    ],
    "prefs": [
        "j/k Navigate",
        "Enter Edit",
        "Space Toggle",
        "Tab/S-Tab Cycle",
        "Esc Back",
    ],
    "prefs_array": ["j/k Navigate", "a Add", "d Delete", "e/Enter Edit", "Esc Back"],
}

_ACTIONS_FOOTER_COMPACT = [
    "j/k Nav",
    "Enter Apply",
    "Esc Back",
    "Marked: restart/close/move",
    "Single: all actions",
]


def _shorten_home(path: str) -> str:
    """Replace the home directory prefix with ~ for display."""
    home = os.path.expanduser("~")
    if path.startswith(home):
        return "~" + path[len(home) :]
    return path


def draw_footer(
    stdscr: curses.window, mode: str, exclude: list[str] | None = None
) -> None:
    """Draw the help-key footer at the bottom of the screen."""
    rows, cols = stdscr.getmaxyx()
    sep = "  "
    avail = cols - 3
    items = _FOOTER_HINTS.get(mode, [])
    if mode == "actions" and cols < 120:
        items = _ACTIONS_FOOTER_COMPACT
    if exclude:
        items = [i for i in items if i not in exclude]
    parts: list[str] = []
    length = 0
    for item in items:
        item_len = len(item)
        needed = item_len if not parts else len(sep) + item_len
        if length + needed > avail:
            break
        parts.append(item)
        length += needed
    safe_addstr(stdscr, rows - 1, 0, " " * (cols - 1), curses.color_pair(CP_NORMAL))
    safe_addstr(stdscr, rows - 1, 2, sep.join(parts), curses.color_pair(CP_DIM))


def draw_confirm_prompt(
    stdscr: curses.window, session: dict[str, Any], action: str = "Close"
) -> None:
    """Draw a confirmation prompt above the footer."""
    rows, cols = stdscr.getmaxyx()
    title = session["title"]
    # Reserve space for: action + ' session "' + '"?  y/n' + margin
    prefix_len = len(action) + len(' session "') + len('"?  y/n')
    max_title = max(4, cols - prefix_len - 4)
    title = _truncate_to_width(title, max_title)
    msg = f'{action} session "{title}"?  y/n'
    safe_addstr(
        stdscr,
        rows - 3,
        2,
        msg,
        curses.color_pair(CP_WARN) | curses.A_BOLD,
    )


def draw_batch_confirm_prompt(
    stdscr: curses.window, sessions: list[dict[str, Any]], action: str = "Close"
) -> None:
    """Draw a batch confirmation prompt listing affected sessions."""
    rows, cols = stdscr.getmaxyx()
    attr = curses.color_pair(CP_WARN) | curses.A_BOLD
    safe_addstr(stdscr, rows - 4, 2, f"{action} {len(sessions)} session(s)?  y/n", attr)
    titles = ", ".join(s["title"] for s in sessions)
    max_w = cols - 5
    if len(titles) > max_w:
        titles = titles[: max_w - 3] + "..."
    safe_addstr(stdscr, rows - 3, 4, titles, curses.color_pair(CP_DIM))


def draw_status_message(stdscr: curses.window, message: str) -> None:
    """Draw a transient status message above the footer."""
    rows, cols = stdscr.getmaxyx()
    max_w = max(0, cols - 4)
    clipped = message
    if len(clipped) > max_w:
        clipped = clipped[: max(0, max_w - 3)] + "..."
    safe_addstr(stdscr, rows - 3, 2, clipped, curses.color_pair(CP_WARN))


def _draw_text_input_line(
    stdscr: curses.window, y: int, prefix: str, value: str, cursor_pos: int
) -> None:
    """Draw a text input with cursor at the given row.

    Renders ``prefix`` in warning color followed by ``value`` with a
    reverse-video cursor at ``cursor_pos``.  Used by all single-line
    input prompts (rename, clone title, worktree branch, batch group,
    peek search).
    """
    safe_addstr(stdscr, y, 2, prefix, curses.color_pair(CP_WARN))
    x = 2 + len(prefix)
    before = value[:cursor_pos]
    after = value[cursor_pos:]
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


def draw_search_bar(
    stdscr: curses.window, query: str, cursor_pos: int, active: bool, match_count: int
) -> None:
    """Draw the search/filter bar on the status line."""
    rows, _cols = stdscr.getmaxyx()
    y = rows - 3

    if active:
        _draw_text_input_line(stdscr, y, "/ ", query, cursor_pos)
        suffix_x = 2 + len("/ ") + len(query) + 1
        safe_addstr(
            stdscr,
            y,
            suffix_x,
            f"  ({match_count} matches)",
            curses.color_pair(CP_DIM),
        )
    else:
        safe_addstr(
            stdscr,
            y,
            2,
            f'Filter: "{query}" ({match_count} matches)  [Esc clear]',
            curses.color_pair(CP_DIM),
        )


def draw_rename_input(
    stdscr: curses.window, current_value: str, cursor_pos: int
) -> None:
    """Draw a single-line rename input on the status line area."""
    rows, _cols = stdscr.getmaxyx()
    _draw_text_input_line(stdscr, rows - 3, "Rename: ", current_value, cursor_pos)


def draw_worktree_input(
    stdscr: curses.window,
    session: dict[str, Any],
    branch_value: str,
    cursor_pos: int,
) -> None:
    """Draw a two-line worktree input on the status line area."""
    rows, cols = stdscr.getmaxyx()
    title = _truncate_to_width(session["title"], max(4, cols - 28))

    safe_addstr(
        stdscr,
        rows - 4,
        2,
        f'Clone "{title}" -> worktree',
        curses.color_pair(CP_DIM),
    )
    _draw_text_input_line(stdscr, rows - 3, "Branch: ", branch_value, cursor_pos)


def draw_clone_input(
    stdscr: curses.window,
    session: dict[str, Any],
    title_value: str,
    cursor_pos: int,
) -> None:
    """Draw a two-line clone input on the status line area."""
    rows, cols = stdscr.getmaxyx()
    title = _truncate_to_width(session["title"], max(4, cols - 32))

    safe_addstr(
        stdscr,
        rows - 4,
        2,
        f'Clone "{title}" -> new session',
        curses.color_pair(CP_DIM),
    )
    _draw_text_input_line(stdscr, rows - 3, "Title: ", title_value, cursor_pos)


def draw_batch_group_input(
    stdscr: curses.window,
    targets: list[dict[str, Any]],
    group_value: str,
    cursor_pos: int,
) -> None:
    """Draw a two-line batch group input on the status line area."""
    rows, _cols = stdscr.getmaxyx()

    safe_addstr(
        stdscr,
        rows - 4,
        2,
        f"Set group for {len(targets)} marked session(s) (empty to clear)",
        curses.color_pair(CP_DIM),
    )
    _draw_text_input_line(stdscr, rows - 3, "Group: ", group_value, cursor_pos)


def _render_peek_line(
    stdscr: curses.window,
    row: int,
    line: str,
    cols: int,
    query: str,
    is_match: bool,
    is_current: bool,
    normal_attr: int,
    match_attr: int,
    span_attr: int,
    current_attr: int,
) -> None:
    """Render a single peek line with optional match-span highlighting."""
    clipped = line[: cols - 1]
    if not query or not is_match:
        safe_addstr(stdscr, row, 0, clipped, normal_attr)
        return

    q_lower = query.lower()
    q_len = len(query)
    line_lower = clipped.lower()
    bg_attr = match_attr
    hl_attr = current_attr if is_current else span_attr
    x = 0
    pos = 0
    while pos < len(clipped):
        idx = line_lower.find(q_lower, pos)
        if idx == -1:
            safe_addstr(stdscr, row, x, clipped[pos:], bg_attr)
            break
        if idx > pos:
            safe_addstr(stdscr, row, x, clipped[pos:idx], bg_attr)
            x += idx - pos
        safe_addstr(stdscr, row, x, clipped[idx : idx + q_len], hl_attr)
        x += q_len
        pos = idx + q_len


def _draw_peek_search_bar(
    stdscr: curses.window, query: str, cursor_pos: int, match_count: int
) -> None:
    """Draw the peek search input bar at rows - 2."""
    rows, _cols = stdscr.getmaxyx()
    y = rows - 2

    _draw_text_input_line(stdscr, y, "/ ", query, cursor_pos)
    suffix_x = 2 + len("/ ") + len(query) + 1
    safe_addstr(
        stdscr,
        y,
        suffix_x,
        f"  ({match_count} matches)",
        curses.color_pair(CP_DIM),
    )
