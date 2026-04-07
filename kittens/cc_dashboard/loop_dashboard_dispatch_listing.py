"""Dispatch handlers for list and action-oriented dashboard modes."""

from typing import Any

from cc_dashboard.filters import SORT_LABELS, SORT_MODES
from cc_dashboard.loop_helpers import (
    _begin_restart_options as _begin_restart_options_helper,
)
from cc_dashboard.loop_mode_list import handle_list_mode
from cc_dashboard.loop_modes_dashboard import (
    handle_actions_mode,
    handle_color_picker_mode,
    handle_confirm_modes,
    handle_group_actions_mode,
    handle_move_picker_mode,
    handle_restart_options_mode,
    handle_sort_pick_mode,
)
from cc_dashboard.loop_modes_delete import handle_delete_mode


def _merge_state(state: dict[str, Any], updates: dict[str, Any]) -> None:
    for key, value in updates.items():
        state[key] = value


def dispatch_listing_modes(
    stdscr,
    key: int,
    state: dict[str, Any],
    build_session_actions,
) -> tuple[bool, bool] | None:
    """Handle dashboard list and action overlay modes.

    Returns None when the active mode belongs to another dispatcher.
    """
    mode = state["mode"]

    if mode == "list":
        list_state: dict[str, Any] = {
            "cfg": state["cfg"],
            "mode": mode,
            "show_all": state["show_all"],
            "show_detail": state["show_detail"],
            "sessions": state["sessions"],
            "filtered": state["filtered"],
            "selected": state["selected"],
            "marked": state["marked"],
            "search_active": state["search_active"],
            "search_query": state["search_query"],
            "search_cursor": state["search_cursor"],
            "account_filter": state["account_filter"],
            "account_filter_mode": state["account_filter_mode"],
            "form_mode": state["form_mode"],
            "edit_original_title": state["edit_original_title"],
            "new_fields": state["new_fields"],
            "new_active": state["new_active"],
            "new_cursor": state["new_cursor"],
            "dir_browse_active": state["dir_browse_active"],
            "pick_selected": state["pick_selected"],
            "pick_filtered": state["pick_filtered"],
            "pick_running": state["pick_running"],
            "pick_running_count": state["pick_running_count"],
            "pick_query": state["pick_query"],
            "pick_cursor": state["pick_cursor"],
            "pick_search_active": state["pick_search_active"],
            "pick_target_instance": state["pick_target_instance"],
            "delete_step": state["delete_step"],
            "delete_running": state["delete_running"],
            "delete_entries": state["delete_entries"],
            "delete_filtered": state["delete_filtered"],
            "delete_running_count": state["delete_running_count"],
            "delete_selected": state["delete_selected"],
            "delete_query": state["delete_query"],
            "delete_cursor": state["delete_cursor"],
            "delete_search_active": state["delete_search_active"],
            "actions_scope": state["actions_scope"],
            "actions_targets": state["actions_targets"],
            "actions_selected": state["actions_selected"],
            "group_filter": state["group_filter"],
            "group_filter_mode": state["group_filter_mode"],
            "collapsed_groups": state["collapsed_groups"],
            "ga_groups": state["ga_groups"],
            "ga_selected": state["ga_selected"],
            "ga_step": state["ga_step"],
            "ga_action_selected": state["ga_action_selected"],
            "sort_mode": state["sort_mode"],
            "sort_modes": SORT_MODES,
            "sort_selected": state["sort_selected"],
            "peek_session_title": state["peek_session_title"],
            "peek_extent": state["peek_extent"],
            "peek_scroll": state["peek_scroll"],
            "peek_search_query": state["peek_search_query"],
            "peek_search_active": state["peek_search_active"],
            "peek_search_cursor": state["peek_search_cursor"],
            "peek_match_lines": state["peek_match_lines"],
            "peek_match_index": state["peek_match_index"],
            "peek_error": state["peek_error"],
            "peek_lines": state["peek_lines"],
            "host_window_id": state["host_window_id"],
            "hooks_drift": state["hooks_drift"],
            "hooks_selected": state["hooks_selected"],
            "config_selected": state["config_selected"],
            "actlog_session_title": state["actlog_session_title"],
            "actlog_session_id": state["actlog_session_id"],
            "actlog_summary": state["actlog_summary"],
            "actlog_lines": state["actlog_lines"],
            "actlog_scroll": state["actlog_scroll"],
            "actlog_search_query": state["actlog_search_query"],
            "actlog_search_active": state["actlog_search_active"],
            "actlog_search_cursor": state["actlog_search_cursor"],
            "actlog_match_lines": state["actlog_match_lines"],
            "actlog_match_index": state["actlog_match_index"],
            "home_socket": state["home_socket"],
            "home_instance_label": state["home_instance_label"],
            "msg": state["msg"],
            "msg_frames": state["msg_frames"],
            "refilter": state["refilter"],
            "reload_all_config": state["reload_all_config"],
            "query_all_sessions": state["query_all_sessions"],
            "make_new_fields": state["make_new_fields"],
            "strip_override_marker": state["strip_override_marker"],
            "sort_pick_entries": state["sort_pick_entries"],
            "build_delete_entries": state["build_delete_entries"],
            "do_cross_instance_focus": state["do_cross_instance_focus"],
            "do_toggle_mute": state["do_toggle_mute"],
            "resolve_session_id": state["resolve_session_id"],
            "load_activity_log": state["load_activity_log"],
            "format_log_entry": state["format_log_entry"],
            "get_window_text": state["get_window_text"],
            "rstrip_blank_lines": state["rstrip_blank_lines"],
            "_save_ui_state": state.get("_save_ui_state"),
        }
        should_exit = handle_list_mode(stdscr, key, list_state)
        _merge_state(state, list_state)
        return should_exit, False

    if mode == "actions":
        actions_state: dict[str, Any] = {
            "mode": mode,
            "filtered": state["filtered"],
            "selected": state["selected"],
            "actions_scope": state["actions_scope"],
            "actions_targets": state["actions_targets"],
            "actions_selected": state["actions_selected"],
            "batch_targets": state["batch_targets"],
            "multi_instance": state["multi_instance"],
            "move_selected": state["move_selected"],
            "clone_title": state["clone_title"],
            "clone_cursor": state["clone_cursor"],
            "worktree_branch": state["worktree_branch"],
            "worktree_cursor": state["worktree_cursor"],
            "override_source": state["override_source"],
            "override_selected": state["override_selected"],
            "rename_value": state["rename_value"],
            "rename_cursor": state["rename_cursor"],
            "restart_options_fields": state["restart_options_fields"],
            "restart_options_active": state["restart_options_active"],
            "restart_options_cursor": state["restart_options_cursor"],
            "restart_target_socket": state["restart_target_socket"],
            "restart_target_tab_id": state["restart_target_tab_id"],
            "sessions": state["sessions"],
            "cfg": state["cfg"],
            "strip_override_marker": state["strip_override_marker"],
            "generate_clone_title": state["generate_clone_title"],
            "start_restart_options": lambda sess: _begin_restart_options_helper(
                sess,
                state["multi_instance"],
                state["home_instance_label"],
                state["restart_default_config_label"],
                state["home_socket"],
            ),
            "palette_names": state["palette_names"],
            "do_edit_notes": state["do_edit_notes"],
            "do_toggle_mute": state["do_toggle_mute"],
            "home_socket": state["home_socket"],
            "marked": state["marked"],
            "query_all_sessions": state["query_all_sessions"],
            "show_all": state["show_all"],
            "refilter": state["refilter"],
            "msg": state["msg"],
            "msg_frames": state["msg_frames"],
        }
        handle_actions_mode(key, actions_state, build_session_actions)
        _merge_state(state, actions_state)
        return False, False

    if mode == "restart_options":
        restart_state: dict[str, Any] = {
            "mode": mode,
            "sessions": state["sessions"],
            "show_all": state["show_all"],
            "home_socket": state["home_socket"],
            "host_tab_id": state["host_tab_id"],
            "multi_instance": state["multi_instance"],
            "instance_labels": state["instance_labels"],
            "instance_socket_map": state["instance_socket_map"],
            "restart_options_fields": state["restart_options_fields"],
            "restart_options_active": state["restart_options_active"],
            "restart_options_cursor": state["restart_options_cursor"],
            "restart_options_commands": state.get("restart_options_commands", []),
            "restart_target_socket": state["restart_target_socket"],
            "restart_target_tab_id": state["restart_target_tab_id"],
            "restart_default_config_label": state["restart_default_config_label"],
            "cfg": state["cfg"],
            "cycle_pw": state["cycle_pw"],
            "cycle_instance": state["cycle_instance"],
            "do_restart": state["do_restart"],
            "do_move": state["do_move"],
            "query_all_sessions": state["query_all_sessions"],
            "refilter": state["refilter"],
            "msg": state["msg"],
            "msg_frames": state["msg_frames"],
            "should_exit": False,
        }
        handle_restart_options_mode(key, restart_state)
        _merge_state(state, restart_state)
        return restart_state["should_exit"], False

    if mode == "color_picker":
        color_state: dict[str, Any] = {
            "mode": mode,
            "filtered": state["filtered"],
            "selected": state["selected"],
            "color_selected": state["color_selected"],
            "palette_names": state["palette_names"],
            "actions_scope": state["actions_scope"],
            "actions_targets": state["actions_targets"],
            "marked": state["marked"],
            "cfg": state["cfg"],
            "home_socket": state["home_socket"],
            "show_all": state["show_all"],
            "sessions": state["sessions"],
            "do_set_color": state["do_set_color"],
            "query_all_sessions": state["query_all_sessions"],
            "refilter": state["refilter"],
            "msg": state["msg"],
            "msg_frames": state["msg_frames"],
        }
        handle_color_picker_mode(key, color_state)
        _merge_state(state, color_state)
        return False, False

    if mode == "sort_pick":
        sort_state: dict[str, Any] = {
            "mode": mode,
            "sort_selected": state["sort_selected"],
            "sort_mode": state["sort_mode"],
            "sort_modes": SORT_MODES,
            "sort_labels": SORT_LABELS,
            "refilter": state["refilter"],
            "_save_ui_state": state.get("_save_ui_state"),
            "msg": state["msg"],
            "msg_frames": state["msg_frames"],
        }
        handle_sort_pick_mode(key, sort_state)
        _merge_state(state, sort_state)
        return False, False

    if mode in (
        "confirm",
        "confirm_restart",
        "confirm_batch_close",
        "confirm_batch_restart",
        "confirm_batch_clone",
        "confirm_save",
    ):
        confirm_state: dict[str, Any] = {
            "mode": mode,
            "filtered": state["filtered"],
            "selected": state["selected"],
            "batch_targets": state["batch_targets"],
            "sessions": state["sessions"],
            "marked": state["marked"],
            "show_all": state["show_all"],
            "home_socket": state["home_socket"],
            "host_tab_id": state["host_tab_id"],
            "cfg": state["cfg"],
            "strip_override_marker": state["strip_override_marker"],
            "do_close": state["do_close"],
            "do_restart": state["do_restart"],
            "do_launch": state["do_launch"],
            "spawn_detached_close": state["spawn_detached_close"],
            "generate_clone_title": state["generate_clone_title"],
            "compute_insert_location": state["compute_insert_location"],
            "query_all_sessions": state["query_all_sessions"],
            "refilter": state["refilter"],
            "msg": state["msg"],
            "msg_frames": state["msg_frames"],
            "should_exit": False,
        }
        handle_confirm_modes(key, confirm_state)
        _merge_state(state, confirm_state)
        return confirm_state["should_exit"], False

    if mode == "move_picker":
        move_state: dict[str, Any] = {
            "mode": mode,
            "filtered": state["filtered"],
            "selected": state["selected"],
            "move_selected": state["move_selected"],
            "actions_scope": state["actions_scope"],
            "actions_targets": state["actions_targets"],
            "instance_labels": state["instance_labels"],
            "instance_socket_map": state["instance_socket_map"],
            "sessions": state["sessions"],
            "marked": state["marked"],
            "show_all": state["show_all"],
            "home_socket": state["home_socket"],
            "host_tab_id": state["host_tab_id"],
            "cfg": state["cfg"],
            "strip_override_marker": state["strip_override_marker"],
            "do_move": state["do_move"],
            "query_all_sessions": state["query_all_sessions"],
            "refilter": state["refilter"],
            "msg": state["msg"],
            "msg_frames": state["msg_frames"],
            "should_exit": False,
        }
        handle_move_picker_mode(key, move_state)
        _merge_state(state, move_state)
        return move_state["should_exit"], False

    if mode == "group_actions":
        ga_state: dict[str, Any] = {
            "mode": mode,
            "ga_groups": state["ga_groups"],
            "ga_selected": state["ga_selected"],
            "ga_step": state["ga_step"],
            "ga_action_selected": state["ga_action_selected"],
            "sessions": state["sessions"],
            "cfg": state["cfg"],
            "home_socket": state["home_socket"],
            "show_all": state["show_all"],
            "batch_targets": state["batch_targets"],
            "compute_insert_location": state["compute_insert_location"],
            "do_launch": state["do_launch"],
            "reload_all_config": state["reload_all_config"],
            "query_all_sessions": state["query_all_sessions"],
            "refilter": state["refilter"],
            "msg": state["msg"],
            "msg_frames": state["msg_frames"],
        }
        handle_group_actions_mode(key, ga_state)
        _merge_state(state, ga_state)
        return False, False

    if mode == "delete":
        delete_state: dict[str, Any] = {
            "mode": mode,
            "delete_entries": state["delete_entries"],
            "delete_selected": state["delete_selected"],
            "delete_step": state["delete_step"],
            "delete_running": state["delete_running"],
            "delete_search_active": state["delete_search_active"],
            "delete_query": state["delete_query"],
            "delete_cursor": state["delete_cursor"],
            "delete_filtered": state["delete_filtered"],
            "delete_running_count": state["delete_running_count"],
            "delete_target_entry": state["delete_target_entry"],
            "delete_warnings": state["delete_warnings"],
            "sort_mode": state["sort_mode"],
            "msg": state["msg"],
            "msg_frames": state["msg_frames"],
        }
        handle_delete_mode(key, delete_state)
        _merge_state(state, delete_state)
        return False, False

    return None
