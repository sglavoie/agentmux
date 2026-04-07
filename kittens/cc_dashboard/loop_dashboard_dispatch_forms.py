"""Dispatch handlers for forms, pickers, and mutation workflows."""

from typing import Any

from cc_dashboard.loop_modes_forms import (
    _resolve_live_or_config_options,
    handle_batch_group_mode,
    handle_clone_mode,
    handle_confirm_mkdir_mode,
    handle_new_mode,
    handle_override_mode,
    handle_pick_mode,
    handle_rename_mode,
    handle_worktree_mode,
)


def _merge_state(state: dict[str, Any], updates: dict[str, Any]) -> None:
    for key, value in updates.items():
        state[key] = value


def dispatch_form_modes(
    stdscr, key: int, state: dict[str, Any]
) -> tuple[bool, bool] | None:
    """Handle dashboard modes that edit, create, or select sessions."""
    mode = state["mode"]

    if mode == "override":
        override_state: dict[str, Any] = {
            "mode": mode,
            "override_source": state["override_source"],
            "override_selected": state["override_selected"],
            "pick_filtered": state["pick_filtered"],
            "pick_selected": state["pick_selected"],
            "pick_target_instance": state["pick_target_instance"],
            "filtered": state["filtered"],
            "selected": state["selected"],
            "batch_targets": state["batch_targets"],
            "marked": state["marked"],
            "cfg": state["cfg"],
            "multi_instance": state["multi_instance"],
            "instance_socket_map": state["instance_socket_map"],
            "home_socket": state["home_socket"],
            "host_tab_id": state["host_tab_id"],
            "show_all": state["show_all"],
            "sessions": state["sessions"],
            "compute_insert_location": state["compute_insert_location"],
            "do_launch": state["do_launch"],
            "do_close": state["do_close"],
            "do_restart": state["do_restart"],
            "append_override_marker": state["append_override_marker"],
            "append_override_for_dir": state["append_override_for_dir"],
            "strip_override_marker": state["strip_override_marker"],
            "resolve_live_or_config_options": _resolve_live_or_config_options,
            "query_all_sessions": state["query_all_sessions"],
            "refilter": state["refilter"],
            "msg": state["msg"],
            "msg_frames": state["msg_frames"],
            "should_exit": False,
        }
        handle_override_mode(key, override_state)
        _merge_state(state, override_state)
        return override_state["should_exit"], False

    if mode == "rename":
        rename_state: dict[str, Any] = {
            "mode": mode,
            "rename_value": state["rename_value"],
            "rename_cursor": state["rename_cursor"],
            "filtered": state["filtered"],
            "selected": state["selected"],
            "home_socket": state["home_socket"],
            "show_all": state["show_all"],
            "sessions": state["sessions"],
            "query_all_sessions": state["query_all_sessions"],
            "refilter": state["refilter"],
            "do_rename": state["do_rename"],
            "strip_override_marker": state["strip_override_marker"],
            "msg": state["msg"],
            "msg_frames": state["msg_frames"],
        }
        handle_rename_mode(key, rename_state)
        _merge_state(state, rename_state)
        return False, False

    if mode == "worktree":
        worktree_state: dict[str, Any] = {
            "mode": mode,
            "worktree_branch": state["worktree_branch"],
            "worktree_cursor": state["worktree_cursor"],
            "filtered": state["filtered"],
            "selected": state["selected"],
            "cfg": state["cfg"],
            "home_socket": state["home_socket"],
            "show_all": state["show_all"],
            "sessions": state["sessions"],
            "do_create_worktree": state["do_create_worktree"],
            "strip_override_marker": state["strip_override_marker"],
            "compute_insert_location": state["compute_insert_location"],
            "do_launch": state["do_launch"],
            "query_all_sessions": state["query_all_sessions"],
            "refilter": state["refilter"],
            "msg": state["msg"],
            "msg_frames": state["msg_frames"],
        }
        handle_worktree_mode(key, worktree_state)
        _merge_state(state, worktree_state)
        return False, False

    if mode == "clone":
        clone_state: dict[str, Any] = {
            "mode": mode,
            "clone_title": state["clone_title"],
            "clone_cursor": state["clone_cursor"],
            "filtered": state["filtered"],
            "selected": state["selected"],
            "cfg": state["cfg"],
            "home_socket": state["home_socket"],
            "show_all": state["show_all"],
            "sessions": state["sessions"],
            "strip_override_marker": state["strip_override_marker"],
            "compute_insert_location": state["compute_insert_location"],
            "do_launch": state["do_launch"],
            "query_all_sessions": state["query_all_sessions"],
            "refilter": state["refilter"],
            "msg": state["msg"],
            "msg_frames": state["msg_frames"],
        }
        handle_clone_mode(key, clone_state)
        _merge_state(state, clone_state)
        return False, False

    if mode == "batch_group":
        batch_group_state: dict[str, Any] = {
            "mode": mode,
            "batch_group_value": state["batch_group_value"],
            "batch_group_cursor": state["batch_group_cursor"],
            "actions_targets": state["actions_targets"],
            "marked": state["marked"],
            "home_socket": state["home_socket"],
            "show_all": state["show_all"],
            "sessions": state["sessions"],
            "kitty_cmd": state["kitty_cmd"],
            "query_all_sessions": state["query_all_sessions"],
            "refilter": state["refilter"],
            "msg": state["msg"],
            "msg_frames": state["msg_frames"],
        }
        handle_batch_group_mode(key, batch_group_state)
        _merge_state(state, batch_group_state)
        return False, False

    if mode == "new":
        new_state: dict[str, Any] = {
            "mode": mode,
            "form_mode": state["form_mode"],
            "edit_original_title": state["edit_original_title"],
            "new_fields": state["new_fields"],
            "new_active": state["new_active"],
            "new_cursor": state["new_cursor"],
            "dir_browse_active": state["dir_browse_active"],
            "dir_browse_entries": state["dir_browse_entries"],
            "dir_browse_selected": state["dir_browse_selected"],
            "dir_browse_scroll": state["dir_browse_scroll"],
            "dir_browse_show_hidden": state["dir_browse_show_hidden"],
            "multi_instance": state["multi_instance"],
            "instance_labels": state["instance_labels"],
            "instance_socket_map": state["instance_socket_map"],
            "home_socket": state["home_socket"],
            "sessions": state["sessions"],
            "cfg": state["cfg"],
            "strip_override_marker": state["strip_override_marker"],
            "compute_insert_location": state["compute_insert_location"],
            "do_launch": state["do_launch"],
            "msg": state["msg"],
            "msg_frames": state["msg_frames"],
            "should_exit": False,
        }
        handle_new_mode(stdscr, key, new_state)
        _merge_state(state, new_state)
        return new_state["should_exit"], False

    if mode == "confirm_mkdir":
        mkdir_state: dict[str, Any] = {
            "mode": mode,
            "mkdir_path": state["mkdir_path"],
            "mkdir_pending": state["mkdir_pending"],
            "new_fields": state["new_fields"],
            "new_active": state["new_active"],
            "new_cursor": state["new_cursor"],
            "cfg": state["cfg"],
            "compute_insert_location": state["compute_insert_location"],
            "do_launch": state["do_launch"],
            "msg": state["msg"],
            "msg_frames": state["msg_frames"],
            "should_exit": False,
        }
        handle_confirm_mkdir_mode(key, mkdir_state)
        _merge_state(state, mkdir_state)
        return mkdir_state["should_exit"], False

    if mode == "pick":
        pick_state: dict[str, Any] = {
            "mode": mode,
            "pick_selected": state["pick_selected"],
            "pick_filtered": state["pick_filtered"],
            "pick_running": state["pick_running"],
            "pick_running_count": state["pick_running_count"],
            "pick_query": state["pick_query"],
            "pick_cursor": state["pick_cursor"],
            "pick_search_active": state["pick_search_active"],
            "pick_marked": state.get("pick_marked", set()),
            "pick_group_filter": state.get("pick_group_filter"),
            "pick_group_filter_mode": state.get("pick_group_filter_mode", False),
            "pick_target_instance": state["pick_target_instance"],
            "override_source": state["override_source"],
            "override_selected": state["override_selected"],
            "sort_mode": state["sort_mode"],
            "cfg": state["cfg"],
            "multi_instance": state["multi_instance"],
            "instance_labels": state["instance_labels"],
            "instance_socket_map": state["instance_socket_map"],
            "home_socket": state["home_socket"],
            "compute_insert_location": state["compute_insert_location"],
            "do_launch": state["do_launch"],
            "msg": state["msg"],
            "msg_frames": state["msg_frames"],
            "should_exit": False,
        }
        handle_pick_mode(key, pick_state)
        _merge_state(state, pick_state)
        return pick_state["should_exit"], False

    return None
