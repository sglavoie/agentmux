#!/usr/bin/env python3
"""Standalone curses session picker for cc-launch.

Displays configured sessions from the sessions config in a curses UI with
account-colored entries, Running/Stopped sections, search, and multi-select.
Selected sessions are printed as tab-delimited lines to stdout.

Exit code 0 means selections were made, exit code 1 means user quit.
"""

import curses
import os
import sys


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

for _mod in list(sys.modules):
    if _mod.startswith("cc_dashboard"):
        del sys.modules[_mod]

from cc_dashboard import config as _cfg
from cc_dashboard.config import load_accounts, load_defaults, load_sessions
from cc_dashboard.remote.command import get_kitty_socket
from cc_dashboard.remote.sessions import query_sessions
from cc_dashboard.ui import (
    CP_DIM,
    CP_HEADER,
    _cp_for_account,
    _filter_config_sessions,
    _pad_to_width,
    _sort_pick_entries,
    _truncate_to_width,
    draw_search_bar,
    init_colors,
    safe_addstr,
    shorten_path,
)


def _normalize_commands(raw_commands):
    """Return a sanitized list of command strings for picker output."""
    if raw_commands is None:
        return []
    if not isinstance(raw_commands, list):
        return []
    result = []
    for command in raw_commands:
        if (
            isinstance(command, str)
            and "\t" not in command
            and "\n" not in command
            and "\r" not in command
        ):
            result.append(command)
    return result


def draw_picker_view(stdscr, entries, selected, running_count, toggled):
    """Draw the session picker list with Running/Stopped sections and toggle markers."""
    rows, cols = stdscr.getmaxyx()
    stdscr.erase()

    title = "Select Sessions"
    safe_addstr(
        stdscr,
        1,
        max(0, (cols - len(title)) // 2),
        title,
        curses.color_pair(CP_HEADER) | curses.A_BOLD,
    )

    if not entries:
        safe_addstr(stdscr, 4, 3, "No sessions configured.", curses.color_pair(CP_DIM))
        safe_addstr(
            stdscr,
            5,
            3,
            "Add entries to the sessions config, then try again.",
            curses.color_pair(CP_DIM),
        )
        draw_picker_footer(stdscr)
        return

    c_title = 28
    c_acct = 12
    c_dir = max(12, cols - c_title - c_acct - 10)  # extra space for toggle marker

    hdr = f"      {'Title':<{c_title}}{'Account':<{c_acct}}{'Directory':<{c_dir}}"
    safe_addstr(stdscr, 3, 1, hdr, curses.color_pair(CP_HEADER) | curses.A_BOLD)
    sep = f"      {'-' * c_title}{'-' * c_acct}{'-' * c_dir}"
    safe_addstr(stdscr, 4, 1, sep, curses.color_pair(CP_DIM))

    stopped_count = len(entries) - running_count
    visual_rows: list[tuple[str, str | int]] = []
    if running_count > 0:
        visual_rows.append(("header", f"Running ({running_count})"))
        for i in range(running_count):
            visual_rows.append(("entry", i))
    if stopped_count > 0:
        visual_rows.append(("header", f"Stopped ({stopped_count})"))
        for i in range(running_count, len(entries)):
            visual_rows.append(("entry", i))

    sel_visual = 0
    for vi, row in enumerate(visual_rows):
        if row[0] == "entry" and row[1] == selected:
            sel_visual = vi
            break

    avail = max(1, rows - 8)
    total_visual = len(visual_rows)
    if total_visual <= avail:
        offset = 0
    else:
        offset = max(0, min(sel_visual - avail // 2, total_visual - avail))

    for vi in range(offset, min(offset + avail, total_visual)):
        row = visual_rows[vi]
        screen_y = 5 + (vi - offset)

        if row[0] == "header":
            safe_addstr(
                stdscr,
                screen_y,
                2,
                str(row[1]),
                curses.color_pair(CP_DIM) | curses.A_BOLD,
            )
        else:
            entry_idx = row[1]
            entry = entries[entry_idx]
            sel = entry_idx == selected
            is_running = entry_idx < running_count

            cursor_mark = ">" if sel else " "
            toggle_mark = "[x]" if entry_idx in toggled else "[ ]"

            cp = _cp_for_account(entry["account"], selected=sel)
            short_dir = shorten_path(os.path.expanduser(entry["dir"]), c_dir - 1)
            title_padded = _pad_to_width(
                _truncate_to_width(entry["title"], c_title - 1), c_title
            )
            acct = f"{entry['account']:<{c_acct}}"
            sdir = f"{short_dir:<{c_dir}}"
            line = f"{cursor_mark} {toggle_mark} {title_padded}{acct}{sdir}"

            attr = curses.color_pair(cp)
            if sel:
                attr |= curses.A_BOLD
            if is_running and not sel:
                attr = curses.color_pair(CP_DIM)
            safe_addstr(stdscr, screen_y, 1, line, attr)

    draw_picker_footer(stdscr)


def draw_picker_footer(stdscr):
    """Draw the help-key footer for the picker."""
    rows, cols = stdscr.getmaxyx()
    text = "k Up  j Down  Space/x Toggle  Enter Launch  / Search  q Quit"
    safe_addstr(stdscr, rows - 1, 2, text[: cols - 3], curses.color_pair(CP_DIM))


def picker_main(stdscr, socket):
    """Run the interactive session picker loop. Returns list of selected entries."""
    curses.curs_set(0)
    init_colors()
    stdscr.timeout(100)

    sessions = query_sessions(socket, managed_only=True)
    pick_running = {s["title"] for s in sessions}

    pick_filtered, pick_running_count = _sort_pick_entries(
        list(_cfg._sessions), pick_running
    )
    pick_selected = 0
    toggled: set[int] = set()

    pick_query = ""
    pick_cursor = 0
    pick_search_active = False

    while True:
        # Draw
        draw_picker_view(
            stdscr, pick_filtered, pick_selected, pick_running_count, toggled
        )
        if pick_search_active or pick_query:
            draw_search_bar(
                stdscr,
                pick_query,
                pick_cursor,
                pick_search_active,
                len(pick_filtered),
            )
        stdscr.refresh()

        key = stdscr.getch()
        if key == -1:
            continue

        if pick_search_active:
            if key == 27:  # Escape — cancel search
                pick_search_active = False
                pick_query = ""
                pick_cursor = 0
                pick_filtered, pick_running_count = _sort_pick_entries(
                    list(_cfg._sessions), pick_running
                )
                pick_selected = min(pick_selected, max(0, len(pick_filtered) - 1))
                continue
            elif key in (10, 13, curses.KEY_ENTER):
                pick_search_active = False
                continue
            elif key in (curses.KEY_UP, ord("k"), ord("K")) and pick_filtered:
                pick_selected = (pick_selected - 1) % len(pick_filtered)
                continue
            elif key in (curses.KEY_DOWN, ord("j"), ord("J")) and pick_filtered:
                pick_selected = (pick_selected + 1) % len(pick_filtered)
                continue
            elif key in (curses.KEY_BACKSPACE, 127, 8):
                if pick_cursor > 0:
                    pick_query = (
                        pick_query[: pick_cursor - 1] + pick_query[pick_cursor:]
                    )
                    pick_cursor -= 1
                    pick_filtered = _filter_config_sessions(_cfg._sessions, pick_query)
                    pick_filtered, pick_running_count = _sort_pick_entries(
                        pick_filtered, pick_running
                    )
                    pick_selected = min(pick_selected, max(0, len(pick_filtered) - 1))
                    toggled.clear()
                continue
            elif key == curses.KEY_LEFT:
                pick_cursor = max(0, pick_cursor - 1)
                continue
            elif key == curses.KEY_RIGHT:
                pick_cursor = min(len(pick_query), pick_cursor + 1)
                continue
            elif 32 <= key < 127:
                pick_query = (
                    pick_query[:pick_cursor] + chr(key) + pick_query[pick_cursor:]
                )
                pick_cursor += 1
                pick_filtered = _filter_config_sessions(_cfg._sessions, pick_query)
                pick_filtered, pick_running_count = _sort_pick_entries(
                    pick_filtered, pick_running
                )
                pick_selected = min(pick_selected, max(0, len(pick_filtered) - 1))
                toggled.clear()
                continue
            else:
                continue

        # Normal mode key handling
        if key in (ord("q"), ord("Q")):
            return []
        elif key == 27:  # Escape
            if pick_query:
                pick_query = ""
                pick_cursor = 0
                pick_filtered, pick_running_count = _sort_pick_entries(
                    list(_cfg._sessions), pick_running
                )
                pick_selected = min(pick_selected, max(0, len(pick_filtered) - 1))
                toggled.clear()
            else:
                return []
        elif key == ord("/"):
            pick_search_active = True
            pick_query = ""
            pick_cursor = 0
            pick_filtered, pick_running_count = _sort_pick_entries(
                list(_cfg._sessions), pick_running
            )
        elif key in (curses.KEY_UP, ord("k"), ord("K")) and pick_filtered:
            pick_selected = (pick_selected - 1) % len(pick_filtered)
        elif key in (curses.KEY_DOWN, ord("j"), ord("J")) and pick_filtered:
            pick_selected = (pick_selected + 1) % len(pick_filtered)
        elif key in (ord(" "), ord("x")) and pick_filtered:
            if pick_selected in toggled:
                toggled.discard(pick_selected)
            else:
                toggled.add(pick_selected)
        elif key in (10, 13, curses.KEY_ENTER) and pick_filtered:
            if toggled:
                return [
                    pick_filtered[i] for i in sorted(toggled) if i < len(pick_filtered)
                ]
            else:
                return [pick_filtered[pick_selected]]


def main():
    load_accounts()
    load_defaults()
    load_sessions()

    if not _cfg._sessions:
        print(
            f"No sessions configured in {_cfg._sessions_config_filename()}",
            file=sys.stderr,
        )
        sys.exit(1)

    socket = get_kitty_socket()
    if not socket:
        print(
            "Error: No kitty socket found. "
            "Is Kitty running with remote control enabled?",
            file=sys.stderr,
        )
        sys.exit(1)

    selected = []

    def run(stdscr):
        curses.set_escdelay(25)
        nonlocal selected
        selected = picker_main(stdscr, socket)

    # When invoked via command substitution, stdout is a pipe and curses
    # cannot draw to the terminal.  Redirect fd 1 to /dev/tty so curses
    # renders correctly, then restore the original fd for final output.
    saved_stdout = os.dup(1)
    tty_fd = os.open("/dev/tty", os.O_RDWR)
    os.dup2(tty_fd, 1)
    os.close(tty_fd)

    try:
        curses.wrapper(run)
    finally:
        # Restore original stdout so pipe output reaches the caller.
        os.dup2(saved_stdout, 1)
        os.close(saved_stdout)

    if not selected:
        sys.exit(1)

    for entry in selected:
        pw = entry.get("pw", "")
        flags = entry.get("flags", "")
        env = entry.get("env", "")
        groups = "\x1f".join(entry.get("groups", []))
        mute = "true" if entry.get("mute") else ""
        cmds = "\x1f".join(_normalize_commands(entry.get("commands")))
        print(
            f"{entry['title']}\t{entry['dir']}\t{entry['account']}\t{flags}\t{pw}\t{env}\t{groups}\t{mute}\t{cmds}"
        )


if __name__ == "__main__":
    main()
