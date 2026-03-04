"""Rendering helpers for the dashboard event loop."""

import curses

from cc_dashboard.colors import CP_NORMAL, CP_WARN
from cc_dashboard.drawing import safe_addstr
from cc_dashboard.filters import SORT_LABELS
from cc_dashboard.views import (
    DETAIL_PANE_HEIGHT,
    draw_actlog_browse,
    draw_actlog_view,
    draw_batch_confirm_prompt,
    draw_clone_input,
    draw_color_picker,
    draw_config_action_picker,
    draw_config_picker,
    draw_config_show,
    draw_confirm_prompt,
    draw_delete_confirm,
    draw_delete_view,
    draw_detail_pane,
    draw_dir_dropdown,
    draw_filter_bar,
    draw_footer,
    draw_hooks_add_form,
    draw_hooks_delete_confirm,
    draw_hooks_event_view,
    draw_hooks_example,
    draw_hooks_info,
    draw_hooks_matcher_picker,
    draw_hooks_sync_confirm,
    draw_hooks_view,
    draw_list_view,
    draw_move_picker,
    draw_new_form,
    draw_override_picker,
    draw_peek_overlay,
    draw_pick_view,
    draw_rename_input,
    draw_restart_options_form,
    draw_search_bar,
    draw_session_action_picker,
    draw_sort_picker,
    draw_status_message,
    draw_worktree_input,
)


def draw_dashboard_frame(stdscr, state, build_session_actions):
    """Render one dashboard frame for the current mode/state."""
    mode = state["mode"]

    if mode in (
        "list",
        "actions",
        "restart_options",
        "confirm",
        "confirm_restart",
        "confirm_batch_close",
        "confirm_batch_restart",
        "confirm_save",
        "rename",
        "worktree",
        "clone",
        "move_picker",
        "config_pick",
        "config_action",
        "sort_pick",
        "color_picker",
    ) or (mode == "override" and state["override_source"] == "list"):
        rows, _cols = stdscr.getmaxyx()
        filtered = state["filtered"]
        selected = state["selected"]
        detail_visible = state["show_detail"] and rows >= 20 and bool(filtered)
        draw_list_view(
            stdscr,
            filtered,
            selected,
            detail_visible=detail_visible,
            sort_label=SORT_LABELS[state["sort_mode"]],
            marked=state["marked"],
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
        elif mode == "override" and filtered:
            draw_override_picker(stdscr, filtered[selected], state["override_selected"])
            draw_footer(stdscr, "override")
        elif mode == "move_picker" and filtered:
            move_subtitle = None
            if state["actions_scope"] == "marked" and state["actions_targets"]:
                move_subtitle = f"{len(state['actions_targets'])} marked session(s)"
            draw_move_picker(
                stdscr,
                filtered[selected],
                state["instance_labels"],
                state["move_selected"],
                subtitle=move_subtitle,
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
            sess = filtered[selected]
            acct_info = state["cfg"]._accounts.get(sess.get("account", ""), {})
            account_color = acct_info.get("color", "gray")
            current_color = sess.get("cc_color", "") or account_color
            draw_color_picker(
                stdscr, sess, state["palette_names"],
                current_color, account_color, state["color_selected"],
            )
            draw_footer(stdscr, "color_picker")
        elif mode == "actions" and filtered:
            sess = filtered[selected]
            action_labels = [
                label
                for _, label in build_session_actions(
                    sess,
                    state["actions_scope"],
                )
            ]
            if state["actions_scope"] == "marked" and state["actions_targets"]:
                action_subtitle = f"{len(state['actions_targets'])} marked session(s)"
            else:
                action_subtitle = sess.get("title", "")
            draw_session_action_picker(
                stdscr,
                action_subtitle,
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
    elif mode == "config_show":
        draw_config_show(
            stdscr,
            state["config_file_label"],
            state["config_file_path"],
            state["config_show_lines"],
            state["config_show_scroll"],
        )
    elif mode == "peek":
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
    elif mode == "new":
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
                state["dir_browse_show_hidden"],
            )
            draw_footer(stdscr, "new_dir_browse")
    elif mode == "pick" or (mode == "override" and state["override_source"] == "pick"):
        draw_pick_view(
            stdscr,
            state["pick_filtered"],
            state["pick_selected"],
            state["pick_running_count"],
            marked=state.get("pick_marked", set()),
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
        if mode == "override" and state["pick_filtered"]:
            draw_override_picker(
                stdscr,
                state["pick_filtered"][state["pick_selected"]],
                state["override_selected"],
            )
            draw_footer(stdscr, "override")
    elif mode == "delete":
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
    elif mode == "hooks":
        draw_hooks_view(
            stdscr,
            state["cfg"]._accounts,
            state["hooks_drift"],
            state["cfg"]._hooks_global,
            state["cfg"]._hooks_overrides,
            state["hooks_selected"],
        )
    elif mode == "hooks_event":
        draw_hooks_event_view(
            stdscr,
            state["hooks_account"],
            state["cfg"]._hooks_global,
            state["cfg"]._hooks_overrides,
            state["hooks_drift"].get(state["hooks_account"], "missing"),
            state["hooks_event_selected"],
        )
    elif mode == "hooks_delete_confirm":
        draw_hooks_event_view(
            stdscr,
            state["hooks_account"],
            state["cfg"]._hooks_global,
            state["cfg"]._hooks_overrides,
            state["hooks_drift"].get(state["hooks_account"], "missing"),
            state["hooks_event_selected"],
        )
        draw_hooks_delete_confirm(stdscr, state.get("hooks_delete_label", ""))
        draw_footer(stdscr, "hooks_delete_confirm")
    elif mode == "hooks_sync_confirm":
        draw_hooks_sync_confirm(stdscr, state["hooks_sync_scope"])
    elif mode == "hooks_add":
        # Draw the event view underneath, then overlay the form
        draw_hooks_event_view(
            stdscr,
            state["hooks_account"],
            state["cfg"]._hooks_global,
            state["cfg"]._hooks_overrides,
            state["hooks_drift"].get(state["hooks_account"], "missing"),
            state["hooks_event_selected"],
        )
        from cc_dashboard.loop_modes_hooks import _get_hooks_add_form_state

        draw_hooks_add_form(
            stdscr,
            state["hooks_account"],
            state["hooks_target_event"],
            _get_hooks_add_form_state(state),
            editing=state.get("hooks_add_editing", False),
        )
        draw_footer(stdscr, "hooks_add")
    elif mode == "hooks_matcher_pick":
        draw_hooks_event_view(
            stdscr,
            state["hooks_account"],
            state["cfg"]._hooks_global,
            state["cfg"]._hooks_overrides,
            state["hooks_drift"].get(state["hooks_account"], "missing"),
            state["hooks_event_selected"],
        )
        from cc_dashboard.loop_modes_hooks import _get_hooks_add_form_state

        draw_hooks_add_form(
            stdscr,
            state["hooks_account"],
            state["hooks_target_event"],
            _get_hooks_add_form_state(state),
            editing=state.get("hooks_add_editing", False),
        )
        draw_hooks_matcher_picker(
            stdscr,
            state.get("hooks_matcher_options", []),
            state.get("hooks_matcher_selected", 0),
            state.get("hooks_matcher_scroll", 0),
        )
        draw_footer(stdscr, "hooks_matcher_pick")
    elif mode == "hooks_info":
        draw_hooks_event_view(
            stdscr,
            state["hooks_account"],
            state["cfg"]._hooks_global,
            state["cfg"]._hooks_overrides,
            state["hooks_drift"].get(state["hooks_account"], "missing"),
            state["hooks_event_selected"],
        )
        draw_hooks_info(
            stdscr,
            state.get("hooks_info_handler", {}),
            state.get("hooks_info_matcher"),
            state.get("hooks_info_event", ""),
        )
        draw_footer(stdscr, "hooks_info")
    elif mode == "hooks_example":
        draw_hooks_event_view(
            stdscr,
            state["hooks_account"],
            state["cfg"]._hooks_global,
            state["cfg"]._hooks_overrides,
            state["hooks_drift"].get(state["hooks_account"], "missing"),
            state["hooks_event_selected"],
        )
        draw_hooks_example(stdscr, state.get("hooks_example_event", ""))
        draw_footer(stdscr, "hooks_example")
    elif mode == "actlog":
        draw_actlog_view(
            stdscr,
            state["actlog_session_title"],
            state["actlog_lines"],
            state["actlog_scroll"],
            state["actlog_summary"],
            search_query=state["actlog_search_query"],
            search_active=state["actlog_search_active"],
            search_cursor=state["actlog_search_cursor"],
            match_line_indices=state["actlog_match_lines"],
            match_index=state["actlog_match_index"],
        )
    elif mode == "actlog_browse":
        draw_actlog_browse(
            stdscr,
            state["actlog_browse_entries"],
            state["actlog_browse_selected"],
            state["actlog_browse_scroll"],
            search_query=state["actlog_browse_search_query"],
            search_active=state["actlog_browse_search_active"],
            search_cursor=state["actlog_browse_search_cursor"],
            match_line_indices=state["actlog_browse_match_lines"],
            match_index=state["actlog_browse_match_index"],
        )
        if state["msg"] and state["msg_frames"] > 0:
            draw_status_message(stdscr, state["msg"])
