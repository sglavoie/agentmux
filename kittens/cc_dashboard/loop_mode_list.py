"""List mode handler for the dashboard event loop."""

import curses
from typing import Any

from cc_dashboard.remote.activity import list_activity_logs


def handle_list_mode(stdscr, key: int, state: dict[str, Any]) -> bool:
    """Handle key events while dashboard is in list mode.

    Returns True when the dashboard loop should exit, otherwise False.
    """
    if state["search_active"]:
        if key == 27:  # Escape -- cancel search
            state["search_active"] = False
            state["search_query"] = ""
            state["search_cursor"] = 0
            state["refilter"]()
            return False
        if key in (10, 13, curses.KEY_ENTER):  # Accept filter
            state["search_active"] = False
            return False
        if key in (curses.KEY_UP, ord("k"), ord("K")) and state["filtered"]:
            state["selected"] = (state["selected"] - 1) % len(state["filtered"])
            return False
        if key in (curses.KEY_DOWN, ord("j"), ord("J")) and state["filtered"]:
            state["selected"] = (state["selected"] + 1) % len(state["filtered"])
            return False
        if key in (curses.KEY_BACKSPACE, 127, 8):
            if state["search_cursor"] > 0:
                state["search_query"] = (
                    state["search_query"][: state["search_cursor"] - 1]
                    + state["search_query"][state["search_cursor"] :]
                )
                state["search_cursor"] -= 1
                state["refilter"]()
            return False
        if key == curses.KEY_LEFT:
            state["search_cursor"] = max(0, state["search_cursor"] - 1)
            return False
        if key == curses.KEY_RIGHT:
            state["search_cursor"] = min(
                len(state["search_query"]), state["search_cursor"] + 1
            )
            return False
        if 32 <= key < 127:
            state["search_query"] = (
                state["search_query"][: state["search_cursor"]]
                + chr(key)
                + state["search_query"][state["search_cursor"] :]
            )
            state["search_cursor"] += 1
            state["refilter"]()
            return False
        return False

    if state["account_filter_mode"] and ord("1") <= key <= ord("9"):
        idx = key - ord("1")
        if idx < len(state["cfg"]._account_names):
            name = state["cfg"]._account_names[idx]
            if state["account_filter"] is None:
                state["account_filter"] = {name}
            elif name in state["account_filter"]:
                state["account_filter"].discard(name)
                if not state["account_filter"]:
                    state["account_filter"] = None
            else:
                state["account_filter"].add(name)
            state["refilter"]()
        return False

    if key in (ord("q"), ord("Q")):
        return True
    if key == 27:
        if state["marked"]:
            state["marked"].clear()
        elif state["account_filter_mode"]:
            state["account_filter_mode"] = False
            state["account_filter"] = None
            state["refilter"]()
        elif state["search_query"]:
            state["search_query"] = ""
            state["search_cursor"] = 0
            state["refilter"]()
        elif state["account_filter"] is not None:
            state["account_filter"] = None
            state["refilter"]()
        else:
            return True
    elif key == ord("f"):
        state["account_filter_mode"] = not state["account_filter_mode"]
    elif key == ord("/"):
        state["search_active"] = True
        state["search_query"] = ""
        state["search_cursor"] = 0
        state["refilter"]()
    elif key in (curses.KEY_UP, ord("k"), ord("K")) and state["filtered"]:
        state["selected"] = (state["selected"] - 1) % len(state["filtered"])
    elif key in (curses.KEY_DOWN, ord("j"), ord("J")) and state["filtered"]:
        state["selected"] = (state["selected"] + 1) % len(state["filtered"])
    elif key == ord(" ") and state["filtered"]:
        tid = state["filtered"][state["selected"]]["tab_id"]
        state["marked"].symmetric_difference_update({tid})
        state["selected"] = min(state["selected"] + 1, len(state["filtered"]) - 1)
    elif key == ord("U") and state["marked"]:
        state["marked"].clear()
    elif key in (10, 13, curses.KEY_ENTER) and state["filtered"]:
        sess = state["filtered"][state["selected"]]
        state["do_cross_instance_focus"](
            sess.get("socket", state["home_socket"]),
            sess["tab_id"],
            state["home_socket"],
        )
        return True
    elif key in (ord("n"), ord("N")):
        state["mode"] = "new"
        state["form_mode"] = "new"
        state["new_fields"] = state["make_new_fields"]()
        state["new_active"] = 0
        state["new_cursor"] = 0
        state["dir_browse_active"] = False
    elif key == ord("e") and state["filtered"]:
        sess = state["filtered"][state["selected"]]
        stripped = state["strip_override_marker"](sess["title"])
        cfg_entry = state["cfg"]._find_config_session(stripped)
        if cfg_entry is None:
            state["msg"] = "Session not in config (use S to save first)"
            state["msg_frames"] = 20
        else:
            state["mode"] = "new"
            state["form_mode"] = "edit"
            state["edit_original_title"] = cfg_entry["title"]
            pw_val = cfg_entry.get("pw", "")
            state["new_fields"] = {
                "title": cfg_entry["title"],
                "dir": cfg_entry["dir"],
                "account": cfg_entry["account"],
                "flags": cfg_entry.get("flags", ""),
                "pw": pw_val if pw_val else "none",
                "env": cfg_entry.get("env", ""),
            }
            state["new_active"] = 0
            state["new_cursor"] = len(state["new_fields"]["title"])
            state["dir_browse_active"] = False
    elif key == ord("l"):
        state["reload_all_config"]()
        if state["cfg"]._sessions:
            state["mode"] = "pick"
            state["pick_selected"] = 0
            state["pick_running"] = {s["title"] for s in state["sessions"]}
            (
                state["pick_filtered"],
                state["pick_running_count"],
            ) = state["sort_pick_entries"](
                list(state["cfg"]._sessions), state["pick_running"]
            )
            state["pick_query"] = ""
            state["pick_cursor"] = 0
            state["pick_search_active"] = False
            state["pick_marked"] = set()
            state["pick_target_instance"] = state["home_instance_label"]
        else:
            state["msg"] = (
                f"No sessions configured in {state['cfg']._sessions_config_filename()}"
            )
            state["msg_frames"] = 20
    elif key == ord("D"):
        state["reload_all_config"]()
        if state["cfg"]._sessions:
            state["mode"] = "delete"
            state["delete_step"] = "select"
            state["delete_running"] = {s["title"] for s in state["sessions"]}
            state["delete_entries"] = state["build_delete_entries"](
                state["cfg"]._sessions
            )
            (
                state["delete_filtered"],
                state["delete_running_count"],
            ) = state["sort_pick_entries"](
                state["delete_entries"], state["delete_running"]
            )
            state["delete_selected"] = 0
            state["delete_query"] = ""
            state["delete_cursor"] = 0
            state["delete_search_active"] = False
        else:
            state["msg"] = (
                f"No sessions configured in {state['cfg']._sessions_config_filename()}"
            )
            state["msg_frames"] = 20
    elif key == ord("a"):
        state["show_all"] = not state["show_all"]
        state["sessions"] = state["query_all_sessions"](
            managed_only=(not state["show_all"])
        )
        state["refilter"]()
        label = "all" if state["show_all"] else "managed"
        state["msg"] = f"Showing {label} sessions ({len(state['filtered'])} found)"
        state["msg_frames"] = 20
    elif key == ord("d"):
        state["show_detail"] = not state["show_detail"]
    elif key == ord("x") and state["filtered"]:
        if state["marked"]:
            state["actions_targets"] = [
                s for s in state["filtered"] if s["tab_id"] in state["marked"]
            ]
            state["batch_targets"] = list(state["actions_targets"])
            state["mode"] = "confirm_batch_close"
        else:
            state["actions_targets"] = [state["filtered"][state["selected"]]]
            state["actions_scope"] = "selected"
            state["mode"] = "confirm"
    elif key == ord("t") and state["filtered"]:
        state["actions_targets"] = []
        if state["marked"]:
            state["actions_targets"] = [
                s for s in state["filtered"] if s["tab_id"] in state["marked"]
            ]
        if state["actions_targets"]:
            state["actions_scope"] = "marked"
            state["msg"] = (
                f"Actions: {len(state['actions_targets'])} marked (restart/close/move)"
            )
        else:
            state["actions_scope"] = "selected"
            state["actions_targets"] = [state["filtered"][state["selected"]]]
            state["msg"] = "Actions: selected (all actions)"
        state["mode"] = "actions"
        state["actions_selected"] = 0
        state["msg_frames"] = 18
    elif key == ord("s"):
        state["mode"] = "sort_pick"
        state["sort_selected"] = state["sort_modes"].index(state["sort_mode"])
    elif key == ord("p") and state["filtered"]:
        sess = state["filtered"][state["selected"]]
        win_id = sess.get("claude_window_id", 0)
        sess_socket = sess.get("socket", state["home_socket"])
        state["peek_session_title"] = sess["title"]
        state["peek_extent"] = "screen"
        state["peek_scroll"] = 0
        state["peek_search_query"] = ""
        state["peek_search_active"] = False
        state["peek_search_cursor"] = 0
        state["peek_match_lines"] = []
        state["peek_match_index"] = 0
        if not win_id:
            state["peek_error"] = "No window ID available for this session"
            state["peek_lines"] = []
        elif state["host_window_id"] and win_id == state["host_window_id"]:
            state["peek_error"] = "Dashboard overlay is active on this window"
            state["peek_lines"] = []
        else:
            text = state["get_window_text"](sess_socket, win_id, state["peek_extent"])
            if text is None:
                state["peek_error"] = "Failed to capture window text"
                state["peek_lines"] = []
            else:
                state["peek_error"] = ""
                state["peek_lines"] = state["rstrip_blank_lines"](text.splitlines())
        state["mode"] = "peek"
    elif key == 18:  # Ctrl+r -- refresh
        state["sessions"] = state["query_all_sessions"](
            managed_only=(not state["show_all"])
        )
        state["marked"] &= {s["tab_id"] for s in state["sessions"]}
        state["refilter"]()
        state["msg"] = f"{len(state['filtered'])} session(s) found"
        state["msg_frames"] = 20
    elif key == ord("S"):
        if state["sessions"]:
            state["mode"] = "confirm_save"
        else:
            state["msg"] = "No sessions to save"
            state["msg_frames"] = 20
    elif key == ord("C"):
        state["mode"] = "config_pick"
        state["config_selected"] = 0
    elif key == ord("H"):
        state["cfg"].load_hooks()
        state["hooks_drift"] = state["cfg"]._detect_all_drift()
        state["hooks_selected"] = 0
        state["mode"] = "hooks"
    elif key == 6 and not state["search_active"]:  # Ctrl+F -- global log search
        log_index = list_activity_logs(state["sessions"])
        if log_index:
            state["actlog_gsearch_query"] = ""
            state["actlog_gsearch_cursor"] = 0
            state["actlog_gsearch_results"] = []
            state["actlog_gsearch_selected"] = 0
            state["actlog_gsearch_scroll"] = 0
            state["actlog_gsearch_last_query"] = ""
            state["actlog_gsearch_debounce_ticks"] = 0
            state["actlog_gsearch_loading"] = False
            state["actlog_gsearch_total_scanned"] = 0
            state["actlog_gsearch_log_index"] = log_index
            state["actlog_gsearch_search_active"] = True
            state["mode"] = "actlog_gsearch"
        else:
            state["msg"] = "No activity logs found"
            state["msg_frames"] = 20
    elif key == ord("L") and state["filtered"]:
        sess = state["filtered"][state["selected"]]
        sid = state["resolve_session_id"](sess["tab_id"])
        if sid:
            entries, summary = state["load_activity_log"](sid)
            if entries:
                state["actlog_session_title"] = sess["title"]
                state["actlog_session_id"] = sid
                state["actlog_summary"] = summary
                screen_width = stdscr.getmaxyx()[1]
                state["actlog_lines"] = [
                    state["format_log_entry"](entry, screen_width) for entry in entries
                ]
                state["actlog_scroll"] = max(
                    0, len(state["actlog_lines"]) - (stdscr.getmaxyx()[0] - 5)
                )
                state["actlog_search_query"] = ""
                state["actlog_search_active"] = False
                state["actlog_search_cursor"] = 0
                state["actlog_match_lines"] = []
                state["actlog_match_index"] = 0
                state["mode"] = "actlog"
            else:
                state["msg"] = "No activity log entries for this session"
                state["msg_frames"] = 20
        else:
            state["msg"] = "No activity log found for this session"
            state["msg_frames"] = 20

    return False
