"""Bootstrap helpers for the dashboard event loop."""

import os
import sys
from typing import Any

from cc_dashboard import config as _cfg
from cc_dashboard.config_data import PALETTE_NAMES
from cc_dashboard.drawing import safe_addstr
from cc_dashboard.filters import SORT_MODES, _session_groups, _sort_pick_entries
from cc_dashboard.loop_helpers import (
    _build_delete_entries,
    _rstrip_blank_lines,
)
from cc_dashboard.loop_helpers import (
    _build_session_actions as _build_session_actions_helper,
)
from cc_dashboard.loop_helpers import (
    _make_new_fields as _make_new_fields_helper,
)
from cc_dashboard.loop_helpers import (
    _refilter as _refilter_helper,
)
from cc_dashboard.loop_helpers import (
    _reload_all_config as _reload_all_config_helper,
)
from cc_dashboard.remote import (
    _spawn_detached_close,
    compute_insert_location,
    do_close,
    do_cross_instance_focus,
    do_launch,
    do_move,
    do_rename,
    do_restart,
    generate_clone_title,
    open_terminal,
)
from cc_dashboard.remote.actions_tab_ui import (
    do_edit_notes,
    do_set_color,
    do_toggle_mute,
)
from cc_dashboard.remote.activity import load_activity_log, resolve_session_id
from cc_dashboard.remote.command import (
    get_all_kitty_sockets,
    get_host_tab_id,
    get_kitty_socket,
    get_window_text,
    kitty_cmd,
)
from cc_dashboard.remote.git_ops import do_create_worktree
from cc_dashboard.remote.sessions import query_all_sessions
from cc_dashboard.remote.shared import (
    OVERRIDE_EMOJIS,
    _append_override_for_dir,
    _append_override_marker,
    _has_override_emoji,
    _strip_override_marker,
)
from cc_dashboard.state_persist import load_ui_state, prune_stale_entries, save_ui_state
from cc_dashboard.views import _cycle_instance, _cycle_pw, _format_log_entry


def run_startup_validation(state: dict[str, Any]) -> None:
    """Run config validation and surface issues in the status bar."""
    try:
        lib_dir = os.path.join(
            os.path.dirname(os.path.realpath(__file__)), "..", "..", "lib"
        )
        if lib_dir not in sys.path:
            sys.path.insert(0, lib_dir)
        from config_validator import quick_validate  # type: ignore[import-not-found]

        config_dir = _cfg._config_dir()
        data_dir = os.path.join(
            os.path.dirname(os.path.realpath(__file__)), "..", "..", "data"
        )
        issues = quick_validate(config_dir, data_dir)
        errors = [msg for level, msg in issues if level == "error"]
        warnings = [msg for level, msg in issues if level == "warning"]
        if errors:
            state["msg"] = f"Config: {len(errors)} error(s) found (ccm config validate)"
            state["msg_frames"] = 50
        elif warnings:
            state["msg"] = f"Config: {len(warnings)} warning(s) (ccm config validate)"
            state["msg_frames"] = 30
    except Exception as e:
        state["msg"] = f"Config check failed: {e}"
        state["msg_frames"] = 30
        if os.environ.get("CCM_DEBUG") == "1":
            import traceback

            traceback.print_exc(file=sys.stderr)


def preflight_check() -> str | None:
    """Validate config directory and accounts.toml before entering curses."""
    config_dir = _cfg._config_dir()
    if not os.path.isdir(config_dir):
        return (
            f"Config directory not found: {config_dir}\n"
            "Run 'ccm config init' to create the default configuration."
        )
    accounts_path = os.path.join(config_dir, "accounts.toml")
    if os.path.exists(accounts_path) and not os.access(accounts_path, os.R_OK):
        return f"Cannot read accounts config: {accounts_path}"
    return None


def show_preflight_error(stdscr, message: str) -> None:
    """Render a blocking preflight error message until the user exits."""
    stdscr.erase()
    lines = message.split("\n")
    for index, line in enumerate(lines):
        safe_addstr(stdscr, index, 0, line)
    safe_addstr(stdscr, len(lines) + 1, 0, "Press q to exit.")
    stdscr.refresh()
    while True:
        if stdscr.getch() == ord("q"):
            return


def initialize_dashboard_state(stdscr) -> dict[str, Any]:
    """Discover runtime state and build the initial dashboard state dict."""
    stdscr.erase()
    safe_addstr(stdscr, 0, 0, "Discovering sessions...")
    stdscr.refresh()

    home_socket = os.environ.get("KITTY_LISTEN_ON", "") or get_kitty_socket()
    discovered_sockets = get_all_kitty_sockets()
    home_instance_label = _resolve_home_instance_label(discovered_sockets, home_socket)

    safe_addstr(
        stdscr,
        0,
        0,
        f"Loading sessions ({len(discovered_sockets)} instance(s))...",
    )
    stdscr.clrtoeol()
    stdscr.refresh()

    sessions = query_all_sessions(managed_only=True)
    state = _build_initial_state(
        home_socket,
        home_instance_label,
        discovered_sockets,
        sessions,
    )
    _attach_state_helpers(state)
    run_startup_validation(state)
    _apply_default_sort(state)
    _restore_persisted_ui_state(state)
    return state


def build_session_actions(
    state: dict[str, Any], sess: dict[str, Any], scope: str = "selected"
):
    """Build the action menu for a session based on current runtime state."""
    return _build_session_actions_helper(sess, scope, state["multi_instance"])


def _resolve_home_instance_label(
    discovered_sockets: list[tuple[str, str]], home_socket: str
) -> str:
    for sock, label in discovered_sockets:
        if sock == home_socket:
            return label
    if discovered_sockets:
        return discovered_sockets[0][1]
    return ""


def _build_initial_state(
    home_socket: str,
    home_instance_label: str,
    discovered_sockets,
    sessions,
) -> dict[str, Any]:
    instance_labels = [label for _, label in discovered_sockets]
    instance_socket_map = {label: sock for sock, label in discovered_sockets}
    selected = 0
    for index, sess in enumerate(sessions):
        if sess["status"] == "focused":
            selected = index
            break

    host_tab_id = get_host_tab_id(home_socket)
    host_wid_str = os.environ.get("KITTY_WINDOW_ID", "")
    host_window_id = int(host_wid_str) if host_wid_str.isdigit() else 0

    default_account = _cfg._account_names[0] if _cfg._account_names else "work"
    default_pw = (
        _cfg._default_project_window if _cfg._default_project_window else "none"
    )
    refresh_interval = _cfg._default_dashboard_refresh

    return {
        "cfg": _cfg,
        "home_socket": home_socket,
        "home_instance_label": home_instance_label,
        "instance_labels": instance_labels,
        "instance_socket_map": instance_socket_map,
        "multi_instance": len(discovered_sockets) > 1,
        "host_tab_id": host_tab_id,
        "host_window_id": host_window_id,
        "show_all": False,
        "show_detail": False,
        "mode": "list",
        "sessions": sessions,
        "selected": selected,
        "filtered": sessions,
        "marked": set(),
        "batch_targets": [],
        "batch_group_value": "",
        "batch_group_cursor": 0,
        "search_query": "",
        "search_active": False,
        "search_cursor": 0,
        "sort_mode": "default",
        "account_filter": None,
        "account_filter_mode": False,
        "group_filter": None,
        "group_filter_mode": False,
        "collapsed_groups": set(),
        "ga_groups": [],
        "ga_selected": 0,
        "ga_step": "pick_group",
        "ga_action_selected": 0,
        "msg": "",
        "msg_frames": 0,
        "refresh_interval": refresh_interval,
        "refresh_ticks": 0,
        "refresh_threshold": refresh_interval * 10,
        "pick_selected": 0,
        "pick_filtered": list(_cfg._sessions),
        "pick_running": set(),
        "pick_running_count": 0,
        "pick_query": "",
        "pick_cursor": 0,
        "pick_search_active": False,
        "pick_target_instance": home_instance_label,
        "rename_value": "",
        "rename_cursor": 0,
        "clone_title": "",
        "clone_cursor": 0,
        "worktree_branch": "",
        "worktree_cursor": 0,
        "override_selected": 0,
        "override_source": "list",
        "move_selected": 0,
        "sort_selected": 0,
        "actions_selected": 0,
        "actions_scope": "selected",
        "actions_targets": [],
        "restart_target_socket": "",
        "restart_target_tab_id": 0,
        "restart_default_config_label": "(account default)",
        "restart_options_fields": {
            "flags": "",
            "pw": "none",
            "env": "",
            "config_dir": "(account default)",
        },
        "restart_options_active": 0,
        "restart_options_cursor": 0,
        "restart_options_commands": [],
        "delete_entries": [],
        "delete_selected": 0,
        "delete_step": "select",
        "delete_running": set(),
        "delete_search_active": False,
        "delete_query": "",
        "delete_cursor": 0,
        "delete_filtered": [],
        "delete_running_count": 0,
        "delete_target_entry": {},
        "delete_warnings": [],
        "config_selected": 0,
        "config_action_selected": 0,
        "config_file_label": "",
        "config_file_path": "",
        "config_show_lines": [],
        "config_show_scroll": 0,
        "hooks_drift": {},
        "hooks_selected": 0,
        "hooks_event_selected": 0,
        "hooks_account": "",
        "hooks_target_event": "",
        "hooks_sync_scope": "",
        "hooks_add_type": "command",
        "hooks_add_matcher": "",
        "hooks_add_value": "",
        "hooks_add_async": True,
        "hooks_add_timeout": "",
        "hooks_add_field": 2,
        "hooks_add_cursor": 0,
        "hooks_add_editing": False,
        "hooks_add_edit_idx": None,
        "hooks_info_handler": None,
        "hooks_info_matcher": None,
        "hooks_info_event": "",
        "peek_lines": [],
        "peek_scroll": 0,
        "peek_extent": "screen",
        "peek_session_title": "",
        "peek_error": "",
        "peek_search_query": "",
        "peek_search_active": False,
        "peek_search_cursor": 0,
        "peek_match_lines": [],
        "peek_match_index": 0,
        "actlog_lines": [],
        "actlog_entries": [],
        "actlog_selected": 0,
        "actlog_detail_entry": {},
        "actlog_scroll": 0,
        "actlog_session_title": "",
        "actlog_session_id": "",
        "actlog_summary": {},
        "actlog_search_query": "",
        "actlog_search_active": False,
        "actlog_search_cursor": 0,
        "actlog_match_lines": [],
        "actlog_match_index": 0,
        "actlog_browse_entries": [],
        "actlog_browse_selected": 0,
        "actlog_browse_scroll": 0,
        "actlog_browse_search_active": False,
        "actlog_browse_search_query": "",
        "actlog_browse_search_cursor": 0,
        "actlog_browse_match_lines": [],
        "actlog_browse_match_index": 0,
        "actlog_gsearch_query": "",
        "actlog_gsearch_cursor": 0,
        "actlog_gsearch_results": [],
        "actlog_gsearch_selected": 0,
        "actlog_gsearch_scroll": 0,
        "actlog_gsearch_last_query": "",
        "actlog_gsearch_debounce_ticks": 0,
        "actlog_gsearch_loading": False,
        "actlog_gsearch_total_scanned": 0,
        "actlog_gsearch_log_index": [],
        "actlog_gsearch_search_active": True,
        "actlog_gsearch_parent_mode": "list",
        "prefs_values": {},
        "prefs_selected": 0,
        "prefs_editing": False,
        "prefs_cursor": 0,
        "prefs_edit_value": "",
        "prefs_array_items": [],
        "prefs_array_selected": 0,
        "prefs_array_editing": False,
        "prefs_array_cursor": 0,
        "prefs_array_edit_value": "",
        "prefs_array_field_key": "",
        "prefs_ext_edit_path": "",
        "prefs_ext_edit_field": "",
        "default_account": default_account,
        "default_pw": default_pw,
        "new_fields": {},
        "new_active": 0,
        "new_cursor": 0,
        "form_mode": "new",
        "edit_original_title": "",
        "dir_browse_active": False,
        "dir_browse_entries": [],
        "dir_browse_selected": 0,
        "dir_browse_scroll": 0,
        "dir_browse_show_hidden": False,
        "cycle_pw": _cycle_pw,
        "cycle_instance": _cycle_instance,
        "query_all_sessions": query_all_sessions,
        "sort_pick_entries": _sort_pick_entries,
        "build_delete_entries": _build_delete_entries,
        "strip_override_marker": _strip_override_marker,
        "append_override_marker": _append_override_marker,
        "append_override_for_dir": _append_override_for_dir,
        "has_override_emoji": _has_override_emoji,
        "override_emojis": OVERRIDE_EMOJIS,
        "spawn_detached_close": _spawn_detached_close,
        "compute_insert_location": compute_insert_location,
        "do_close": do_close,
        "do_cross_instance_focus": do_cross_instance_focus,
        "do_launch": do_launch,
        "do_move": do_move,
        "open_terminal": open_terminal,
        "do_restart": do_restart,
        "generate_clone_title": generate_clone_title,
        "do_rename": do_rename,
        "color_selected": 0,
        "palette_names": PALETTE_NAMES,
        "do_set_color": do_set_color,
        "do_edit_notes": do_edit_notes,
        "do_toggle_mute": do_toggle_mute,
        "kitty_cmd": kitty_cmd,
        "do_create_worktree": do_create_worktree,
        "resolve_session_id": resolve_session_id,
        "load_activity_log": load_activity_log,
        "format_log_entry": _format_log_entry,
        "get_window_text": get_window_text,
        "rstrip_blank_lines": _rstrip_blank_lines,
    }


def _attach_state_helpers(state: dict[str, Any]) -> None:
    def make_new_fields():
        return _make_new_fields_helper(
            state["default_account"],
            state["default_pw"],
            state["multi_instance"],
            state["home_instance_label"],
        )

    def reload_all_config():
        (
            state["default_account"],
            state["default_pw"],
            state["refresh_interval"],
            state["refresh_threshold"],
        ) = _reload_all_config_helper()

    def refilter():
        state["filtered"], state["selected"] = _refilter_helper(
            state["sessions"],
            state["search_query"],
            state["account_filter"],
            state["sort_mode"],
            state["filtered"],
            state["selected"],
            group_filter=state["group_filter"],
        )

    def save_ui_state_callback():
        save_ui_state(
            {
                "sort_mode": state["sort_mode"],
                "account_filter": sorted(state["account_filter"])
                if state["account_filter"]
                else None,
                "group_filter": sorted(state["group_filter"])
                if state["group_filter"]
                else None,
                "collapsed_groups": sorted(state["collapsed_groups"]),
                "show_detail": state["show_detail"],
                "show_all": state["show_all"],
            }
        )

    state["make_new_fields"] = make_new_fields
    state["reload_all_config"] = reload_all_config
    state["refilter"] = refilter
    state["_save_ui_state"] = save_ui_state_callback
    state["new_fields"] = make_new_fields()


def _apply_default_sort(state: dict[str, Any]) -> None:
    cfg_sort = _cfg._default_sort_sessions
    if cfg_sort and (cfg_sort in SORT_MODES or cfg_sort == "alpha"):
        state["sort_mode"] = cfg_sort


def _restore_persisted_ui_state(state: dict[str, Any]) -> None:
    persisted = load_ui_state()
    valid_accounts = set(_cfg._account_names)
    valid_groups: set[str] = set()
    for s in state["sessions"]:
        valid_groups.update(_session_groups(s))
    prune_stale_entries(persisted, valid_accounts, valid_groups)

    if persisted.get("sort_mode", "default") != "default":
        state["sort_mode"] = persisted["sort_mode"]
    account_filter = persisted.get("account_filter")
    if isinstance(account_filter, list) and account_filter:
        state["account_filter"] = set(account_filter)
    group_filter = persisted.get("group_filter")
    if isinstance(group_filter, list) and group_filter:
        state["group_filter"] = set(group_filter)
    collapsed_groups = persisted.get("collapsed_groups")
    if isinstance(collapsed_groups, list):
        state["collapsed_groups"] = set(collapsed_groups)
    if persisted.get("show_detail"):
        state["show_detail"] = True
    if persisted.get("show_all"):
        state["show_all"] = True
        state["sessions"] = query_all_sessions(managed_only=False)
    state["refilter"]()
