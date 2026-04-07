"""Rendering helpers for list and action dashboard modes."""

import curses

from cc_dashboard.colors import CP_NORMAL, CP_WARN
from cc_dashboard.drawing import safe_addstr
from cc_dashboard.filters import SORT_LABELS
from cc_dashboard.views import (
    DETAIL_PANE_HEIGHT,
    draw_batch_confirm_prompt,
    draw_batch_group_input,
    draw_clone_input,
    draw_color_picker,
    draw_config_action_picker,
    draw_config_picker,
    draw_confirm_prompt,
    draw_detail_pane,
    draw_filter_bar,
    draw_footer,
    draw_group_actions,
    draw_group_filter_bar,
    draw_group_picker,
    draw_list_view,
    draw_move_picker,
    draw_override_picker,
    draw_rename_input,
    draw_restart_options_form,
    draw_search_bar,
    draw_session_action_picker,
    draw_sort_picker,
    draw_status_message,
    draw_worktree_input,
)

LIST_MODES = {
    "list",
    "actions",
    "restart_options",
    "confirm",
    "confirm_restart",
    "confirm_batch_close",
    "confirm_batch_restart",
    "confirm_batch_clone",
    "confirm_save",
    "rename",
    "worktree",
    "clone",
    "move_picker",
    "config_pick",
    "config_action",
    "sort_pick",
    "color_picker",
    "batch_group",
    "group_actions",
}

OVERLAY_MODES = {
    "actions",
    "restart_options",
    "rename",
    "worktree",
    "clone",
    "move_picker",
    "config_pick",
    "config_action",
    "sort_pick",
    "color_picker",
    "batch_group",
    "override",
    "group_actions",
    "confirm",
    "confirm_restart",
    "confirm_batch_close",
    "confirm_batch_restart",
    "confirm_batch_clone",
    "confirm_save",
}


def is_list_mode(state):
    """Return whether the current state should render the list-mode layout."""
    mode = state["mode"]
    return mode in LIST_MODES or (
        mode == "override" and state["override_source"] in ("list", "batch")
    )


def render_list_modes(stdscr, state, build_session_actions):
    """Render list and list-overlay modes."""
    if not is_list_mode(state):
        return False

    mode = state["mode"]
    rows, _cols = stdscr.getmaxyx()
    filtered = state["filtered"]
    selected = state["selected"]
    detail_visible = (
        state["show_detail"]
        and rows >= 20
        and bool(filtered)
        and mode not in OVERLAY_MODES
    )
    draw_list_view(
        stdscr,
        filtered,
        selected,
        detail_visible=detail_visible,
        sort_label=SORT_LABELS[state["sort_mode"]],
        marked=state["marked"],
        collapsed_groups=state.get("collapsed_groups", set()),
        sort_mode=state["sort_mode"],
        group_filter=state.get("group_filter"),
    )
    if detail_visible:
        detail_y = rows - 1 - DETAIL_PANE_HEIGHT
        draw_detail_pane(stdscr, filtered[selected], detail_y)
    if state["msg"] and state["msg_frames"] > 0:
        draw_status_message(stdscr, state["msg"])
    elif state["search_active"] or state["search_query"]:
        draw_search_bar(
            stdscr,
            state["search_query"],
            state["search_cursor"],
            state["search_active"],
            len(filtered),
        )
    if state["account_filter"] is not None or state["account_filter_mode"]:
        draw_filter_bar(
            stdscr,
            state["account_filter"],
            state["account_filter_mode"],
        )
    if state["account_filter_mode"]:
        rows_now, cols_now = stdscr.getmaxyx()
        safe_addstr(
            stdscr,
            rows_now - 1,
            0,
            " " * (cols_now - 1),
            curses.color_pair(CP_NORMAL),
        )
        draw_footer(stdscr, "filter")
    if state.get("group_filter") is not None and not state.get("group_filter_mode"):
        draw_group_filter_bar(
            stdscr,
            state["sessions"],
            state.get("group_filter"),
            False,
        )
    if state.get("group_filter_mode"):
        draw_group_picker(
            stdscr,
            state["sessions"],
            state.get("group_filter"),
        )
        rows_now, cols_now = stdscr.getmaxyx()
        safe_addstr(
            stdscr,
            rows_now - 1,
            0,
            " " * (cols_now - 1),
            curses.color_pair(CP_NORMAL),
        )
        draw_footer(stdscr, "group_filter")

    if mode == "confirm" and filtered:
        draw_confirm_prompt(stdscr, filtered[selected], "Close")
    elif mode == "confirm_restart" and filtered:
        draw_confirm_prompt(stdscr, filtered[selected], "Restart")
    elif mode == "confirm_batch_close":
        draw_batch_confirm_prompt(stdscr, state["batch_targets"], "Close")
        draw_footer(stdscr, "confirm_batch_close")
    elif mode == "confirm_batch_restart":
        draw_batch_confirm_prompt(stdscr, state["batch_targets"], "Restart")
        draw_footer(stdscr, "confirm_batch_restart")
    elif mode == "confirm_batch_clone":
        draw_batch_confirm_prompt(stdscr, state["batch_targets"], "Clone")
        draw_footer(stdscr, "confirm_batch_clone")
    elif mode == "confirm_save":
        rows_now, _ = stdscr.getmaxyx()
        fname = state["cfg"]._sessions_config_filename()
        prompt = f"Save {len(state['sessions'])} session(s) to {fname}? y/n"
        safe_addstr(
            stdscr,
            rows_now - 3,
            2,
            prompt,
            curses.color_pair(CP_WARN) | curses.A_BOLD,
        )
    elif mode == "rename":
        draw_rename_input(stdscr, state["rename_value"], state["rename_cursor"])
        draw_footer(stdscr, "rename")
    elif mode == "worktree" and filtered:
        draw_worktree_input(
            stdscr,
            filtered[selected],
            state["worktree_branch"],
            state["worktree_cursor"],
        )
        draw_footer(stdscr, "worktree")
    elif mode == "clone" and filtered:
        draw_clone_input(
            stdscr,
            filtered[selected],
            state["clone_title"],
            state["clone_cursor"],
        )
        draw_footer(stdscr, "clone")
    elif mode == "batch_group":
        draw_batch_group_input(
            stdscr,
            state["actions_targets"],
            state["batch_group_value"],
            state["batch_group_cursor"],
        )
        draw_footer(stdscr, "batch_group")
    elif mode == "override" and filtered:
        batch_count = (
            len(state.get("batch_targets", []))
            if state.get("override_source") == "batch"
            else None
        )
        draw_override_picker(
            stdscr,
            filtered[selected],
            state["override_selected"],
            batch_count=batch_count,
        )
        draw_footer(stdscr, "override")
    elif mode == "move_picker" and filtered:
        subtitle = None
        if state["actions_scope"] == "marked" and state["actions_targets"]:
            subtitle = f"{len(state['actions_targets'])} marked session(s)"
        draw_move_picker(
            stdscr,
            filtered[selected],
            state["instance_labels"],
            state["move_selected"],
            subtitle=subtitle,
        )
        draw_footer(stdscr, "move")
    elif mode == "config_pick":
        draw_config_picker(
            stdscr,
            state["cfg"]._all_config_paths(),
            state["config_selected"],
        )
        draw_footer(stdscr, "config_pick")
    elif mode == "config_action":
        draw_config_action_picker(
            stdscr,
            state["config_file_label"],
            state["config_file_path"],
            state["config_action_selected"],
        )
        draw_footer(stdscr, "config_action")
    elif mode == "sort_pick":
        draw_sort_picker(stdscr, state["sort_selected"], state["sort_mode"])
        draw_footer(stdscr, "sort_pick")
    elif mode == "color_picker" and filtered:
        session = filtered[selected]
        acct_info = state["cfg"]._accounts.get(session.get("account", ""), {})
        account_color = acct_info.get("color", "gray")
        current_color = session.get("cc_color", "") or account_color
        subtitle = None
        if state.get("actions_scope") == "marked" and state.get("actions_targets"):
            subtitle = f"{len(state['actions_targets'])} marked session(s)"
        draw_color_picker(
            stdscr,
            session,
            state["palette_names"],
            current_color,
            account_color,
            state["color_selected"],
            subtitle=subtitle,
        )
        draw_footer(stdscr, "color_picker")
    elif mode == "actions" and filtered:
        session = filtered[selected]
        action_labels = [
            label for _, label in build_session_actions(session, state["actions_scope"])
        ]
        if state["actions_scope"] == "marked" and state["actions_targets"]:
            subtitle = f"{len(state['actions_targets'])} marked session(s)"
        else:
            subtitle = session.get("title", "")
        draw_session_action_picker(
            stdscr,
            subtitle,
            action_labels,
            state["actions_selected"],
        )
        draw_footer(stdscr, "actions")
    elif mode == "restart_options" and filtered:
        draw_restart_options_form(
            stdscr,
            filtered[selected],
            state["restart_options_fields"],
            state["restart_options_active"],
            state["restart_options_cursor"],
            multi_instance=state["multi_instance"],
        )
    elif mode == "group_actions":
        draw_group_actions(stdscr, state)
        draw_footer(stdscr, "group_actions")

    return True
