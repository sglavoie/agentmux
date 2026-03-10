"""Overlay picker and config picker views."""

import curses
import os

from cc_dashboard import config as _cfg
from cc_dashboard.colors import (
    CP_BORDER,
    CP_DIM,
    CP_HEADER,
    CP_NORMAL,
    _cp_for_palette_color,
)
from cc_dashboard.drawing import draw_border, safe_addstr
from cc_dashboard.filters import SORT_MODES
from cc_dashboard.views_shared import _shorten_home


def draw_override_picker(stdscr, session, override_selected):
    """Draw a config-dir selection overlay for override."""
    rows, cols = stdscr.getmaxyx()
    dirs = _cfg._unique_config_dirs()
    if not dirs:
        return

    home = os.path.expanduser("~")

    def _short(p):
        if p.startswith(home):
            return "~" + p[len(home) :]
        return p

    display_dirs = [_short(d) for d in dirs]

    current_dir = session.get("config_dir", "")
    if not current_dir:
        current_dir = _cfg._accounts.get(session.get("account", ""), {}).get(
            "config_dir", ""
        )

    max_dir_len = max(len(d) for d in display_dirs)
    box_w = min(cols - 4, max(36, max_dir_len + 20))
    box_h = len(dirs) + 4
    box_y = max(0, (rows - box_h) // 2)
    box_x = max(0, (cols - box_w) // 2)

    blank_line = " " * box_w
    dim_attr = curses.color_pair(CP_DIM)
    for dy in range(box_h):
        safe_addstr(stdscr, box_y + dy, box_x, blank_line, dim_attr)
    draw_border(
        stdscr, box_y, box_x, box_w, box_h, curses.color_pair(CP_BORDER) | curses.A_BOLD
    )

    title = "Override Config Dir"
    safe_addstr(
        stdscr,
        box_y,
        box_x + max(0, (box_w - len(title)) // 2),
        title,
        curses.color_pair(CP_HEADER) | curses.A_BOLD,
    )

    sub = f'for "{session["title"]}"'
    safe_addstr(
        stdscr,
        box_y + 1,
        box_x + max(0, (box_w - len(sub)) // 2),
        sub,
        dim_attr,
    )

    normal_attr = curses.color_pair(CP_NORMAL)
    for i, display in enumerate(display_dirs):
        y = box_y + 3 + i
        if y >= rows - 1:
            break
        sel = i == override_selected
        marker = "▸ " if sel else "  "
        suffix = " (current)" if dirs[i] == current_dir else ""
        label = f"{marker}{display}{suffix}"

        attr = normal_attr
        if sel:
            attr |= curses.A_BOLD
        safe_addstr(stdscr, y, box_x + 2, label, attr)


def draw_move_picker(stdscr, session, instance_labels, move_selected, subtitle=None):
    """Draw an instance selection overlay for moving a session."""
    rows, cols = stdscr.getmaxyx()
    current_instance = session.get("instance_label", "")
    targets = [lbl for lbl in instance_labels if lbl != current_instance]
    if not targets:
        return

    max_label_len = max(len(lbl) for lbl in targets)
    box_w = min(cols - 4, max(36, max_label_len + 16))
    box_h = len(targets) + 4
    box_y = max(0, (rows - box_h) // 2)
    box_x = max(0, (cols - box_w) // 2)

    blank_line = " " * box_w
    dim_attr = curses.color_pair(CP_DIM)
    for dy in range(box_h):
        safe_addstr(stdscr, box_y + dy, box_x, blank_line, dim_attr)
    draw_border(
        stdscr, box_y, box_x, box_w, box_h, curses.color_pair(CP_BORDER) | curses.A_BOLD
    )

    title = "Move to Instance"
    safe_addstr(
        stdscr,
        box_y,
        box_x + max(0, (box_w - len(title)) // 2),
        title,
        curses.color_pair(CP_HEADER) | curses.A_BOLD,
    )

    sub = subtitle if subtitle else f'"{session["title"]}" from {current_instance}'
    safe_addstr(
        stdscr,
        box_y + 1,
        box_x + max(0, (box_w - len(sub)) // 2),
        sub,
        dim_attr,
    )

    normal_attr = curses.color_pair(CP_NORMAL)
    for i, lbl in enumerate(targets):
        y = box_y + 3 + i
        if y >= rows - 1:
            break
        sel = i == move_selected
        marker = "▸ " if sel else "  "
        attr = normal_attr | (curses.A_BOLD if sel else 0)
        safe_addstr(stdscr, y, box_x + 2, f"{marker}{lbl}", attr)


def draw_session_action_picker(stdscr, subtitle, action_labels, selected):
    """Draw a centered action picker for selected or marked sessions."""
    rows, cols = stdscr.getmaxyx()
    if not action_labels:
        return

    max_action_len = max(len(label) for label in action_labels)
    title = "Session Actions"
    subtitle = subtitle or ""

    box_w = min(cols - 4, max(42, max(max_action_len, len(subtitle)) + 12))
    box_h = len(action_labels) + 4
    box_y = max(0, (rows - box_h) // 2)
    box_x = max(0, (cols - box_w) // 2)

    blank_line = " " * box_w
    dim_attr = curses.color_pair(CP_DIM)
    for dy in range(box_h):
        safe_addstr(stdscr, box_y + dy, box_x, blank_line, dim_attr)
    draw_border(
        stdscr, box_y, box_x, box_w, box_h, curses.color_pair(CP_BORDER) | curses.A_BOLD
    )

    safe_addstr(
        stdscr,
        box_y,
        box_x + max(0, (box_w - len(title)) // 2),
        title,
        curses.color_pair(CP_HEADER) | curses.A_BOLD,
    )
    safe_addstr(
        stdscr,
        box_y + 1,
        box_x + max(0, (box_w - len(subtitle)) // 2),
        subtitle[: box_w - 2],
        dim_attr,
    )

    normal_attr = curses.color_pair(CP_NORMAL)
    for i, label in enumerate(action_labels):
        y = box_y + 3 + i
        if y >= rows - 1:
            break
        is_selected = i == selected
        marker = "▸ " if is_selected else "  "
        attr = normal_attr | (curses.A_BOLD if is_selected else 0)
        safe_addstr(stdscr, y, box_x + 2, f"{marker}{label}"[: box_w - 4], attr)


def draw_config_picker(stdscr, config_entries, selected):
    """Draw a centered config file picker overlay."""
    rows, cols = stdscr.getmaxyx()
    if not config_entries:
        return

    display_rows = []
    for label, path in config_entries:
        suffix = "" if os.path.isfile(path) else " (missing)"
        display_rows.append(f"{label}: {_shorten_home(path)}{suffix}")

    max_row_len = max(len(r) for r in display_rows)
    box_w = min(cols - 4, max(40, max_row_len + 10))
    box_h = len(config_entries) + 3
    box_y = max(0, (rows - box_h) // 2)
    box_x = max(0, (cols - box_w) // 2)

    blank_line = " " * box_w
    dim_attr = curses.color_pair(CP_DIM)
    for dy in range(box_h):
        safe_addstr(stdscr, box_y + dy, box_x, blank_line, dim_attr)
    draw_border(
        stdscr, box_y, box_x, box_w, box_h, curses.color_pair(CP_BORDER) | curses.A_BOLD
    )

    title = "Config Files"
    safe_addstr(
        stdscr,
        box_y,
        box_x + max(0, (box_w - len(title)) // 2),
        title,
        curses.color_pair(CP_HEADER) | curses.A_BOLD,
    )

    normal_attr = curses.color_pair(CP_NORMAL)
    for i, display in enumerate(display_rows):
        y = box_y + 2 + i
        if y >= rows - 1:
            break
        sel = i == selected
        marker = "▸ " if sel else "  "
        safe_addstr(
            stdscr,
            y,
            box_x + 2,
            f"{marker}{display}"[: box_w - 4],
            normal_attr | (curses.A_BOLD if sel else 0),
        )


_SORT_DESCRIPTIONS = {
    "default": "Default Kitty tab-bar order",
    "name": "Alphabetical by title",
    "account": "Grouped by account, alpha within",
    "group": "Grouped by session group, alpha within",
    "status": "Error → permission → active → idle → stopped",
    "idle": "Most idle first",
    "activity": "Most recently active first",
}

_SORT_DISPLAY_LABELS = {
    "default": "Tab order",
    "name": "Name",
    "account": "Account",
    "group": "Group",
    "status": "Status",
    "idle": "Idle",
    "activity": "Activity",
}


def draw_sort_picker(stdscr, selected, current_mode):
    """Draw a centered sort mode picker overlay."""
    rows, cols = stdscr.getmaxyx()
    label_col = max(len(v) for v in _SORT_DISPLAY_LABELS.values()) + 2

    display_rows = []
    for mode in SORT_MODES:
        check = "✓ " if mode == current_mode else "  "
        label = _SORT_DISPLAY_LABELS[mode].ljust(label_col)
        display_rows.append(f"{check}{label}{_SORT_DESCRIPTIONS[mode]}")

    max_row_len = max(len(r) for r in display_rows)
    box_w = min(cols - 4, max(40, max_row_len + 10))
    box_h = len(SORT_MODES) + 3
    box_y = max(0, (rows - box_h) // 2)
    box_x = max(0, (cols - box_w) // 2)

    blank_line = " " * box_w
    dim_attr = curses.color_pair(CP_DIM)
    for dy in range(box_h):
        safe_addstr(stdscr, box_y + dy, box_x, blank_line, dim_attr)
    draw_border(
        stdscr, box_y, box_x, box_w, box_h, curses.color_pair(CP_BORDER) | curses.A_BOLD
    )

    title = "Sort By"
    safe_addstr(
        stdscr,
        box_y,
        box_x + max(0, (box_w - len(title)) // 2),
        title,
        curses.color_pair(CP_HEADER) | curses.A_BOLD,
    )

    normal_attr = curses.color_pair(CP_NORMAL)
    for i, display in enumerate(display_rows):
        y = box_y + 2 + i
        if y >= rows - 1:
            break
        sel = i == selected
        marker = "▸ " if sel else "  "
        safe_addstr(
            stdscr,
            y,
            box_x + 2,
            f"{marker}{display}"[: box_w - 4],
            normal_attr | (curses.A_BOLD if sel else 0),
        )


def draw_group_picker(stdscr, sessions, group_filter):
    """Draw a centered group filter picker overlay."""
    rows, cols = stdscr.getmaxyx()
    groups = sorted({s.get("group", "") or "(ungrouped)" for s in sessions})
    if not groups:
        return

    display_rows = []
    for i, name in enumerate(groups):
        if i >= 9:
            break
        enabled = group_filter is None or name in group_filter
        check = "[x] " if enabled else "[ ] "
        display_rows.append((f"{i + 1}: {check}{name}", enabled))

    max_row_len = max(len(r[0]) for r in display_rows)
    box_w = min(cols - 4, max(30, max_row_len + 8))
    box_h = len(display_rows) + 3
    box_y = max(0, (rows - box_h) // 2)
    box_x = max(0, (cols - box_w) // 2)

    blank_line = " " * box_w
    dim_attr = curses.color_pair(CP_DIM)
    for dy in range(box_h):
        safe_addstr(stdscr, box_y + dy, box_x, blank_line, dim_attr)
    draw_border(
        stdscr, box_y, box_x, box_w, box_h, curses.color_pair(CP_BORDER) | curses.A_BOLD
    )

    title = "Groups"
    safe_addstr(
        stdscr,
        box_y,
        box_x + max(0, (box_w - len(title)) // 2),
        title,
        curses.color_pair(CP_HEADER) | curses.A_BOLD,
    )

    normal_attr = curses.color_pair(CP_NORMAL)
    for i, (display, enabled) in enumerate(display_rows):
        y = box_y + 2 + i
        if y >= rows - 1:
            break
        attr = normal_attr | curses.A_BOLD if enabled else dim_attr
        safe_addstr(stdscr, y, box_x + 2, display[: box_w - 4], attr)


def draw_color_picker(
    stdscr, session, palette_names, current_color, account_color, selected
):
    """Draw a color selection overlay for tab color override."""
    rows, cols = stdscr.getmaxyx()
    total_items = len(palette_names) + 1  # +1 for reset option
    box_w = min(cols - 4, max(36, 30))
    box_h = total_items + 5  # title + subtitle + blank + items + blank
    box_y = max(0, (rows - box_h) // 2)
    box_x = max(0, (cols - box_w) // 2)

    blank_line = " " * box_w
    dim_attr = curses.color_pair(CP_DIM)
    for dy in range(box_h):
        safe_addstr(stdscr, box_y + dy, box_x, blank_line, dim_attr)
    draw_border(
        stdscr, box_y, box_x, box_w, box_h, curses.color_pair(CP_BORDER) | curses.A_BOLD
    )

    title = "Set Tab Color"
    safe_addstr(
        stdscr,
        box_y,
        box_x + max(0, (box_w - len(title)) // 2),
        title,
        curses.color_pair(CP_HEADER) | curses.A_BOLD,
    )

    sub = f'for "{session["title"]}"'
    safe_addstr(
        stdscr,
        box_y + 1,
        box_x + max(0, (box_w - len(sub)) // 2),
        sub,
        dim_attr,
    )

    for i, color_name in enumerate(palette_names):
        y = box_y + 3 + i
        if y >= rows - 1:
            break
        sel = i == selected
        marker = "▸ " if sel else "  "
        check = "✓ " if color_name == current_color else "  "
        label = f"{marker}{check}{color_name}"

        attr = curses.color_pair(_cp_for_palette_color(color_name, selected=sel))
        if sel:
            attr |= curses.A_BOLD
        safe_addstr(stdscr, y, box_x + 2, label, attr)

    # Reset option
    reset_y = box_y + 3 + len(palette_names)
    if reset_y < rows - 1:
        sel = selected == len(palette_names)
        marker = "▸ " if sel else "  "
        reset_label = f"{marker}  Reset to default ({account_color})"
        normal_attr = curses.color_pair(CP_NORMAL)
        if sel:
            normal_attr |= curses.A_BOLD
        safe_addstr(stdscr, reset_y, box_x + 2, reset_label, normal_attr)


def draw_config_action_picker(stdscr, label, path, selected):
    """Draw a centered action picker for a selected config file."""
    rows, cols = stdscr.getmaxyx()
    actions = ["Show contents", "Edit in editor", "Show file path"]
    subtitle = f"{label}: {_shorten_home(path)}"

    max_action_len = max(len(a) for a in actions)
    box_w = min(cols - 4, max(40, max(max_action_len, len(subtitle)) + 10))
    box_h = len(actions) + 4
    box_y = max(0, (rows - box_h) // 2)
    box_x = max(0, (cols - box_w) // 2)

    blank_line = " " * box_w
    dim_attr = curses.color_pair(CP_DIM)
    for dy in range(box_h):
        safe_addstr(stdscr, box_y + dy, box_x, blank_line, dim_attr)
    draw_border(
        stdscr, box_y, box_x, box_w, box_h, curses.color_pair(CP_BORDER) | curses.A_BOLD
    )

    title = f"Config: {label}"
    safe_addstr(
        stdscr,
        box_y,
        box_x + max(0, (box_w - len(title)) // 2),
        title,
        curses.color_pair(CP_HEADER) | curses.A_BOLD,
    )
    safe_addstr(
        stdscr,
        box_y + 1,
        box_x + max(0, (box_w - len(subtitle)) // 2),
        subtitle[: box_w - 2],
        dim_attr,
    )

    normal_attr = curses.color_pair(CP_NORMAL)
    for i, action in enumerate(actions):
        y = box_y + 3 + i
        if y >= rows - 1:
            break
        sel = i == selected
        marker = "▸ " if sel else "  "
        safe_addstr(
            stdscr,
            y,
            box_x + 2,
            f"{marker}{action}",
            normal_attr | (curses.A_BOLD if sel else 0),
        )
