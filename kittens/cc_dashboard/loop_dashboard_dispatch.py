"""Mode dispatch for the dashboard main event loop."""

from typing import Any

from cc_dashboard.filters import SORT_LABELS, SORT_MODES
from cc_dashboard.loop_helpers import (
    _begin_restart_options as _begin_restart_options_helper,
)
from cc_dashboard.loop_mode_list import handle_list_mode
from cc_dashboard.loop_modes_config import handle_config_modes
from cc_dashboard.loop_modes_dashboard import (
    handle_actions_mode,
    handle_color_picker_mode,
    handle_confirm_modes,
    handle_move_picker_mode,
    handle_restart_options_mode,
    handle_sort_pick_mode,
)
from cc_dashboard.loop_modes_delete import handle_delete_mode
from cc_dashboard.loop_modes_forms import (
    handle_clone_mode,
    handle_new_mode,
    handle_override_mode,
    handle_pick_mode,
    handle_rename_mode,
    handle_worktree_mode,
)
from cc_dashboard.loop_modes_hooks import handle_hooks_modes
from cc_dashboard.loop_viewers import handle_actlog_mode, handle_peek_mode


def dispatch_mode(
    stdscr,
    key: int,
    state: dict[str, Any],
    build_session_actions,
) -> tuple[bool, bool]:
    """Dispatch one key event based on the active mode.

    Returns `(should_exit, should_continue)` for the caller loop.
    """
    mode = state["mode"]
    should_exit = False
    should_continue = False

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
            "resolve_session_id": state["resolve_session_id"],
            "load_activity_log": state["load_activity_log"],
            "format_log_entry": state["format_log_entry"],
            "get_window_text": state["get_window_text"],
            "rstrip_blank_lines": state["rstrip_blank_lines"],
        }
        should_exit = handle_list_mode(stdscr, key, list_state)
        for k in list_state:
            state[k] = list_state[k]

    elif mode == "actions":
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
            "home_socket": state["home_socket"],
            "msg": state["msg"],
            "msg_frames": state["msg_frames"],
        }
        handle_actions_mode(key, actions_state, build_session_actions)
        for k in actions_state:
            state[k] = actions_state[k]

    elif mode == "restart_options":
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
        for k in restart_state:
            state[k] = restart_state[k]
        should_exit = restart_state["should_exit"]

    elif mode == "color_picker":
        color_state: dict[str, Any] = {
            "mode": mode,
            "filtered": state["filtered"],
            "selected": state["selected"],
            "color_selected": state["color_selected"],
            "palette_names": state["palette_names"],
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
        for k in color_state:
            state[k] = color_state[k]

    elif mode == "sort_pick":
        sort_state: dict[str, Any] = {
            "mode": mode,
            "sort_selected": state["sort_selected"],
            "sort_mode": state["sort_mode"],
            "sort_modes": SORT_MODES,
            "sort_labels": SORT_LABELS,
            "refilter": state["refilter"],
            "msg": state["msg"],
            "msg_frames": state["msg_frames"],
        }
        handle_sort_pick_mode(key, sort_state)
        for k in sort_state:
            state[k] = sort_state[k]

    elif mode in (
        "hooks",
        "hooks_event",
        "hooks_example",
        "hooks_sync_confirm",
        "hooks_add",
        "hooks_info",
        "hooks_delete_confirm",
        "hooks_matcher_pick",
    ):
        hooks_state: dict[str, Any] = {
            "mode": mode,
            "hooks_drift": state["hooks_drift"],
            "hooks_selected": state["hooks_selected"],
            "hooks_event_selected": state["hooks_event_selected"],
            "hooks_account": state["hooks_account"],
            "hooks_target_event": state["hooks_target_event"],
            "hooks_sync_scope": state["hooks_sync_scope"],
            "hooks_add_type": state.get("hooks_add_type", "command"),
            "hooks_add_matcher": state.get("hooks_add_matcher", ""),
            "hooks_add_value": state.get("hooks_add_value", ""),
            "hooks_add_async": state.get("hooks_add_async", True),
            "hooks_add_timeout": state.get("hooks_add_timeout", ""),
            "hooks_add_field": state.get("hooks_add_field", 2),
            "hooks_add_cursor": state.get("hooks_add_cursor", 0),
            "hooks_add_editing": state.get("hooks_add_editing", False),
            "hooks_add_edit_idx": state.get("hooks_add_edit_idx"),
            "hooks_delete_label": state.get("hooks_delete_label", ""),
            "hooks_delete_event": state.get("hooks_delete_event", ""),
            "hooks_delete_idx": state.get("hooks_delete_idx", 0),
            "hooks_matcher_options": state.get("hooks_matcher_options", []),
            "hooks_matcher_selected": state.get("hooks_matcher_selected", 0),
            "hooks_matcher_scroll": state.get("hooks_matcher_scroll", 0),
            "hooks_info_handler": state.get("hooks_info_handler"),
            "hooks_info_matcher": state.get("hooks_info_matcher"),
            "hooks_info_event": state.get("hooks_info_event"),
            "hooks_example_event": state.get("hooks_example_event", ""),
            "msg": state["msg"],
            "msg_frames": state["msg_frames"],
        }
        handle_hooks_modes(key, hooks_state)
        for k in hooks_state:
            state[k] = hooks_state[k]

    elif mode in ("actlog", "actlog_browse", "actlog_gsearch"):
        actlog_state: dict[str, Any] = {
            "mode": mode,
            "actlog_lines": state["actlog_lines"],
            "actlog_scroll": state["actlog_scroll"],
            "actlog_session_title": state["actlog_session_title"],
            "actlog_session_id": state["actlog_session_id"],
            "actlog_summary": state["actlog_summary"],
            "actlog_search_query": state["actlog_search_query"],
            "actlog_search_active": state["actlog_search_active"],
            "actlog_search_cursor": state["actlog_search_cursor"],
            "actlog_match_lines": state["actlog_match_lines"],
            "actlog_match_index": state["actlog_match_index"],
            "actlog_browse_entries": state["actlog_browse_entries"],
            "actlog_browse_selected": state["actlog_browse_selected"],
            "actlog_browse_scroll": state["actlog_browse_scroll"],
            "actlog_browse_search_active": state["actlog_browse_search_active"],
            "actlog_browse_search_query": state["actlog_browse_search_query"],
            "actlog_browse_search_cursor": state["actlog_browse_search_cursor"],
            "actlog_browse_match_lines": state["actlog_browse_match_lines"],
            "actlog_browse_match_index": state["actlog_browse_match_index"],
            "actlog_gsearch_query": state["actlog_gsearch_query"],
            "actlog_gsearch_cursor": state["actlog_gsearch_cursor"],
            "actlog_gsearch_results": state["actlog_gsearch_results"],
            "actlog_gsearch_selected": state["actlog_gsearch_selected"],
            "actlog_gsearch_scroll": state["actlog_gsearch_scroll"],
            "actlog_gsearch_last_query": state["actlog_gsearch_last_query"],
            "actlog_gsearch_debounce_ticks": state["actlog_gsearch_debounce_ticks"],
            "actlog_gsearch_loading": state["actlog_gsearch_loading"],
            "actlog_gsearch_total_scanned": state["actlog_gsearch_total_scanned"],
            "actlog_gsearch_log_index": state["actlog_gsearch_log_index"],
            "actlog_gsearch_search_active": state["actlog_gsearch_search_active"],
            "msg": state["msg"],
            "msg_frames": state["msg_frames"],
        }
        should_continue = handle_actlog_mode(
            stdscr, key, actlog_state, state["sessions"]
        )
        for k in actlog_state:
            state[k] = actlog_state[k]

    elif mode in ("config_pick", "config_action", "config_show"):
        config_state: dict[str, Any] = {
            "mode": mode,
            "config_selected": state["config_selected"],
            "config_action_selected": state["config_action_selected"],
            "config_file_label": state["config_file_label"],
            "config_file_path": state["config_file_path"],
            "config_show_lines": state["config_show_lines"],
            "config_show_scroll": state["config_show_scroll"],
            "msg": state["msg"],
            "msg_frames": state["msg_frames"],
            "screen_height": stdscr.getmaxyx()[0],
        }
        handle_config_modes(
            key,
            config_state,
            state["home_socket"],
            state["reload_all_config"],
        )
        for k in config_state:
            state[k] = config_state[k]

    elif mode == "peek":
        peek_state: dict[str, Any] = {
            "mode": mode,
            "peek_lines": state["peek_lines"],
            "peek_scroll": state["peek_scroll"],
            "peek_extent": state["peek_extent"],
            "peek_error": state["peek_error"],
            "peek_search_query": state["peek_search_query"],
            "peek_search_active": state["peek_search_active"],
            "peek_search_cursor": state["peek_search_cursor"],
            "peek_match_lines": state["peek_match_lines"],
            "peek_match_index": state["peek_match_index"],
        }
        should_continue = handle_peek_mode(
            stdscr,
            key,
            peek_state,
            state["filtered"],
            state["selected"],
            state["home_socket"],
        )
        for k in peek_state:
            state[k] = peek_state[k]

    elif mode in (
        "confirm",
        "confirm_restart",
        "confirm_batch_close",
        "confirm_batch_restart",
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
            "spawn_detached_close": state["spawn_detached_close"],
            "query_all_sessions": state["query_all_sessions"],
            "refilter": state["refilter"],
            "msg": state["msg"],
            "msg_frames": state["msg_frames"],
            "should_exit": False,
        }
        handle_confirm_modes(key, confirm_state)
        for k in confirm_state:
            state[k] = confirm_state[k]
        should_exit = confirm_state["should_exit"]

    elif mode == "override":
        override_state: dict[str, Any] = {
            "mode": mode,
            "override_source": state["override_source"],
            "override_selected": state["override_selected"],
            "pick_filtered": state["pick_filtered"],
            "pick_selected": state["pick_selected"],
            "pick_target_instance": state["pick_target_instance"],
            "filtered": state["filtered"],
            "selected": state["selected"],
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
            "query_all_sessions": state["query_all_sessions"],
            "refilter": state["refilter"],
            "msg": state["msg"],
            "msg_frames": state["msg_frames"],
            "should_exit": False,
        }
        handle_override_mode(key, override_state)
        for k in override_state:
            state[k] = override_state[k]
        should_exit = override_state["should_exit"]

    elif mode == "move_picker":
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
        for k in move_state:
            state[k] = move_state[k]
        should_exit = move_state["should_exit"]

    elif mode == "rename":
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
            "override_marker": state["override_marker"],
            "append_override_marker": state["append_override_marker"],
            "msg": state["msg"],
            "msg_frames": state["msg_frames"],
        }
        handle_rename_mode(key, rename_state)
        for k in rename_state:
            state[k] = rename_state[k]

    elif mode == "worktree":
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
        for k in worktree_state:
            state[k] = worktree_state[k]

    elif mode == "clone":
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
        for k in clone_state:
            state[k] = clone_state[k]

    elif mode == "new":
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
        for k in new_state:
            state[k] = new_state[k]
        should_exit = new_state["should_exit"]

    elif mode == "pick":
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
            "pick_target_instance": state["pick_target_instance"],
            "override_source": state["override_source"],
            "override_selected": state["override_selected"],
            "cfg": state["cfg"],
            "multi_instance": state["multi_instance"],
            "instance_labels": state["instance_labels"],
            "instance_socket_map": state["instance_socket_map"],
            "home_socket": state["home_socket"],
            "compute_insert_location": state["compute_insert_location"],
            "do_launch": state["do_launch"],
            "should_exit": False,
        }
        handle_pick_mode(key, pick_state)
        for k in pick_state:
            state[k] = pick_state[k]
        should_exit = pick_state["should_exit"]

    elif mode == "delete":
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
            "msg": state["msg"],
            "msg_frames": state["msg_frames"],
        }
        handle_delete_mode(key, delete_state)
        for k in delete_state:
            state[k] = delete_state[k]

    return should_exit, should_continue
