"""Main event loop and session fingerprinting for the Claude Code Dashboard."""

import curses
import os
from typing import Any

from cc_dashboard import config as _cfg
from cc_dashboard.colors import init_colors
from cc_dashboard.config_data import PALETTE_NAMES
from cc_dashboard.filters import _apply_filters, _apply_sort, _sort_pick_entries
from cc_dashboard.loop_dashboard_dispatch import dispatch_mode
from cc_dashboard.loop_dashboard_draw import build_draw_state
from cc_dashboard.loop_helpers import (
    _build_delete_entries,
    _rstrip_blank_lines,
    _session_fingerprint,
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
from cc_dashboard.loop_render import draw_dashboard_frame
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
)
from cc_dashboard.remote.actions_core import do_edit_notes, do_set_color
from cc_dashboard.remote.activity import (
    load_activity_log,
    resolve_session_id,
    search_all_logs,
)
from cc_dashboard.remote.command import (
    get_all_kitty_sockets,
    get_host_tab_id,
    get_kitty_socket,
    get_window_text,
)
from cc_dashboard.remote.git_ops import do_create_worktree
from cc_dashboard.remote.sessions import query_all_sessions
from cc_dashboard.remote.shared import (
    OVERRIDE_MARKER,
    _append_override_marker,
    _strip_override_marker,
)
from cc_dashboard.views import (
    _check_idle_notifications,
    _cycle_instance,
    _cycle_pw,
    _format_log_entry,
)


def dashboard(stdscr):
    """Run the interactive dashboard loop."""
    curses.set_escdelay(25)
    curses.curs_set(0)
    init_colors()
    stdscr.timeout(100)

    home_socket = os.environ.get("KITTY_LISTEN_ON", "") or get_kitty_socket()
    discovered_sockets = get_all_kitty_sockets()
    instance_labels = [label for _, label in discovered_sockets]
    instance_socket_map = {label: sock for sock, label in discovered_sockets}

    home_instance_label = ""
    for sock, label in discovered_sockets:
        if sock == home_socket:
            home_instance_label = label
            break
    if not home_instance_label and instance_labels:
        home_instance_label = instance_labels[0]

    sessions = query_all_sessions(managed_only=True)
    selected = 0
    for i, sess in enumerate(sessions):
        if sess["status"] == "focused":
            selected = i
            break

    host_tab_id = get_host_tab_id(home_socket)
    _host_wid_str = os.environ.get("KITTY_WINDOW_ID", "")
    host_window_id = int(_host_wid_str) if _host_wid_str.isdigit() else 0

    default_account = _cfg._account_names[0] if _cfg._account_names else "work"
    default_pw = (
        _cfg._default_project_window if _cfg._default_project_window else "none"
    )
    refresh_interval = _cfg._default_dashboard_refresh

    state: dict[str, Any] = {
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
        "search_query": "",
        "search_active": False,
        "search_cursor": 0,
        "sort_mode": "default",
        "account_filter": None,
        "account_filter_mode": False,
        "group_filter": None,
        "group_filter_mode": False,
        "collapsed_groups": set(),
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
        "spawn_detached_close": _spawn_detached_close,
        "compute_insert_location": compute_insert_location,
        "do_close": do_close,
        "do_cross_instance_focus": do_cross_instance_focus,
        "do_launch": do_launch,
        "do_move": do_move,
        "do_restart": do_restart,
        "generate_clone_title": generate_clone_title,
        "do_rename": do_rename,
        "color_selected": 0,
        "palette_names": PALETTE_NAMES,
        "do_set_color": do_set_color,
        "do_edit_notes": do_edit_notes,
        "do_create_worktree": do_create_worktree,
        "resolve_session_id": resolve_session_id,
        "load_activity_log": load_activity_log,
        "format_log_entry": _format_log_entry,
        "get_window_text": get_window_text,
        "rstrip_blank_lines": _rstrip_blank_lines,
        "override_marker": OVERRIDE_MARKER,
    }

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

    def build_session_actions(sess, scope="selected"):
        return _build_session_actions_helper(sess, scope, state["multi_instance"])

    state["make_new_fields"] = make_new_fields
    state["reload_all_config"] = reload_all_config
    state["refilter"] = refilter
    state["new_fields"] = make_new_fields()

    while True:
        draw_dashboard_frame(stdscr, build_draw_state(state), build_session_actions)
        stdscr.refresh()

        if state["msg_frames"] > 0:
            state["msg_frames"] -= 1
            if state["msg_frames"] == 0:
                state["msg"] = ""

        key = stdscr.getch()
        if key == curses.KEY_RESIZE:
            continue

        if key == -1:
            if (
                state["mode"] == "list"
                and not state["search_active"]
                and state["refresh_threshold"] > 0
            ):
                state["refresh_ticks"] += 1
                if state["refresh_ticks"] >= state["refresh_threshold"]:
                    state["refresh_ticks"] = 0
                    reload_all_config()
                    new_sessions = query_all_sessions(
                        managed_only=(not state["show_all"])
                    )
                    _check_idle_notifications(new_sessions)
                    old_fp = _session_fingerprint(state["filtered"])
                    new_filtered = _apply_sort(
                        _apply_filters(
                            new_sessions,
                            state["search_query"],
                            state["account_filter"],
                            state["group_filter"],
                        ),
                        state["sort_mode"],
                    )
                    if _session_fingerprint(new_filtered) != old_fp:
                        old_tab_id = (
                            state["filtered"][state["selected"]]["tab_id"]
                            if state["filtered"]
                            and state["selected"] < len(state["filtered"])
                            else None
                        )
                        state["sessions"] = new_sessions
                        state["marked"] &= {s["tab_id"] for s in state["sessions"]}
                        state["filtered"] = new_filtered
                        state["selected"] = 0
                        if old_tab_id is not None:
                            for i, sess in enumerate(state["filtered"]):
                                if sess["tab_id"] == old_tab_id:
                                    state["selected"] = i
                                    break
                        state["selected"] = min(
                            state["selected"],
                            max(0, len(state["filtered"]) - 1),
                        )
            if (
                state["mode"] == "actlog_gsearch"
                and state["actlog_gsearch_debounce_ticks"] > 0
            ):
                state["actlog_gsearch_debounce_ticks"] -= 1
                if state["actlog_gsearch_debounce_ticks"] == 0:
                    query = state["actlog_gsearch_query"]
                    if len(query) >= 3 and query != state["actlog_gsearch_last_query"]:
                        state["actlog_gsearch_loading"] = True
                        results, total_scanned = search_all_logs(
                            query,
                            state["actlog_gsearch_log_index"],
                            cols=stdscr.getmaxyx()[1],
                        )
                        state["actlog_gsearch_results"] = results
                        state["actlog_gsearch_total_scanned"] = total_scanned
                        state["actlog_gsearch_selected"] = 0
                        state["actlog_gsearch_scroll"] = 0
                        state["actlog_gsearch_last_query"] = query
                        state["actlog_gsearch_loading"] = False
            continue

        state["refresh_ticks"] = 0
        should_exit, should_continue = dispatch_mode(
            stdscr,
            key,
            state,
            build_session_actions,
        )
        if should_exit:
            return
        if state.pop("force_refresh", False):
            state["refresh_ticks"] = state["refresh_threshold"]
        if should_continue:
            continue
