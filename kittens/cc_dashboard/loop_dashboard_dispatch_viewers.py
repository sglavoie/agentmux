"""Dispatch handlers for viewer and config-oriented dashboard modes."""

from typing import Any

from cc_dashboard.loop_modes_config import handle_config_modes
from cc_dashboard.loop_modes_hooks import handle_hooks_modes
from cc_dashboard.loop_modes_prefs import handle_prefs_array_mode, handle_prefs_mode
from cc_dashboard.loop_viewers import handle_actlog_mode, handle_peek_mode


def _merge_state(state: dict[str, Any], updates: dict[str, Any]) -> None:
    for key, value in updates.items():
        state[key] = value


def dispatch_viewer_modes(
    stdscr, key: int, state: dict[str, Any]
) -> tuple[bool, bool] | None:
    """Handle viewer, preferences, hooks, and config modes."""
    mode = state["mode"]

    if mode in ("prefs", "prefs_array"):
        prefs_state: dict[str, Any] = {
            "mode": mode,
            "prefs_values": state["prefs_values"],
            "prefs_selected": state["prefs_selected"],
            "prefs_editing": state["prefs_editing"],
            "prefs_cursor": state["prefs_cursor"],
            "prefs_edit_value": state["prefs_edit_value"],
            "prefs_array_items": state.get("prefs_array_items", []),
            "prefs_array_selected": state.get("prefs_array_selected", 0),
            "prefs_array_editing": state.get("prefs_array_editing", False),
            "prefs_array_cursor": state.get("prefs_array_cursor", 0),
            "prefs_array_edit_value": state.get("prefs_array_edit_value", ""),
            "prefs_array_field_key": state.get("prefs_array_field_key", ""),
            "prefs_ext_edit_path": state.get("prefs_ext_edit_path", ""),
            "prefs_ext_edit_field": state.get("prefs_ext_edit_field", ""),
            "home_socket": state["home_socket"],
            "cfg": state["cfg"],
            "reload_all_config": state["reload_all_config"],
            "msg": state["msg"],
            "msg_frames": state["msg_frames"],
        }
        if mode == "prefs":
            handle_prefs_mode(key, prefs_state)
        else:
            handle_prefs_array_mode(key, prefs_state)
        _merge_state(state, prefs_state)
        return False, False

    if mode in (
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
        _merge_state(state, hooks_state)
        return False, False

    if mode in ("actlog", "actlog_browse", "actlog_gsearch", "actlog_detail"):
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
            "actlog_gsearch_parent_mode": state.get(
                "actlog_gsearch_parent_mode", "list"
            ),
            "actlog_entries": state.get("actlog_entries", []),
            "actlog_selected": state.get("actlog_selected", 0),
            "actlog_detail_entry": state.get("actlog_detail_entry", {}),
            "msg": state["msg"],
            "msg_frames": state["msg_frames"],
        }
        should_continue = handle_actlog_mode(
            stdscr, key, actlog_state, state["sessions"]
        )
        _merge_state(state, actlog_state)
        return False, should_continue

    if mode in ("config_pick", "config_action", "config_show"):
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
        _merge_state(state, config_state)
        return False, False

    if mode == "peek":
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
        _merge_state(state, peek_state)
        return False, should_continue

    return None
