"""Config picker and delete flow views."""

import curses
import os

from cc_dashboard.colors import CP_DIM, CP_HEADER, CP_WARN, _cp_for_account
from cc_dashboard.drawing import (
    _pad_to_width,
    _truncate_to_width,
    safe_addstr,
    shorten_path,
)
from cc_dashboard.views_shared import draw_footer


def draw_pick_view(
    stdscr,
    entries,
    selected,
    running_count,
    marked=None,
    group_filter=None,
):
    """Draw the config session picker list with Running/Stopped sections."""
    rows, cols = stdscr.getmaxyx()
    stdscr.clear()

    if marked is None:
        marked = set()
    mark_count = len(marked)
    title = (
        f"Launch from Config ({mark_count} marked)"
        if mark_count
        else "Launch from Config"
    )
    safe_addstr(
        stdscr,
        1,
        max(0, (cols - len(title)) // 2),
        title,
        curses.color_pair(CP_HEADER) | curses.A_BOLD,
    )

    if group_filter is not None:
        filter_label = "Group: " + ", ".join(sorted(group_filter))
        safe_addstr(
            stdscr,
            2,
            max(0, (cols - len(filter_label)) // 2),
            filter_label,
            curses.color_pair(CP_DIM),
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
        draw_footer(stdscr, "pick")
        return

    c_title = 28
    c_acct = 12
    c_dir = max(12, cols - c_title - c_acct - 6)

    hdr = f"  {'Title':<{c_title}}{'Account':<{c_acct}}{'Directory':<{c_dir}}"
    safe_addstr(stdscr, 3, 1, hdr, curses.color_pair(CP_HEADER) | curses.A_BOLD)
    sep = f"  {'-' * c_title}{'-' * c_acct}{'-' * c_dir}"
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
                row[1],
                curses.color_pair(CP_DIM) | curses.A_BOLD,
            )
            continue

        entry_idx = row[1]
        entry = entries[entry_idx]
        sel = entry_idx == selected
        is_marked = entry["title"] in marked
        is_running = entry_idx < running_count
        if sel and is_marked:
            mark = ">*"
        elif is_marked:
            mark = " *"
        elif sel:
            mark = "> "
        else:
            mark = "  "

        cp = _cp_for_account(entry["account"], selected=sel)
        short_dir = shorten_path(os.path.expanduser(entry["dir"]), c_dir - 1)
        title_padded = _pad_to_width(
            _truncate_to_width(entry["title"], c_title - 1), c_title
        )
        acct_padded = _pad_to_width(
            _truncate_to_width(entry["account"], c_acct - 1), c_acct
        )
        line = f"{mark}{title_padded}{acct_padded}{short_dir:<{c_dir}}"

        attr = curses.color_pair(cp)
        if sel:
            attr |= curses.A_BOLD
        if is_running and not sel:
            attr = curses.color_pair(CP_DIM)
        safe_addstr(stdscr, screen_y, 1, line, attr)

    draw_footer(stdscr, "pick")


def draw_delete_view(stdscr, entries, selected, running_count):
    """Draw the delete session picker list with Running/Stopped sections."""
    rows, cols = stdscr.getmaxyx()
    stdscr.clear()

    title = "Delete Session"
    safe_addstr(
        stdscr,
        1,
        max(0, (cols - len(title)) // 2),
        title,
        curses.color_pair(CP_HEADER) | curses.A_BOLD,
    )

    if not entries:
        safe_addstr(stdscr, 4, 3, "No sessions configured.", curses.color_pair(CP_DIM))
        draw_footer(stdscr, "delete")
        return

    c_title = 24
    c_acct = 12
    c_type = 12
    c_dir = max(12, cols - c_title - c_acct - c_type - 6)

    hdr = (
        f"  {'Title':<{c_title}}{'Account':<{c_acct}}"
        f"{'Type':<{c_type}}{'Directory':<{c_dir}}"
    )
    safe_addstr(stdscr, 3, 1, hdr, curses.color_pair(CP_HEADER) | curses.A_BOLD)
    sep = f"  {'-' * c_title}{'-' * c_acct}{'-' * c_type}{'-' * c_dir}"
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
                row[1],
                curses.color_pair(CP_DIM) | curses.A_BOLD,
            )
            continue

        entry_idx = row[1]
        entry = entries[entry_idx]
        sel = entry_idx == selected
        mark = ">" if sel else " "
        is_running = entry_idx < running_count

        cp = _cp_for_account(entry["account"], selected=sel)
        short_dir = shorten_path(os.path.expanduser(entry["dir"]), c_dir - 1)
        title_padded = _pad_to_width(
            _truncate_to_width(entry["title"], c_title - 1), c_title
        )
        type_tag = entry.get("type_tag", "[dir]")
        acct_padded = _pad_to_width(
            _truncate_to_width(entry["account"], c_acct - 1), c_acct
        )
        line = (
            f"{mark} {title_padded}{acct_padded}"
            f"{type_tag:<{c_type}}{short_dir:<{c_dir}}"
        )

        attr = curses.color_pair(cp)
        if sel:
            attr |= curses.A_BOLD
        if is_running and not sel:
            attr = curses.color_pair(CP_DIM)
        safe_addstr(stdscr, screen_y, 1, line, attr)

    draw_footer(stdscr, "delete")


def draw_delete_confirm(stdscr, delete_step, entry, warnings=None):
    """Draw the confirmation prompt for the delete flow."""
    if warnings is None:
        warnings = []
    rows, _ = stdscr.getmaxyx()
    title = entry["title"]
    if delete_step == "confirm_config":
        warning = " (session is running)" if entry.get("is_running", False) else ""
        safe_addstr(
            stdscr,
            rows - 3,
            2,
            f'Remove "{title}" from sessions config?{warning}  y/n',
            curses.color_pair(CP_WARN) | curses.A_BOLD,
        )
    elif delete_step == "confirm_data":
        type_tag = entry.get("type_tag", "[dir]")
        type_label = {"[worktree]": "worktree", "[repo]": "git repo"}.get(
            type_tag, "directory"
        )
        path = os.path.expanduser(entry["dir"])
        prompt_row = rows - 3
        if warnings:
            for i, warning in enumerate(warnings):
                warning_y = prompt_row - len(warnings) + i
                if warning_y >= 1:
                    safe_addstr(
                        stdscr,
                        warning_y,
                        4,
                        f"! {warning}",
                        curses.color_pair(CP_WARN),
                    )
        safe_addstr(
            stdscr,
            prompt_row,
            2,
            f"Delete {type_label} at {path}?  y/n",
            curses.color_pair(CP_WARN) | curses.A_BOLD,
        )
