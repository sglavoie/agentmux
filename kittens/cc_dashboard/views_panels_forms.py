"""Form and directory autocomplete views."""

import curses
import os

from cc_dashboard.colors import CP_DIM, CP_HEADER, CP_INPUT, CP_INPUT_ACTIVE, CP_NORMAL
from cc_dashboard.drawing import safe_addstr
from cc_dashboard.views_shared import draw_footer


def draw_new_form(
    stdscr,
    fields,
    active_field,
    cursor_pos,
    multi_instance=False,
    form_title="New Session",
):
    """Draw the new-session or edit-session form."""
    _rows, cols = stdscr.getmaxyx()
    stdscr.clear()

    safe_addstr(
        stdscr,
        1,
        max(0, (cols - len(form_title)) // 2),
        form_title,
        curses.color_pair(CP_HEADER) | curses.A_BOLD,
    )

    labels = [
        "Title:",
        "Directory:",
        "Account:",
        "Flags:",
        "Split:",
        "Env vars:",
        "Group:",
        "Commands:",
    ]
    field_keys = ["title", "dir", "account", "flags", "pw", "env", "group", "commands"]
    cycle_fields = {"account", "pw"}

    if "save" in fields:
        labels.append("Save:")
        field_keys.append("save")
        cycle_fields.add("save")

    if multi_instance and "instance" in fields:
        labels.insert(-1, "Instance:")
        field_keys.insert(-1, "instance")
        cycle_fields.add("instance")

    for i, (label, key) in enumerate(zip(labels, field_keys, strict=False)):
        y = 4 + i * 2
        is_active = i == active_field
        safe_addstr(stdscr, y, 4, f"{label:>12}", curses.color_pair(CP_HEADER))

        val = fields[key]
        if key in cycle_fields:
            display = f" {val} "
            if is_active:
                display += "  (Tab to cycle)"
            cp = CP_INPUT_ACTIVE if is_active else CP_INPUT
            safe_addstr(stdscr, y, 17, display, curses.color_pair(cp))
            continue

        if is_active:
            before = val[:cursor_pos]
            after = val[cursor_pos:]
            safe_addstr(stdscr, y, 17, before, curses.color_pair(CP_INPUT_ACTIVE))
            cursor_ch = after[0] if after else " "
            safe_addstr(
                stdscr,
                y,
                17 + len(before),
                cursor_ch,
                curses.color_pair(CP_INPUT_ACTIVE) | curses.A_REVERSE,
            )
            if len(after) > 1:
                safe_addstr(
                    stdscr,
                    y,
                    17 + len(before) + 1,
                    after[1:],
                    curses.color_pair(CP_INPUT_ACTIVE),
                )
        else:
            safe_addstr(
                stdscr,
                y,
                17,
                val[: cols - 20],
                curses.color_pair(CP_INPUT),
            )

    hint_y = 4 + len(field_keys) * 2
    safe_addstr(
        stdscr,
        hint_y,
        17,
        'Flags/Split: empty = inherit global, "-" = disable  |  Env: KEY=VAL KEY2=VAL2',
        curses.color_pair(CP_DIM),
    )
    safe_addstr(
        stdscr,
        hint_y + 1,
        17,
        "Commands: ;; separated, 'shell' for terminal  |  auto-enables stack layout",
        curses.color_pair(CP_DIM),
    )

    draw_footer(stdscr, "new")


def draw_restart_options_form(
    stdscr,
    session,
    fields,
    active_field,
    cursor_pos,
    *,
    multi_instance=False,
):
    """Draw restart options form for a selected running session."""
    _rows, cols = stdscr.getmaxyx()
    stdscr.clear()

    title = "Restart Options"
    safe_addstr(
        stdscr,
        1,
        max(0, (cols - len(title)) // 2),
        title,
        curses.color_pair(CP_HEADER) | curses.A_BOLD,
    )

    subtitle = session.get("title", "")
    safe_addstr(
        stdscr,
        2,
        max(0, (cols - len(subtitle)) // 2),
        subtitle,
        curses.color_pair(CP_DIM),
    )

    labels = ["Flags:", "Split:", "Env vars:", "Config dir:"]
    field_keys = ["flags", "pw", "env", "config_dir"]
    cycle_fields = {"pw", "config_dir"}

    if multi_instance and "instance" in fields:
        labels.append("Instance:")
        field_keys.append("instance")
        cycle_fields.add("instance")

    for i, (label, key) in enumerate(zip(labels, field_keys, strict=False)):
        y = 5 + i * 2
        is_active = i == active_field
        safe_addstr(stdscr, y, 4, f"{label:>12}", curses.color_pair(CP_HEADER))

        val = fields[key]
        if key in cycle_fields:
            display = f" {val} "
            if is_active:
                display += "  (Tab to cycle)"
            cp = CP_INPUT_ACTIVE if is_active else CP_INPUT
            safe_addstr(stdscr, y, 17, display[: cols - 20], curses.color_pair(cp))
            continue

        if is_active:
            before = val[:cursor_pos]
            after = val[cursor_pos:]
            safe_addstr(stdscr, y, 17, before, curses.color_pair(CP_INPUT_ACTIVE))
            cursor_ch = after[0] if after else " "
            safe_addstr(
                stdscr,
                y,
                17 + len(before),
                cursor_ch,
                curses.color_pair(CP_INPUT_ACTIVE) | curses.A_REVERSE,
            )
            if len(after) > 1:
                safe_addstr(
                    stdscr,
                    y,
                    17 + len(before) + 1,
                    after[1:],
                    curses.color_pair(CP_INPUT_ACTIVE),
                )
        else:
            safe_addstr(
                stdscr,
                y,
                17,
                val[: cols - 20],
                curses.color_pair(CP_INPUT),
            )

    hint_y = 5 + len(field_keys) * 2
    safe_addstr(
        stdscr,
        hint_y,
        17,
        'Flags/Split: empty = inherit global, "-" = disable  |  Env: KEY=VAL KEY2=VAL2',
        curses.color_pair(CP_DIM),
    )

    draw_footer(stdscr, "restart_options")


def _scan_directories(field_value, max_results=50, show_hidden=False):
    """Scan filesystem for directories matching the current dir field input."""
    expanded = os.path.expanduser(field_value)
    if field_value.endswith("/") or expanded.endswith("/"):
        scan_dir = expanded
        partial = ""
    else:
        scan_dir = os.path.dirname(expanded)
        partial = os.path.basename(expanded)
    if not os.path.isdir(scan_dir):
        return []
    try:
        results = []
        for entry in os.scandir(scan_dir):
            try:
                is_dir = entry.is_dir(follow_symlinks=True)
            except OSError:
                continue
            if (
                is_dir
                and (show_hidden or not entry.name.startswith("."))
                and (not partial or partial.lower() in entry.name.lower())
            ):
                results.append(entry.name)
        entries = sorted(results)
    except OSError:
        return []
    return entries[:max_results]


def draw_dir_dropdown(stdscr, entries, selected, scroll):
    """Draw a directory autocomplete dropdown below the dir field."""
    rows, cols = stdscr.getmaxyx()
    dropdown_y = 7
    dropdown_x = 17
    max_visible = min(10, rows - dropdown_y - 3)
    if max_visible < 1:
        return
    if not entries:
        safe_addstr(
            stdscr,
            dropdown_y,
            dropdown_x,
            "(no matching directories)",
            curses.color_pair(CP_DIM),
        )
        return
    visible = entries[scroll : scroll + max_visible]
    max_width = cols - dropdown_x - 2
    for i, name in enumerate(visible):
        y = dropdown_y + i
        if y >= rows - 2:
            break
        abs_idx = scroll + i
        is_sel = abs_idx == selected
        display = ("> " if is_sel else "  ") + name + "/"
        display = display[:max_width]
        attr = (
            curses.color_pair(CP_INPUT_ACTIVE) | curses.A_BOLD
            if is_sel
            else curses.color_pair(CP_NORMAL)
        )
        safe_addstr(stdscr, y, dropdown_x, display, attr)
    total = len(entries)
    if total > max_visible:
        indicator = f"[{scroll + 1}-{min(scroll + max_visible, total)}/{total}]"
        ind_y = dropdown_y + len(visible)
        if ind_y < rows - 2:
            safe_addstr(stdscr, ind_y, dropdown_x, indicator, curses.color_pair(CP_DIM))
