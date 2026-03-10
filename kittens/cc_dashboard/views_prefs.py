"""Rendering for the preferences editor screen."""

import curses

from cc_dashboard.colors import CP_BORDER, CP_DIM, CP_HEADER, CP_INPUT_ACTIVE, CP_NORMAL
from cc_dashboard.drawing import draw_border, safe_addstr


def draw_prefs_view(stdscr, fields, values, selected, editing, edit_value, cursor):
    """Draw the full-screen preferences form."""
    rows, cols = stdscr.getmaxyx()
    if rows < 5 or cols < 30:
        return

    stdscr.clear()

    # Title
    title = " Preferences (defaults.toml) "
    safe_addstr(stdscr, 0, 0, " " * cols, curses.color_pair(CP_HEADER))
    tx = max(0, (cols - len(title)) // 2)
    safe_addstr(stdscr, 0, tx, title, curses.color_pair(CP_HEADER) | curses.A_BOLD)

    # Determine scrolling
    max_rows = rows - 3  # title + footer + bottom padding
    scroll = max(0, selected - max_rows + 2) if selected >= max_rows - 1 else 0

    y = 1
    prev_section = None
    for i, field in enumerate(fields):
        display_y = y - scroll
        section = field["section"]

        # Section header
        if section != prev_section:
            if display_y >= 1 and display_y < rows - 1:
                label = "Defaults" if section == "defaults" else "Notifications"
                hdr = f"-- {label} --"
                safe_addstr(
                    stdscr,
                    display_y,
                    2,
                    hdr,
                    curses.color_pair(CP_DIM),
                )
            y += 1
            display_y = y - scroll
            prev_section = section

        if display_y < 1 or display_y >= rows - 1:
            y += 1
            continue

        is_selected = i == selected
        attr = curses.A_REVERSE if is_selected else curses.color_pair(CP_NORMAL)

        # Clear the line
        safe_addstr(stdscr, display_y, 0, " " * (cols - 1), attr)

        # Label
        label = f"  {field['label']}"
        safe_addstr(stdscr, display_y, 0, label, attr)

        # Value
        ftype = field["type"]
        fkey = field["key"]
        val = values.get(fkey)
        val_x = cols - 22  # right-align area

        if is_selected and editing and ftype in ("int", "string"):
            # Inline editor
            ev = edit_value
            input_w = 18
            display_val = ev[max(0, cursor - input_w + 1) :][:input_w]
            safe_addstr(
                stdscr,
                display_y,
                val_x,
                display_val,
                curses.color_pair(CP_INPUT_ACTIVE),
            )
            # Cursor position
            cx = val_x + min(cursor, input_w - 1)
            if cx < cols - 1:
                ch = ev[cursor] if cursor < len(ev) else " "
                safe_addstr(
                    stdscr,
                    display_y,
                    cx,
                    ch,
                    curses.color_pair(CP_INPUT_ACTIVE) | curses.A_UNDERLINE,
                )
        elif ftype == "bool":
            tag = "[Y]" if val else "[N]"
            safe_addstr(stdscr, display_y, val_x, tag, attr | curses.A_BOLD)
        elif ftype == "choice":
            display = val if val else "(default)"
            txt = f"< {display} >"
            safe_addstr(stdscr, display_y, val_x, txt, attr)
        elif ftype == "int":
            safe_addstr(stdscr, display_y, val_x, str(val), attr)
        elif ftype == "string":
            display = val if val else "(empty)"
            # Truncate if needed
            safe_addstr(stdscr, display_y, val_x, display[:18], attr)
        elif ftype == "string_array":
            count = len(val) if isinstance(val, list) else 0
            safe_addstr(stdscr, display_y, val_x, f"({count} items)", attr)

        y += 1


def draw_prefs_array_editor(stdscr, items, selected, editing, edit_value, cursor):
    """Draw an overlay box for editing a string array."""
    rows, cols = stdscr.getmaxyx()

    # Box dimensions
    box_w = min(60, cols - 4)
    box_h = min(len(items) + 4, rows - 4, 16)
    box_x = (cols - box_w) // 2
    box_y = (rows - box_h) // 2

    if box_w < 20 or box_h < 4:
        return

    border_attr = curses.color_pair(CP_BORDER)
    draw_border(stdscr, box_y, box_x, box_w, box_h, border_attr)

    # Clear interior
    for dy in range(box_h):
        safe_addstr(stdscr, box_y + dy, box_x, " " * box_w, curses.color_pair(CP_NORMAL))

    # Title
    title = " Array Editor "
    tx = box_x + max(0, (box_w - len(title)) // 2)
    safe_addstr(stdscr, box_y, tx, title, curses.color_pair(CP_HEADER) | curses.A_BOLD)

    # Items
    max_visible = box_h - 2
    scroll = max(0, selected - max_visible + 1) if selected >= max_visible else 0

    for i in range(max_visible):
        idx = scroll + i
        if idx >= len(items) + (1 if editing and selected >= len(items) else 0):
            break
        iy = box_y + 1 + i
        is_sel = idx == selected
        attr = curses.A_REVERSE if is_sel else curses.color_pair(CP_NORMAL)

        safe_addstr(stdscr, iy, box_x, " " * box_w, attr)

        if is_sel and editing:
            # Show editable input
            ev = edit_value
            input_w = box_w - 4
            display = ev[:input_w]
            safe_addstr(stdscr, iy, box_x + 2, display, curses.color_pair(CP_INPUT_ACTIVE))
            cx = box_x + 2 + min(cursor, input_w - 1)
            if cx < box_x + box_w - 1:
                ch = ev[cursor] if cursor < len(ev) else " "
                safe_addstr(
                    stdscr,
                    iy,
                    cx,
                    ch,
                    curses.color_pair(CP_INPUT_ACTIVE) | curses.A_UNDERLINE,
                )
        elif idx < len(items):
            txt = items[idx][:box_w - 4]
            safe_addstr(stdscr, iy, box_x + 2, txt, attr)

    # Hint at bottom
    hint = "a:Add  d:Del  e:Edit  Esc:Back"
    safe_addstr(
        stdscr,
        box_y + box_h - 1,
        box_x + 2,
        hint[:box_w - 4],
        curses.color_pair(CP_DIM),
    )
