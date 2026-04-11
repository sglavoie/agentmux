import contextlib
import curses

from cc_dashboard.colors import (
    CP_BORDER,
    CP_DIM,
    CP_HEADER,
    CP_INPUT,
    CP_NORMAL,
    CP_WARN,
    init_colors,
)
from cc_dashboard.config_defaults import _save_tab_statuses
from cc_dashboard.drawing import draw_border, safe_addstr
from cc_dashboard.remote.shared import _strip_override_marker
from cc_tab_status_state import SessionInfo, extract_status, load_statuses

StatusSelection = tuple[str, str] | None


def draw_status_picker(
    stdscr: curses.window,
    session: SessionInfo,
    statuses: list[str],
    selected: int,
    custom_mode: bool,
    custom_value: str,
    custom_cursor: int,
    confirm_delete: bool,
    delete_target: int,
) -> None:
    """Render the status picker popup."""
    stdscr.erase()
    rows, cols = stdscr.getmaxyx()

    current_status = extract_status(_strip_override_marker(session["title"]))[1]

    total_items = len(statuses) + 2
    box_w = min(cols - 4, 40)
    box_h = total_items + 6
    box_y = max(0, (rows - box_h) // 2)
    box_x = max(0, (cols - box_w) // 2)

    dim_attr = curses.color_pair(CP_DIM)
    blank_line = " " * box_w
    for dy in range(box_h):
        safe_addstr(stdscr, box_y + dy, box_x, blank_line, dim_attr)
    draw_border(
        stdscr, box_y, box_x, box_w, box_h, curses.color_pair(CP_BORDER) | curses.A_BOLD
    )

    header = "Set Tab Status"
    safe_addstr(
        stdscr,
        box_y,
        box_x + max(0, (box_w - len(header)) // 2),
        header,
        curses.color_pair(CP_HEADER) | curses.A_BOLD,
    )

    base_title = extract_status(_strip_override_marker(session["title"]))[0]
    subtitle = f'for "{base_title}"'
    safe_addstr(
        stdscr,
        box_y + 1,
        box_x + max(0, (box_w - len(subtitle)) // 2),
        subtitle,
        dim_attr,
    )

    normal_attr = curses.color_pair(CP_NORMAL)
    item_y = box_y + 3

    for index, preset in enumerate(statuses):
        y = item_y + index
        if y >= rows - 1:
            break
        is_selected = index == selected
        if confirm_delete and index == delete_target:
            label = f'  Delete "{preset}"? y/n'
            attr = curses.color_pair(CP_WARN) | curses.A_BOLD
        else:
            marker = "> " if is_selected else "  "
            check = "* " if current_status == preset else "  "
            label = f"{marker}{check}{preset}"
            attr = normal_attr | (curses.A_BOLD if is_selected else 0)
        safe_addstr(stdscr, y, box_x + 2, label, attr)

    custom_idx = len(statuses)
    custom_y = item_y + custom_idx
    if custom_y < rows - 1:
        is_selected = selected == custom_idx
        marker = "> " if is_selected else "  "
        if custom_mode:
            prefix = f"{marker}  Custom: "
            safe_addstr(
                stdscr, custom_y, box_x + 2, prefix, normal_attr | curses.A_BOLD
            )

            field_x = box_x + 2 + len(prefix)
            field_w = box_w - len(prefix) - 4
            display = custom_value[:field_w]
            safe_addstr(
                stdscr,
                custom_y,
                field_x,
                display + " " * max(0, field_w - len(display)),
                curses.color_pair(CP_INPUT),
            )
            cursor_x = field_x + min(custom_cursor, field_w - 1)
            curses.curs_set(1)
            with contextlib.suppress(curses.error):
                stdscr.move(custom_y, cursor_x)
        else:
            curses.curs_set(0)
            check = "* " if current_status and current_status not in statuses else "  "
            label = f"{marker}{check}Custom..."
            attr = normal_attr | (curses.A_BOLD if is_selected else 0)
            safe_addstr(stdscr, custom_y, box_x + 2, label, attr)

    clear_idx = len(statuses) + 1
    clear_y = item_y + clear_idx
    if clear_y < rows - 1:
        is_selected = selected == clear_idx
        marker = "> " if is_selected else "  "
        label = f"{marker}  Clear status"
        attr = normal_attr | (curses.A_BOLD if is_selected else 0)
        safe_addstr(stdscr, clear_y, box_x + 2, label, attr)

    footer_y = item_y + total_items + 1
    if footer_y < rows - 1:
        status_display = current_status or "(none)"
        footer_text = f"Current: {status_display}"
        safe_addstr(stdscr, footer_y, box_x + 2, footer_text, dim_attr)
        if not custom_mode:
            hint = "c Clear  d Delete"
            hint_x = box_x + box_w - len(hint) - 2
            if hint_x > box_x + 2 + len(footer_text) + 2:
                safe_addstr(stdscr, footer_y, hint_x, hint, dim_attr)


def run_status_picker(stdscr: curses.window, session: SessionInfo) -> StatusSelection:
    """Curses event loop for the tab status overlay."""
    curses.set_escdelay(25)
    curses.curs_set(0)
    init_colors()
    stdscr.timeout(-1)

    statuses = load_statuses()
    current_status = extract_status(_strip_override_marker(session["title"]))[1]
    total = len(statuses) + 2

    selected = len(statuses)
    if current_status:
        for index, preset in enumerate(statuses):
            if preset == current_status:
                selected = index
                break

    custom_mode = False
    custom_value = ""
    custom_cursor = 0
    confirm_delete = False
    delete_target = -1

    while True:
        draw_status_picker(
            stdscr,
            session,
            statuses,
            selected,
            custom_mode,
            custom_value,
            custom_cursor,
            confirm_delete,
            delete_target,
        )
        stdscr.refresh()

        key = stdscr.getch()
        if key == -1:
            continue

        if custom_mode:
            if key == 27:
                custom_mode = False
                curses.curs_set(0)
            elif key in (10, 13, curses.KEY_ENTER):
                stripped = custom_value.strip()
                if stripped:
                    return ("custom", stripped)
            elif key in (127, 8, curses.KEY_BACKSPACE):
                if custom_cursor > 0:
                    custom_value = (
                        custom_value[: custom_cursor - 1] + custom_value[custom_cursor:]
                    )
                    custom_cursor -= 1
            elif key == curses.KEY_LEFT:
                if custom_cursor > 0:
                    custom_cursor -= 1
            elif key == curses.KEY_RIGHT:
                if custom_cursor < len(custom_value):
                    custom_cursor += 1
            elif key == curses.KEY_HOME or key == 1:
                custom_cursor = 0
            elif key == curses.KEY_END or key == 5:
                custom_cursor = len(custom_value)
            elif 32 <= key <= 126:
                custom_value = (
                    custom_value[:custom_cursor]
                    + chr(key)
                    + custom_value[custom_cursor:]
                )
                custom_cursor += 1
        elif confirm_delete:
            if key == ord("y"):
                statuses.pop(delete_target)
                _save_tab_statuses(statuses)
                total = len(statuses) + 2
                if selected >= len(statuses):
                    selected = max(0, len(statuses) - 1)
            confirm_delete = False
            delete_target = -1
        else:
            if key in (27, ord("q"), ord("Q")):
                return None
            if key in (curses.KEY_UP, ord("k"), ord("K")):
                selected = (selected - 1) % total
            elif key in (curses.KEY_DOWN, ord("j"), ord("J")):
                selected = (selected + 1) % total
            elif key in (10, 13, curses.KEY_ENTER):
                if selected < len(statuses):
                    return ("preset", statuses[selected])
                if selected == len(statuses):
                    custom_mode = True
                    custom_value = ""
                    custom_cursor = 0
                else:
                    return ("clear", "")
            elif key in (ord("c"), ord("C")):
                return ("clear", "")
            elif key in (ord("d"), ord("x")) and selected < len(statuses):
                confirm_delete = True
                delete_target = selected
