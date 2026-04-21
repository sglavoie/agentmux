"""Session-oriented picker overlays."""

import curses

from cc_dashboard import config as _cfg
from cc_dashboard.colors import CP_NORMAL, _cp_for_palette_color
from cc_dashboard.drawing import safe_addstr
from cc_dashboard.filters import _session_groups
from cc_dashboard.views_overlays_pickers_shared import (
    centered_box,
    draw_overlay_box,
    draw_overlay_title,
)


def draw_move_picker(stdscr, session, instance_labels, move_selected, subtitle=None):
    """Draw an instance selection overlay for moving a session."""
    rows, cols = stdscr.getmaxyx()
    current_instance = session.get("instance_label", "")
    targets = [label for label in instance_labels if label != current_instance]
    if not targets:
        return

    max_label_len = max(len(label) for label in targets)
    box_w = min(cols - 4, max(36, max_label_len + 16))
    box_h = len(targets) + 4
    _, _, box_y, box_x = centered_box(stdscr, box_w, box_h)

    dim_attr = draw_overlay_box(stdscr, box_y, box_x, box_w, box_h)
    sub = subtitle if subtitle else f'"{session["title"]}" from {current_instance}'
    draw_overlay_title(stdscr, box_y, box_x, box_w, "Move to Instance", sub, dim_attr)

    normal_attr = curses.color_pair(CP_NORMAL)
    for i, label in enumerate(targets):
        y = box_y + 3 + i
        if y >= rows - 1:
            break
        marker = "> " if i == move_selected else "  "
        attr = normal_attr | (curses.A_BOLD if i == move_selected else 0)
        safe_addstr(stdscr, y, box_x + 2, f"{marker}{label}", attr)


def draw_session_action_picker(stdscr, subtitle, action_labels, selected):
    """Draw a centered action picker for selected or marked sessions."""
    if not action_labels:
        return

    rows, cols = stdscr.getmaxyx()
    title = "Session Actions"
    subtitle = subtitle or ""
    max_action_len = max(len(label) for label in action_labels)
    box_w = min(cols - 4, max(42, max(max_action_len, len(subtitle)) + 12))
    box_h = len(action_labels) + 4
    _, _, box_y, box_x = centered_box(stdscr, box_w, box_h)

    dim_attr = draw_overlay_box(stdscr, box_y, box_x, box_w, box_h)
    draw_overlay_title(stdscr, box_y, box_x, box_w, title, subtitle, dim_attr)

    normal_attr = curses.color_pair(CP_NORMAL)
    for i, label in enumerate(action_labels):
        y = box_y + 3 + i
        if y >= rows - 1:
            break
        marker = "> " if i == selected else "  "
        attr = normal_attr | (curses.A_BOLD if i == selected else 0)
        safe_addstr(stdscr, y, box_x + 2, f"{marker}{label}"[: box_w - 4], attr)


def draw_color_picker(
    stdscr,
    session,
    palette_names,
    current_color,
    account_color,
    selected,
    subtitle=None,
):
    """Draw a color selection overlay for tab color override."""
    rows, cols = stdscr.getmaxyx()
    total_items = len(palette_names) + 1
    box_w = min(cols - 4, 36)
    box_h = total_items + 5
    _, _, box_y, box_x = centered_box(stdscr, box_w, box_h)

    dim_attr = draw_overlay_box(stdscr, box_y, box_x, box_w, box_h)
    sub = subtitle if subtitle else f'for "{session["title"]}"'
    draw_overlay_title(stdscr, box_y, box_x, box_w, "Set Tab Color", sub, dim_attr)

    for i, color_name in enumerate(palette_names):
        y = box_y + 3 + i
        if y >= rows - 1:
            break
        marker = "> " if i == selected else "  "
        check = "* " if color_name == current_color else "  "
        attr = curses.color_pair(
            _cp_for_palette_color(color_name, selected=i == selected)
        )
        if i == selected:
            attr |= curses.A_BOLD
        safe_addstr(stdscr, y, box_x + 2, f"{marker}{check}{color_name}", attr)

    reset_y = box_y + 3 + len(palette_names)
    if reset_y < rows - 1:
        marker = "> " if selected == len(palette_names) else "  "
        attr = curses.color_pair(CP_NORMAL)
        if selected == len(palette_names):
            attr |= curses.A_BOLD
        safe_addstr(
            stdscr,
            reset_y,
            box_x + 2,
            f"{marker}  Reset to default ({account_color})",
            attr,
        )


def draw_group_actions(stdscr, state):
    """Draw the group actions overlay (pick group or pick action)."""
    rows, cols = stdscr.getmaxyx()
    step = state.get("ga_step", "pick_group")
    groups = state.get("ga_groups", [])
    ga_selected = state.get("ga_selected", 0)
    sessions = state.get("sessions", [])
    cfg_sessions = state.get("cfg", _cfg)._sessions

    if step == "pick_group":
        display_rows = []
        for group_name in groups:
            n_running = sum(
                1 for session in sessions if group_name in _session_groups(session)
            )
            n_config = sum(
                1 for session in cfg_sessions if group_name in _session_groups(session)
            )
            display_rows.append(
                f"{group_name} ({n_running} running / {n_config} in config)"
            )

        if not display_rows:
            return

        max_row_len = max(len(row) for row in display_rows)
        box_w = min(cols - 4, max(42, max_row_len + 10))
        box_h = len(display_rows) + 3
        _, _, box_y, box_x = centered_box(stdscr, box_w, box_h)

        draw_overlay_box(stdscr, box_y, box_x, box_w, box_h)
        draw_overlay_title(stdscr, box_y, box_x, box_w, "Group Actions")

        normal_attr = curses.color_pair(CP_NORMAL)
        for i, display in enumerate(display_rows):
            y = box_y + 2 + i
            if y >= rows - 1:
                break
            marker = ">" if i == ga_selected else " "
            attr = normal_attr | (curses.A_BOLD if i == ga_selected else 0)
            safe_addstr(stdscr, y, box_x + 2, f"{marker} {display}"[: box_w - 4], attr)
        return

    if step != "pick_action":
        return

    from cc_dashboard.loop_modes_dashboard_ops import _ga_build_actions

    group_name = groups[ga_selected] if ga_selected < len(groups) else ""
    actions = _ga_build_actions(state, group_name)

    if not actions:
        title = f"Group: {group_name}"
        box_w = min(cols - 4, max(42, len(title) + 10))
        box_h = 4
        _, _, box_y, box_x = centered_box(stdscr, box_w, box_h)
        dim_attr = draw_overlay_box(stdscr, box_y, box_x, box_w, box_h)
        draw_overlay_title(stdscr, box_y, box_x, box_w, title)
        safe_addstr(stdscr, box_y + 2, box_x + 2, "No actions available", dim_attr)
        return

    action_labels = [label for _, label in actions]
    ga_action_selected = state.get("ga_action_selected", 0)
    max_action_len = max(len(label) for label in action_labels)
    title = f"Group: {group_name}"
    box_w = min(cols - 4, max(42, max(max_action_len, len(title)) + 10))
    box_h = len(action_labels) + 3
    _, _, box_y, box_x = centered_box(stdscr, box_w, box_h)

    draw_overlay_box(stdscr, box_y, box_x, box_w, box_h)
    draw_overlay_title(stdscr, box_y, box_x, box_w, title)

    normal_attr = curses.color_pair(CP_NORMAL)
    for i, label in enumerate(action_labels):
        y = box_y + 2 + i
        if y >= rows - 1:
            break
        marker = ">" if i == ga_action_selected else " "
        attr = normal_attr | (curses.A_BOLD if i == ga_action_selected else 0)
        safe_addstr(stdscr, y, box_x + 2, f"{marker} {label}"[: box_w - 4], attr)
