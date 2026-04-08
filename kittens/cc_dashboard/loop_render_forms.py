"""Rendering helpers for form, picker, and config dashboard modes."""

import curses

from cc_dashboard.colors import CP_WARN
from cc_dashboard.drawing import safe_addstr
from cc_dashboard.views import (
    draw_config_show,
    draw_delete_confirm,
    draw_delete_view,
    draw_dir_dropdown,
    draw_footer,
    draw_group_picker,
    draw_new_form,
    draw_override_picker,
    draw_peek_overlay,
    draw_pick_view,
    draw_search_bar,
    draw_status_message,
)


def render_form_modes(stdscr, state):
    """Render config, form, picker, and delete modes."""
    mode = state["mode"]

    if mode == "config_show":
        draw_config_show(
            stdscr,
            state["config_file_label"],
            state["config_file_path"],
            state["config_show_lines"],
            state["config_show_scroll"],
        )
        return True

    if mode == "peek":
        draw_peek_overlay(
            stdscr,
            state["peek_session_title"],
            state["peek_lines"],
            state["peek_scroll"],
            state["peek_extent"],
            error=state["peek_error"],
            search_query=state["peek_search_query"],
            search_active=state["peek_search_active"],
            search_cursor=state["peek_search_cursor"],
            match_line_indices=state["peek_match_lines"],
            match_index=state["peek_match_index"],
        )
        return True

    if mode == "new":
        form_title = "Edit Session" if state["form_mode"] == "edit" else "New Session"
        draw_new_form(
            stdscr,
            state["new_fields"],
            state["new_active"],
            state["new_cursor"],
            multi_instance=(state["multi_instance"] and state["form_mode"] == "new"),
            form_title=form_title,
        )
        if state["dir_browse_active"]:
            draw_dir_dropdown(
                stdscr,
                state["dir_browse_entries"],
                state["dir_browse_selected"],
                state["dir_browse_scroll"],
            )
            draw_footer(stdscr, "new_dir_browse")
        return True

    if mode == "confirm_mkdir":
        draw_new_form(
            stdscr,
            state["new_fields"],
            state["new_active"],
            state["new_cursor"],
            multi_instance=state["multi_instance"],
            form_title="New Session",
        )
        rows_now, cols_now = stdscr.getmaxyx()
        path = state.get("mkdir_path", "")
        max_width = cols_now - 6
        display_path = (
            path if len(path) <= max_width else "..." + path[-(max_width - 3) :]
        )
        prompt = f"Directory does not exist: {display_path}"
        safe_addstr(
            stdscr,
            rows_now - 4,
            2,
            prompt,
            curses.color_pair(CP_WARN) | curses.A_BOLD,
        )
        safe_addstr(
            stdscr,
            rows_now - 3,
            2,
            "Create it? y/n",
            curses.color_pair(CP_WARN) | curses.A_BOLD,
        )
        draw_footer(stdscr, "confirm_mkdir")
        return True

    if mode == "pick" or (mode == "override" and state["override_source"] == "pick"):
        draw_pick_view(
            stdscr,
            state["pick_filtered"],
            state["pick_selected"],
            state["pick_running_count"],
            marked=state.get("pick_marked", set()),
            group_filter=state.get("pick_group_filter"),
        )
        if state["msg"] and state["msg_frames"] > 0:
            draw_status_message(stdscr, state["msg"])
        elif (
            state["multi_instance"]
            and not state["pick_search_active"]
            and not state["pick_query"]
        ):
            draw_status_message(
                stdscr,
                f"Target: {state['pick_target_instance']}  (i to cycle)",
            )
        elif state["pick_search_active"] or state["pick_query"]:
            draw_search_bar(
                stdscr,
                state["pick_query"],
                state["pick_cursor"],
                state["pick_search_active"],
                len(state["pick_filtered"]),
            )
        if state.get("pick_group_filter_mode"):
            draw_group_picker(
                stdscr,
                state["cfg"]._sessions,
                state.get("pick_group_filter"),
            )
            draw_footer(stdscr, "pick_group_filter")
        if mode == "override" and state["pick_filtered"]:
            from cc_dashboard.loop_modes_forms_shared import _get_pick_entry

            pick_entry = _get_pick_entry(state)
            if pick_entry is not None:
                draw_override_picker(
                    stdscr,
                    pick_entry,
                    state["override_selected"],
                )
                draw_footer(stdscr, "override")
        return True

    if mode == "delete":
        draw_delete_view(
            stdscr,
            state["delete_filtered"],
            state["delete_selected"],
            state["delete_running_count"],
        )
        if state["delete_step"] in ("confirm_config", "confirm_data"):
            draw_delete_confirm(
                stdscr,
                state["delete_step"],
                state["delete_target_entry"],
                state["delete_warnings"],
            )
            draw_footer(stdscr, "delete_confirm")
        elif state["msg"] and state["msg_frames"] > 0:
            draw_status_message(stdscr, state["msg"])
        elif state["delete_search_active"] or state["delete_query"]:
            draw_search_bar(
                stdscr,
                state["delete_query"],
                state["delete_cursor"],
                state["delete_search_active"],
                len(state["delete_filtered"]),
            )
        return True

    return False
